package client

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/wire"
)

// defaultMaxTPDULength is the S7comm-style Class 0 TPDU size offer (wire 0xC0 = 0x0A).
const defaultMaxTPDULength = 1024

func tsapSelector(tsap uint16) []byte {
	return binary.BigEndian.AppendUint16(nil, tsap)
}

func dialTCP(ctx context.Context, address string, timeout time.Duration) (net.Conn, error) {
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

// connectCOTP completes the TP0 handshake on an owned raw connection.
// On failure go-cotp closes raw.
func connectCOTP(ctx context.Context, raw net.Conn, timeout time.Duration, localTSAP, remoteTSAP uint16) (*cotp.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	connectCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		connectCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return cotp.Connect(connectCtx, raw, cotp.ClientConfig{
		LocalSelector:  tsapSelector(localTSAP),
		RemoteSelector: tsapSelector(remoteTSAP),
		MaxTPDULength:  defaultMaxTPDULength,
	})
}

// dialCOTP dials TCP and completes the TP0 handshake for the given TSAPs.
func dialCOTP(ctx context.Context, address string, timeout time.Duration, localTSAP, remoteTSAP uint16) (*cotp.Conn, error) {
	raw, err := dialTCP(ctx, address, timeout)
	if err != nil {
		return nil, err
	}
	return connectCOTP(ctx, raw, timeout, localTSAP, remoteTSAP)
}

// closeCOTP closes a TP0 connection and maps successful local close (cotp.ErrClosed) to nil.
func closeCOTP(conn *cotp.Conn) error {
	if conn == nil {
		return nil
	}
	err := conn.Close()
	if errors.Is(err, cotp.ErrClosed) {
		return nil
	}
	return err
}

// performS7Setup runs S7 Setup Communication on an open TP0 connection.
// pduRef is the PDU reference to use in the setup request header.
func performS7Setup(ctx context.Context, conn *cotp.Conn, pduRef uint16, maxAmqCalling, maxAmqCalled, maxPDU int) (*wire.SetupCommResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req := wire.EncodeSetupCommRequest(pduRef, maxAmqCalling, maxAmqCalled, maxPDU)
	if err := conn.WriteTSDU(ctx, req); err != nil {
		return nil, err
	}
	s7Data, err := conn.ReadTSDU(ctx)
	if err != nil {
		return nil, err
	}
	header, paramData, err := wire.ParseS7Header(s7Data)
	if err != nil {
		return nil, fmt.Errorf("parse S7 header: %w", errors.Join(err, ErrProtocolFailure))
	}
	if header.PDURef != pduRef {
		return nil, &PDURefMismatchError{Expected: pduRef, Got: header.PDURef}
	}
	if header.ErrorClass != 0 || header.ErrorCode != 0 {
		return nil, wire.NewS7ErrorWithParam(header.ErrorClass, header.ErrorCode, paramData)
	}
	if len(paramData) > 0 && paramData[0] != wire.FuncSetupComm {
		return nil, fmt.Errorf("S7 setup response: expected function 0x%02X, got 0x%02X: %w", wire.FuncSetupComm, paramData[0], ErrProtocolFailure)
	}
	setup, err := wire.ParseSetupCommResponse(paramData)
	if err != nil {
		return nil, fmt.Errorf("parse setup response: %w", errors.Join(err, ErrProtocolFailure))
	}
	return setup, nil
}
