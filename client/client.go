package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/transport"
	"github.com/otfabric/go-s7comm/wire"
)

// ErrNotConnected is returned when an operation requires an active connection but the client is not connected.
var ErrNotConnected = errors.New("not connected")

// ErrProtocolFailure marks errors from COTP/S7 decode, parse, or header validation (for ReadStatus classification).
var ErrProtocolFailure = errors.New("protocol failure")

// ErrRequestExceedsPDU is returned when a request size exceeds the negotiated PDU size (client-side constraint).
// Classified as protocol-error, not transport-error.
var ErrRequestExceedsPDU = errors.New("request exceeds negotiated PDU size")

// PDURefMismatchError indicates the response PDU reference did not match the request. Use errors.As to detect.
type PDURefMismatchError struct {
	Expected uint16
	Got      uint16
}

func (e *PDURefMismatchError) Error() string {
	return fmt.Sprintf("S7 response PDU ref mismatch: expected %d, got %d", e.Expected, e.Got)
}

// Client is an S7 protocol client. rack and slot hold the negotiated/detected values
// for the current connection; options are immutable after construction.
type Client struct {
	host          string
	opts          options
	conn          *transport.Conn
	rack          int
	slot          int
	reqMu         sync.Mutex
	mu            sync.RWMutex
	pduRef        uint32
	localTSAP     uint16
	remoteTSAP    uint16
	pduSize       int
	maxAmqCalling int
	maxAmqCalled  int
}

// New creates a new S7 client for the given host
func New(host string, opts ...Option) *Client {
	o := defaultOptions()
	for _, f := range opts {
		f(o)
	}
	return &Client{host: host, opts: *o}
}

func (c *Client) Connect(ctx context.Context) error {
	port := c.opts.port
	timeout := c.opts.timeout
	maxPDU := c.opts.maxPDU
	rack := c.opts.rack
	slot := c.opts.slot
	autoRackSlot := c.opts.autoRackSlot
	useExplicit := c.opts.useExplicit
	logger := c.opts.logger

	if port < 0 {
		return &ValidationError{Message: fmt.Sprintf("invalid port: %d", port)}
	}
	if timeout < 0 {
		return &ValidationError{Message: fmt.Sprintf("invalid timeout: %v", timeout)}
	}
	if maxPDU < 1 {
		return &ValidationError{Message: fmt.Sprintf("invalid max PDU: %d", maxPDU)}
	}
	if !autoRackSlot {
		if rack < 0 || slot < 0 {
			return &ValidationError{Message: fmt.Sprintf("invalid rack/slot: rack=%d slot=%d", rack, slot)}
		}
		if err := wire.ValidateRackSlot(rack, slot); err != nil {
			return &ValidationError{Message: err.Error()}
		}
	}

	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	if autoRackSlot && !useExplicit {
		err := c.autoConnect(ctx)
		if err != nil && logger != nil {
			logger.Error("Connect failed", err)
		}
		return err
	}
	err := c.connectOnce(ctx, rack, slot)
	if err != nil && logger != nil {
		logger.Error("Connect failed", err)
	}
	return err
}

func (c *Client) autoConnect(ctx context.Context) error {
	if c.opts.bruteRackSlot {
		for rack := 0; rack <= 3; rack++ {
			for slot := 0; slot <= 5; slot++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := c.connectOnce(ctx, rack, slot); err == nil {
					return nil
				}
			}
		}
		return fmt.Errorf("auto rack/slot connect failed for rack 0-3 slot 0-5")
	}

	common := [][2]int{{0, 1}, {0, 2}, {0, 0}, {1, 1}, {0, 3}}
	for _, rs := range common {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.connectOnce(ctx, rs[0], rs[1]); err == nil {
			return nil
		}
	}

	return fmt.Errorf("auto rack/slot connect failed on common rack/slot pairs")
}

func (c *Client) connectOnce(ctx context.Context, rack, slot int) error {
	addr := net.JoinHostPort(c.host, fmt.Sprint(c.opts.port))
	timeout := c.opts.timeout
	useExplicit := c.opts.useExplicit
	localTSAP, remoteTSAP := c.opts.localTSAP, c.opts.remoteTSAP
	maxAmqCalling, maxAmqCalled, maxPDU := c.opts.maxAmqCalling, c.opts.maxAmqCalled, c.opts.maxPDU

	// Dial and handshake the new connection first; only swap into the client after success
	// so a failed reconnect does not drop an existing healthy session.
	conn, err := dialTransport(ctx, addr, timeout)
	if err != nil {
		return err
	}
	var local, remote uint16
	if useExplicit {
		local, remote = localTSAP, remoteTSAP
	} else {
		var err error
		local, err = wire.BuildTSAP(1, 0, 0)
		if err != nil {
			_ = conn.Close()
			return &ValidationError{Message: err.Error()}
		}
		remote, err = wire.BuildTSAP(3, rack, slot)
		if err != nil {
			_ = conn.Close()
			return &ValidationError{Message: err.Error()}
		}
	}
	if err := performCOTPConnect(ctx, conn, local, remote); err != nil {
		_ = conn.Close()
		return fmt.Errorf("COTP connect: %w", err)
	}
	setup, err := performS7Setup(ctx, conn, 1, maxAmqCalling, maxAmqCalled, maxPDU)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("S7 setup: %w", err)
	}
	c.mu.Lock()
	oldConn := c.conn
	c.conn = conn
	c.rack, c.slot = rack, slot
	c.localTSAP, c.remoteTSAP = local, remote
	c.pduSize = setup.PDUSize
	c.maxAmqCalling = setup.MaxAmqCalling
	c.maxAmqCalled = setup.MaxAmqCalled
	c.mu.Unlock()
	if oldConn != nil {
		_ = oldConn.Close()
	}
	return nil
}

