package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/otfabric/go-s7comm/transport"
	"github.com/otfabric/go-s7comm/wire"
)

// dialCOTP dials TCP and completes the TP0 handshake for the given TSAPs.
func dialCOTP(ctx context.Context, address string, timeout time.Duration, localTSAP, remoteTSAP uint16) (*transport.Conn, error) {
	return transport.Dial(ctx, address, transport.Config{
		LocalTSAP:     localTSAP,
		RemoteTSAP:    remoteTSAP,
		Timeout:       timeout,
		MaxTPDULength: transport.DefaultMaxTPDULength,
	})
}

// performS7Setup runs S7 Setup Communication on an open TP0 connection.
// pduRef is the PDU reference to use in the setup request header.
func performS7Setup(ctx context.Context, conn *transport.Conn, pduRef uint16, maxAmqCalling, maxAmqCalled, maxPDU int) (*wire.SetupCommResponse, error) {
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
