// Package transport provides TCP+TP0 connection handling for S7 via go-cotp.
//
// Dial dials TCP and completes the Class 0 COTP handshake. The returned Conn
// exposes TSDU Read/Write; TPKT framing and COTP DT segmentation are owned by
// go-cotp. Callers must not use go-tpkt on the same stream.
package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/otfabric/go-cotp"
)

// ErrConnectionNotEstablished is returned when an operation is attempted on a nil or closed Conn.
var ErrConnectionNotEstablished = errors.New("connection not established")

// DefaultMaxTPDULength is the S7comm-style Class 0 TPDU size (wire 0xC0 = 0x0A).
const DefaultMaxTPDULength = 1024

// Config configures Dial (TCP + COTP Connect).
type Config struct {
	LocalTSAP     uint16
	RemoteTSAP    uint16
	Timeout       time.Duration // dial + handshake bound when > 0
	MaxTPDULength int           // 0 → DefaultMaxTPDULength
}

// Conn is an established TP0 connection carrying S7 PDUs as TSDUs.
type Conn struct {
	cotp *cotp.Conn
}

// DialTCP dials a TCP connection. The caller owns raw until Connect is called.
func DialTCP(ctx context.Context, address string, timeout time.Duration) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dialer := net.Dialer{Timeout: timeout}
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("TCP connect: %w", err)
	}
	return raw, nil
}

// Connect completes the TP0 handshake on an owned raw connection.
// On failure go-cotp closes raw.
func Connect(ctx context.Context, raw net.Conn, cfg Config) (*Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	maxTPDU := cfg.MaxTPDULength
	if maxTPDU == 0 {
		maxTPDU = DefaultMaxTPDULength
	}
	connectCtx := ctx
	var cancel context.CancelFunc
	if cfg.Timeout > 0 {
		connectCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	c, err := cotp.Connect(connectCtx, raw, cotp.ClientConfig{
		LocalSelector:  tsapSelector(cfg.LocalTSAP),
		RemoteSelector: tsapSelector(cfg.RemoteTSAP),
		MaxTPDULength:  maxTPDU,
	})
	if err != nil {
		return nil, err
	}
	return &Conn{cotp: c}, nil
}

// Dial dials TCP at address and runs Connect with the given TSAP selectors.
// On any Connect failure the TCP connection is already closed by go-cotp.
func Dial(ctx context.Context, address string, cfg Config) (*Conn, error) {
	raw, err := DialTCP(ctx, address, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	return Connect(ctx, raw, cfg)
}

// FromCOTP wraps an already-open *cotp.Conn (e.g. test Accept path).
func FromCOTP(c *cotp.Conn) *Conn {
	return &Conn{cotp: c}
}

func tsapSelector(tsap uint16) []byte {
	return binary.BigEndian.AppendUint16(nil, tsap)
}

// WriteTSDU writes one complete S7 PDU as a COTP TSDU.
func (c *Conn) WriteTSDU(ctx context.Context, s7PDU []byte) error {
	if c == nil || c.cotp == nil {
		return ErrConnectionNotEstablished
	}
	return c.cotp.WriteTSDU(ctx, s7PDU)
}

// ReadTSDU reads one complete S7 PDU (reassembled TSDU).
func (c *Conn) ReadTSDU(ctx context.Context) ([]byte, error) {
	if c == nil || c.cotp == nil {
		return nil, ErrConnectionNotEstablished
	}
	return c.cotp.ReadTSDU(ctx)
}

// Close closes the TP0 connection.
func (c *Conn) Close() error {
	if c == nil || c.cotp == nil {
		return nil
	}
	err := c.cotp.Close()
	c.cotp = nil
	// go-cotp reports successful local close as ErrClosed; surface as nil to callers.
	if errors.Is(err, cotp.ErrClosed) {
		return nil
	}
	return err
}

// LocalAddr returns the local network address.
func (c *Conn) LocalAddr() net.Addr {
	if c == nil || c.cotp == nil {
		return nil
	}
	return c.cotp.LocalAddr()
}

// RemoteAddr returns the remote network address.
func (c *Conn) RemoteAddr() net.Addr {
	if c == nil || c.cotp == nil {
		return nil
	}
	return c.cotp.RemoteAddr()
}

// Negotiated returns COTP parameters after a successful handshake.
func (c *Conn) Negotiated() cotp.NegotiatedParameters {
	if c == nil || c.cotp == nil {
		return cotp.NegotiatedParameters{}
	}
	return c.cotp.Negotiated()
}