func (c *Client) closeConnLocked() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.rack, c.slot = 0, 0
	c.localTSAP, c.remoteTSAP = 0, 0
	c.pduSize = 0
	c.maxAmqCalling, c.maxAmqCalled = 0, 0
	return err
}

func (c *Client) nextPDURef() uint16 {
	return uint16(atomic.AddUint32(&c.pduRef, 1))
}

// sendReceive sends req and returns response param/data. When expectedPDURef is non-zero,
// the response S7 header PDU reference must match (request/response correlation).
func (c *Client) sendReceive(ctx context.Context, req []byte, expectedPDURef uint16) ([]byte, []byte, error) {
	c.mu.RLock()
	conn := c.conn
	pduSize := c.pduSize
	c.mu.RUnlock()
	if conn == nil {
		return nil, nil, ErrNotConnected
	}
	if pduSize > 0 && len(req) > pduSize {
		return nil, nil, fmt.Errorf("request size %d exceeds negotiated PDU size %d: %w", len(req), pduSize, ErrRequestExceedsPDU)
	}
	dtBytes, err := wire.EncodeCOTPDT(req)
	if err != nil {
		return nil, nil, fmt.Errorf("encode COTP DT: %w", errors.Join(err, ErrProtocolFailure))
	}
	if err := conn.SendContext(ctx, dtBytes); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	resp, err := conn.ReceiveContext(ctx)
	if err != nil {
		return nil, nil, err
	}

	dec, err := cotp.Decode(resp)
	if err != nil {
		return nil, nil, fmt.Errorf("decode COTP: %w", errors.Join(err, ErrProtocolFailure))
	}
	if dec.DT == nil {
		return nil, nil, fmt.Errorf("expected COTP DT, got %s: %w", dec.Type, ErrProtocolFailure)
	}
	s7Data := dec.DT.UserData

	header, rest, err := wire.ParseS7Header(s7Data)
	if err != nil {
		return nil, nil, fmt.Errorf("parse S7 header: %w", errors.Join(err, ErrProtocolFailure))
	}

	if expectedPDURef != 0 && header.PDURef != expectedPDURef {
		return nil, nil, errors.Join(ErrProtocolFailure, &PDURefMismatchError{Expected: expectedPDURef, Got: header.PDURef})
	}

	if header.ErrorClass != 0 || header.ErrorCode != 0 {
		var param []byte
		if pl := int(header.ParamLength); pl > 0 && len(rest) >= pl {
			param = rest[:pl]
		}
		return nil, nil, wire.NewS7ErrorWithParam(header.ErrorClass, header.ErrorCode, param)
	}

	need := int(header.ParamLength) + int(header.DataLength)
	if len(rest) < need {
		return nil, nil, fmt.Errorf("short S7 payload: need %d bytes, got %d: %w", need, len(rest), ErrProtocolFailure)
	}

	paramData := rest[:header.ParamLength]
	dataData := rest[header.ParamLength : header.ParamLength+header.DataLength]

	return paramData, dataData, nil
}

// Close closes the connection
func (c *Client) Close() error {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeConnLocked()
}

// ConnectionInfo returns a snapshot of the negotiated connection state (host, port, TSAPs, rack, slot,
// PDUSize, MaxAmqCalling, MaxAmqCalled). Safe to call from any goroutine. When not connected, zero values
// are returned. This is the single source for negotiated limits after Connect.
func (c *Client) ConnectionInfo() model.ConnectionInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return model.ConnectionInfo{
		Host:          c.host,
		Port:          c.opts.port,
		LocalTSAP:     c.localTSAP,
		RemoteTSAP:    c.remoteTSAP,
		Rack:          c.rack,
		Slot:          c.slot,
		PDUSize:       c.pduSize,
		MaxAmqCalling: c.maxAmqCalling,
		MaxAmqCalled:  c.maxAmqCalled,
	}
}

// PDUSize returns the negotiated PDU size
func (c *Client) PDUSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.pduSize
}
