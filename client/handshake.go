package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/transport"
	"github.com/otfabric/go-s7comm/wire"
)

// dialTransport dials address and wraps the connection in a transport.Conn.
func dialTransport(ctx context.Context, address string, timeout time.Duration) (*transport.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("TCP connect: %w", err)
	}
	return transport.New(conn, timeout), nil
}

// performCOTPConnect runs COTP CR/CC on conn with the given TSAPs.
func performCOTPConnect(ctx context.Context, conn *transport.Conn, localTSAP, remoteTSAP uint16) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	crBytes, err := wire.EncodeCOTPCR(localTSAP, remoteTSAP)
	if err != nil {
		return fmt.Errorf("encode COTP CR: %w", errors.Join(err, ErrProtocolFailure))
	}
	if err := conn.SendContext(ctx, crBytes); err != nil {
		return err
	}
	resp, err := conn.ReceiveContext(ctx)
	if err != nil {
		return err
	}
	dec, err := cotp.Decode(resp)
	if err != nil {
		return fmt.Errorf("decode COTP: %w", errors.Join(err, ErrProtocolFailure))
	}
	if dec.Type != cotp.TypeCC {
		return fmt.Errorf("expected COTP CC, got %s: %w", dec.Type, ErrProtocolFailure)
	}
	return nil
}

// performS7Setup runs S7 Setup Communication on conn and returns the response.
// pduRef is the PDU reference to use in the setup request header.
func performS7Setup(ctx context.Context, conn *transport.Conn, pduRef uint16, maxAmqCalling, maxAmqCalled, maxPDU int) (*wire.SetupCommResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req := wire.EncodeSetupCommRequest(pduRef, maxAmqCalling, maxAmqCalled, maxPDU)
	dtBytes, err := wire.EncodeCOTPDT(req)
	if err != nil {
		return nil, fmt.Errorf("encode COTP DT: %w", errors.Join(err, ErrProtocolFailure))
	}
	if err := conn.SendContext(ctx, dtBytes); err != nil {
		return nil, err
	}
	resp, err := conn.ReceiveContext(ctx)
	if err != nil {
		return nil, err
	}
	dec, err := cotp.Decode(resp)
	if err != nil {
		return nil, fmt.Errorf("decode COTP: %w", errors.Join(err, ErrProtocolFailure))
	}
	if dec.DT == nil {
		return nil, fmt.Errorf("expected COTP DT, got %s: %w", dec.Type, ErrProtocolFailure)
	}
	s7Data := dec.DT.UserData
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
