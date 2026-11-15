// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/wire"
)

// Contract tests of Server (TESTING.md, "Lifecycle contract matrix"). They
// run in a synctest bubble over in-memory connections: synctest.Wait returns
// when every goroutine of the server is blocked, so a test knows that a
// read, a write or a handshake is waiting without sleeping, and a wait that
// can never end fails as a deadlock instead of hanging.

// pipeListener is a net.Listener whose connections are in-memory pipes.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// dial hands the server one end of a new pipe and returns the other.
func (l *pipeListener) dial(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	l.conns <- server
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// serving starts srv on a pipe listener inside the bubble. The result of
// Serve arrives on the returned channel.
func serving(t *testing.T, cfg Config) (*Server, *pipeListener, <-chan error) {
	t.Helper()
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.RegisterDB(1, make([]byte, 16), AreaOptions{}); err != nil {
		t.Fatalf("RegisterDB: %v", err)
	}
	ln := newPipeListener()
	result := make(chan error, 1)
	go func() { result <- srv.Serve(context.Background(), ln) }()
	return srv, ln, result
}

// requireClosed checks what Close promises once it has returned: Serve has
// ended with ErrServerClosed, no connection is registered and every
// connection slot is free, so no connection goroutine is running.
func requireClosed(t *testing.T, srv *Server, serveResult <-chan error) {
	t.Helper()
	synctest.Wait()
	select {
	case err := <-serveResult:
		if !errors.Is(err, ErrServerClosed) {
			t.Errorf("Serve = %v, want ErrServerClosed", err)
		}
	default:
		t.Error("Serve has not returned after Close")
	}
	srv.mu.Lock()
	conns := len(srv.conns)
	srv.mu.Unlock()
	if conns != 0 || len(srv.slots) != 0 {
		t.Errorf("after Close: %d connections registered, %d slots held", conns, len(srv.slots))
	}
}

// Close waits for every connection Serve has registered, also one whose
// goroutine has not started yet: it returns only after that goroutine ended.
func TestCloseWaitsForAConnectionNotYetServed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv, ln, serveResult := serving(t, Config{})
		hold := make(chan struct{})
		release := sync.OnceFunc(func() { close(hold) })
		t.Cleanup(release)
		srv.afterAddConn = func() { <-hold }

		ln.dial(t)
		synctest.Wait() // Serve has registered the connection and waits before starting its goroutine

		closed := make(chan struct{})
		go func() {
			_ = srv.Close()
			close(closed)
		}()
		synctest.Wait() // Close has closed the listener and the connection
		select {
		case <-closed:
			t.Fatal("Close returned before the goroutine of a registered connection had run")
		default:
		}

		release()
		synctest.Wait()
		select {
		case <-closed:
		default:
			t.Fatal("Close did not return after the connection's goroutine ended")
		}
		requireClosed(t, srv, serveResult)
	})
}

// L2. Close from several goroutines: each returns nil after the server has
// stopped.
func TestCloseFromSeveralGoroutines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv, ln, serveResult := serving(t, Config{})
		client := openPipeCOTP(t, ln)
		synctest.Wait() // the connection's goroutine waits for a request

		const callers = 4
		errs := make(chan error, callers)
		for range callers {
			go func() { errs <- srv.Close() }()
		}
		synctest.Wait()
		for range callers {
			select {
			case err := <-errs:
				if err != nil {
					t.Errorf("Close: %v", err)
				}
			default:
				t.Fatal("a Close call has not returned")
			}
		}
		requireClosed(t, srv, serveResult)
		if _, err := client.ReadTSDU(context.Background()); err == nil {
			t.Error("the connection still delivers after Close")
		}
	})
}

// openPipeCOTP connects a COTP client to the server over a pipe.
func openPipeCOTP(t *testing.T, ln *pipeListener) *cotp.Conn {
	t.Helper()
	conn, err := cotp.Connect(context.Background(), ln.dial(t), cotp.ClientConfig{
		LocalSelector: []byte{1, 0}, RemoteSelector: []byte{1, 2}, MaxTPDULength: 1024,
	})
	if err != nil {
		t.Fatalf("cotp.Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// L3. Close while a connection's goroutine waits for a request: the read
// ends, the goroutine has gone when Close returns, and the client's side
// sees the connection closed.
func TestCloseDuringABlockedRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv, ln, serveResult := serving(t, Config{})
		client := openPipeCOTP(t, ln)
		synctest.Wait() // the connection's goroutine is in ReadTSDU

		if err := srv.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		requireClosed(t, srv, serveResult)
		if _, err := client.ReadTSDU(context.Background()); err == nil {
			t.Error("the connection still delivers after Close")
		}
	})
}

// L4. Close while a connection's goroutine writes an answer the client does
// not take: the write ends and the goroutine has gone when Close returns.
func TestCloseDuringAWriteTheClientDoesNotTake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv, ln, serveResult := serving(t, Config{})
		client := openPipeCOTP(t, ln)
		setup, err := wire.EncodeSetupCommRequest(1, 1, 1, 480)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.WriteTSDU(context.Background(), setup); err != nil {
			t.Fatalf("WriteTSDU: %v", err)
		}
		synctest.Wait() // the answer is being written; nobody reads the pipe

		if err := srv.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		requireClosed(t, srv, serveResult)
	})
}

// L5. Close while a connection is still in its COTP handshake: the handshake
// ends without waiting for HandshakeTimeout, and the goroutine has gone when
// Close returns.
func TestCloseDuringTheHandshake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv, ln, serveResult := serving(t, Config{})
		raw := ln.dial(t) // sends no connect request
		synctest.Wait()   // the connection's goroutine waits for it

		if err := srv.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		requireClosed(t, srv, serveResult)
		if _, err := raw.Read(make([]byte, 1)); err == nil {
			t.Error("the connection is still open after Close")
		}
	})
}

// Serve on a closed server returns ErrServerClosed, closes the listener and
// starts nothing; Close on it returns nil again.
func TestServeAfterClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv, _, serveResult := serving(t, Config{})
		synctest.Wait()
		if err := srv.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		requireClosed(t, srv, serveResult)
		ln := newPipeListener()
		if err := srv.Serve(context.Background(), ln); !errors.Is(err, ErrServerClosed) {
			t.Errorf("Serve after Close = %v, want ErrServerClosed", err)
		}
		if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Errorf("listener after a refused Serve: %v, want it closed", err)
		}
		if err := srv.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
	})
}
