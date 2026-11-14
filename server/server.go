// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/otfabric/go-cotp"
)

// Bounds of Config.MaxPDUSize. Setup Communication grants the smaller of
// the client's request and Config.MaxPDUSize.
const (
	MinPDUSize        = 240
	MaxPDUSize        = 4096
	DefaultPDUSize    = 480
	DefaultMaxConns   = 32
	DefaultHandshake  = 5 * time.Second
	defaultMaxTPDULen = 1024
)

// ErrServerClosed is returned by Serve and ListenAndServe after Close.
var ErrServerClosed = errors.New("s7 server: closed")

// Config configures a Server. The zero value is valid.
type Config struct {
	// MaxPDUSize is the largest PDU size Setup Communication grants,
	// MinPDUSize..MaxPDUSize. Zero means DefaultPDUSize.
	MaxPDUSize int
	// MaxConnections is the number of clients served at once; further
	// connections are closed on accept. Zero means DefaultMaxConns.
	MaxConnections int
	// HandshakeTimeout bounds the COTP connect of a new client. Zero means DefaultHandshake.
	HandshakeTimeout time.Duration
	// IdleTimeout closes a connection that sends no request for this long,
	// or that does not take a response within this long. Zero means no limit.
	IdleTimeout time.Duration
	// Logger receives connection and request diagnostics. Nil discards them.
	Logger *slog.Logger
}

func (c Config) withDefaults() (Config, error) {
	if c.MaxPDUSize == 0 {
		c.MaxPDUSize = DefaultPDUSize
	}
	if c.MaxPDUSize < MinPDUSize || c.MaxPDUSize > MaxPDUSize {
		return c, fmt.Errorf("s7 server: MaxPDUSize %d not in %d..%d", c.MaxPDUSize, MinPDUSize, MaxPDUSize)
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = DefaultMaxConns
	}
	if c.MaxConnections < 0 {
		return c, fmt.Errorf("s7 server: MaxConnections %d is negative", c.MaxConnections)
	}
	if c.HandshakeTimeout == 0 {
		c.HandshakeTimeout = DefaultHandshake
	}
	if c.HandshakeTimeout < 0 || c.IdleTimeout < 0 {
		return c, errors.New("s7 server: negative timeout")
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return c, nil
}

// Server answers S7 communication on ISO-on-TCP (RFC 1006) from memory it
// holds: Setup Communication, Read Var and Write Var on the registered areas.
// Other jobs and user data (SZL, blocks, control) get header error 0x8104
// (service not implemented). It accepts any rack, slot and TSAP.
type Server struct {
	cfg Config
	mem *memory

	mu        sync.Mutex
	listeners map[net.Listener]struct{}
	conns     map[net.Conn]struct{}
	closed    bool
	wg        sync.WaitGroup
	slots     chan struct{}
}

// New returns a Server with no areas registered.
func New(cfg Config) (*Server, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:       cfg,
		mem:       newMemory(),
		listeners: make(map[net.Listener]struct{}),
		conns:     make(map[net.Conn]struct{}),
		slots:     make(chan struct{}, cfg.MaxConnections),
	}, nil
}

// RegisterDB adds data block number (1..65535) holding a copy of data.
func (s *Server) RegisterDB(number int, data []byte, opts AreaOptions) error {
	return s.mem.register(AreaDB, number, data, opts)
}

// RegisterArea adds the inputs, outputs or markers area holding a copy of data.
func (s *Server) RegisterArea(area Area, data []byte, opts AreaOptions) error {
	if area == AreaDB {
		return fmt.Errorf("%w: use RegisterDB for data blocks", ErrUnknownArea)
	}
	return s.mem.register(area, 0, data, opts)
}

// Read returns a copy of size bytes at start of an area. db is the DB
// number for AreaDB and ignored otherwise.
func (s *Server) Read(area Area, db, start, size int) ([]byte, error) {
	return s.mem.read(area, db, start, size)
}

// Write changes an area from the host side; ReadOnly does not apply.
func (s *Server) Write(area Area, db, start int, data []byte) error {
	return s.mem.write(area, db, start, data)
}

// ListenAndServe listens on the TCP address and serves until ctx ends or Close.
func (s *Server) ListenAndServe(ctx context.Context, address string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve accepts connections on ln until ctx ends or Close, and closes ln.
// It returns ErrServerClosed after Close and ctx.Err() when ctx ends;
// connections being served are closed in both cases.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.track(ln) {
		_ = ln.Close()
		return ErrServerClosed
	}
	defer s.untrack(ln)
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	s.cfg.Logger.Info("s7 server listening", "address", ln.Addr().String())
	for {
		raw, err := ln.Accept()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if s.isClosed() {
				return ErrServerClosed
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		select {
		case s.slots <- struct{}{}:
		default:
			s.cfg.Logger.Warn("s7 connection refused: limit reached", "remote", raw.RemoteAddr().String(), "limit", s.cfg.MaxConnections)
			_ = raw.Close()
			continue
		}
		if !s.addConn(raw) {
			<-s.slots
			_ = raw.Close()
			return ErrServerClosed
		}
		s.wg.Go(func() {
			defer func() { <-s.slots }()
			defer s.removeConn(raw)
			s.serveConn(raw)
		})
	}
}

// Close stops all listeners and closes every connection, then waits for
// their goroutines. It is safe to call more than once.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return nil
	}
	s.closed = true
	for ln := range s.listeners {
		_ = ln.Close()
	}
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) track(ln net.Listener) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.listeners[ln] = struct{}{}
	return true
}

func (s *Server) untrack(ln net.Listener) {
	s.mu.Lock()
	delete(s.listeners, ln)
	s.mu.Unlock()
	_ = ln.Close()
}

func (s *Server) addConn(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) removeConn(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close()
}

// serveConn runs the COTP handshake and answers jobs until the peer leaves,
// sends a malformed PDU, or the server closes.
func (s *Server) serveConn(raw net.Conn) {
	log := s.cfg.Logger.With("remote", raw.RemoteAddr().String())
	hctx, cancel := context.WithTimeout(context.Background(), s.cfg.HandshakeTimeout)
	conn, err := cotp.Accept(hctx, raw, cotp.ServerConfig{
		MaxTPDULength: defaultMaxTPDULen,
		MaxTSDULength: s.cfg.MaxPDUSize,
	})
	cancel()
	if err != nil {
		log.Debug("s7 connection: COTP handshake failed", "error", err)
		return
	}
	defer func() { _ = conn.Close() }()
	log.Info("s7 connection open")
	sess := &session{srv: s, pduSize: s.cfg.MaxPDUSize, log: log}
	for {
		ctx, cancel := s.requestContext()
		req, err := conn.ReadTSDU(ctx)
		cancel()
		if err != nil {
			log.Info("s7 connection closed", "reason", err)
			return
		}
		out, keep := sess.handle(req)
		if !keep {
			log.Warn("s7 connection dropped: malformed PDU", "length", len(req))
			return
		}
		if out == nil {
			continue
		}
		wctx, wcancel := s.requestContext()
		err = conn.WriteTSDU(wctx, out)
		wcancel()
		if err != nil {
			log.Info("s7 connection closed", "reason", err)
			return
		}
	}
}

func (s *Server) requestContext() (context.Context, context.CancelFunc) {
	if s.cfg.IdleTimeout > 0 {
		return context.WithTimeout(context.Background(), s.cfg.IdleTimeout)
	}
	return context.Background(), func() {}
}
