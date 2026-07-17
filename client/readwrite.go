package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// classifyOpError maps operation failures to ReadStatus for consistent read-path semantics.
func classifyOpError(err error) ReadStatus {
	if err == nil {
		return ReadStatusSuccess
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ReadStatusTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ReadStatusTimeout
	}
	if errors.Is(err, ErrNotConnected) {
		return ReadStatusTransportErr
	}
	var s7Err *wire.S7Error
	if errors.As(err, &s7Err) {
		return ReadStatusRejected
	}
	if errors.Is(err, wire.ErrShortS7Header) || errors.Is(err, wire.ErrInvalidS7ProtocolID) ||
		errors.Is(err, wire.ErrShortS7AckHeader) || errors.Is(err, wire.ErrS7PayloadLength) {
		return ReadStatusProtocolErr
	}
	if errors.Is(err, ErrProtocolFailure) {
		return ReadStatusProtocolErr
	}
	if errors.Is(err, ErrRequestExceedsPDU) {
		return ReadStatusProtocolErr
	}
	return ReadStatusTransportErr
}

// newFailedReadResult builds a ReadResult for a failed read using classified status.
// Cause is set so ReadOutcomeError.Unwrap() and errors.Is/As work on the underlying error.
func newFailedReadResult(requested int, err error) *ReadResult {
	status := classifyOpError(err)
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	return &ReadResult{
		Status:          status,
		RequestedLength: requested,
		ReturnedLength:  0,
		Data:            nil,
		Message:         errMsg,
		Cause:           err,
	}
}

// ReadArea reads data from an S7 memory area and returns a structured result with status and lengths.
// Use result.OK() for success; result.Err() for a non-success outcome; result.Data for the payload.
// Invalid address (negative start/size, negative DB number for DB area) returns an error.
func (c *Client) ReadArea(ctx context.Context, addr model.Address) (*ReadResult, error) {
	if err := validateAddress(addr); err != nil {
		return nil, err
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	reqLen := addr.Size
	if reqLen < 0 {
		reqLen = 0
	}
	out := &ReadResult{
		RequestedLength: reqLen,
		ReturnedLength:  0,
		Data:            nil,
	}

	c.mu.RLock()
	pduSize := c.pduSize
	rateLimit := c.opts.rateLimit
	c.mu.RUnlock()

	maxData := pduSize - wire.ReadVarRequestOverhead
	if maxData < 1 {
		maxData = 200
	}

	result := make([]byte, 0, addr.Size)
	offset := addr.Start
	chunks := planReadChunks(addr.Size, maxData)
	firstChunk := true

	for _, chunkSize := range chunks {
		if err := ctx.Err(); err != nil {
			return newFailedReadResult(reqLen, err), nil
		}
		if rateLimit > 0 && !firstChunk {
			select {
			case <-ctx.Done():
				return newFailedReadResult(reqLen, ctx.Err()), nil
			case <-time.After(rateLimit):
			}
		}
		firstChunk = false

		s7Addr := wire.S7AnyAddress{
			Area:     byte(addr.Area),
			DBNumber: addr.DBNumber,
			Start:    offset,
			Size:     chunkSize,
		}

		ref := c.nextPDURef()
		req := wire.EncodeReadVarRequest(ref, []wire.S7AnyAddress{s7Addr})
		param, data, err := c.sendReceive(ctx, req, ref)
		if err != nil {
			return newFailedReadResult(reqLen, err), nil
		}

		items, err := wire.ParseReadVarResponse(param, data)
		if err != nil {
			out.Status = ReadStatusProtocolErr
			out.Message = err.Error()
			out.ReturnedLength = len(result)
			out.Data = result
			return out, nil
		}

		if len(items) == 0 {
			out.Status = ReadStatusEmptyRead
			out.Message = "no data returned"
			out.ReturnedLength = len(result)
			out.Data = result
			return out, nil
		}

		item := items[0]
		if err := wire.ReturnCodeError(item.ReturnCode); err != nil {
			out.Status = ReadStatusRejected
			out.ReturnCode = item.ReturnCode
			out.Message = err.Error()
			out.ItemStatus = wire.ItemReturnCodeString(item.ReturnCode)
			out.ReturnedLength = len(result)
			out.Data = result
			return out, nil
		}

		result = append(result, item.Data...)
		offset += chunkSize
	}

	out.ReturnedLength = len(result)
	out.Data = result
	out.Status = ClassifyReadOutcome(reqLen, out.ReturnedLength)
	if out.Status == ReadStatusEmptyRead && out.Message == "" {
		out.Message = "target returned no data for requested range"
	}
	if out.Status == ReadStatusShortRead && out.Message == "" {
		out.Message = fmt.Sprintf("short read: requested %d, got %d", reqLen, out.ReturnedLength)
	}
	return out, nil
}

// ClassifyReadOutcome returns the ReadStatus for a read that requested requested bytes and returned returned bytes.
// Intentional: requested <= 0 is treated as success (no bytes requested, so no short/empty semantics).
func ClassifyReadOutcome(requested, returned int) ReadStatus {
	if requested <= 0 {
		return ReadStatusSuccess
	}
	if returned == 0 {
		return ReadStatusEmptyRead
	}
	if returned < requested {
		return ReadStatusShortRead
	}
	return ReadStatusSuccess
}

// WriteArea writes data to an S7 memory area. The number of bytes written is len(data); addr.Size is ignored.
// Large payloads are chunked to fit the negotiated PDU size. Invalid address (negative start, negative DB number for DB area) returns an error.
func (c *Client) WriteArea(ctx context.Context, addr model.Address, data []byte) error {
	if err := validateAddress(addr); err != nil {
		return err
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.RLock()
	pduSize := c.pduSize
	rateLimit := c.opts.rateLimit
	c.mu.RUnlock()

	maxPayload := pduSize - wire.WriteVarRequestOverhead
	if maxPayload < 1 {
		maxPayload = 200
	}
	chunks := planReadChunks(len(data), maxPayload)
	offset := 0
	firstChunk := true
	for _, chunkSize := range chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rateLimit > 0 && !firstChunk {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(rateLimit):
			}
		}
		firstChunk = false
		chunk := data[offset : offset+chunkSize]
		s7Addr := wire.S7AnyAddress{
			Area:     byte(addr.Area),
			DBNumber: addr.DBNumber,
			Start:    addr.Start + offset,
			Size:     len(chunk),
		}
		ref := c.nextPDURef()
		req := wire.EncodeWriteVarRequest(ref, s7Addr, chunk)
		param, respData, err := c.sendReceive(ctx, req, ref)
		if err != nil {
			return err
		}
		if err := wire.ParseWriteVarResponse(param, respData); err != nil {
			return err
		}
		offset += chunkSize
	}
	return nil
}

// ReadDB reads from a data block. Returns *ReadResult; use result.OK() and result.Data.
func (c *Client) ReadDB(ctx context.Context, dbNum, offset, size int) (*ReadResult, error) {
	return c.ReadArea(ctx, model.Address{
		Area:     model.AreaDB,
		DBNumber: dbNum,
		Start:    offset,
		Size:     size,
	})
}

// WriteDB is a convenience function to write to a data block
func (c *Client) WriteDB(ctx context.Context, dbNum, offset int, data []byte) error {
	return c.WriteArea(ctx, model.Address{
		Area:     model.AreaDB,
		DBNumber: dbNum,
		Start:    offset,
		Size:     len(data),
	}, data)
}

// ReadInputs reads from the input area. Returns *ReadResult; use result.OK() and result.Data.
func (c *Client) ReadInputs(ctx context.Context, offset, size int) (*ReadResult, error) {
	return c.ReadArea(ctx, model.Address{
		Area:  model.AreaInputs,
		Start: offset,
		Size:  size,
	})
}

// ReadOutputs reads from the output area. Returns *ReadResult; use result.OK() and result.Data.
func (c *Client) ReadOutputs(ctx context.Context, offset, size int) (*ReadResult, error) {
	return c.ReadArea(ctx, model.Address{
		Area:  model.AreaOutputs,
		Start: offset,
		Size:  size,
	})
}

// ReadMerkers reads from the merker area. Returns *ReadResult; use result.OK() and result.Data.
func (c *Client) ReadMerkers(ctx context.Context, offset, size int) (*ReadResult, error) {
	return c.ReadArea(ctx, model.Address{
		Area:  model.AreaMerkers,
		Start: offset,
		Size:  size,
	})
}

func planReadChunks(total, maxChunk int) []int {
	if total <= 0 {
		return nil
	}
	if maxChunk <= 0 {
		maxChunk = total
	}

	out := make([]int, 0, (total/maxChunk)+1)
	remaining := total
	for remaining > 0 {
		n := remaining
		if n > maxChunk {
			n = maxChunk
		}
		out = append(out, n)
		remaining -= n
	}
	return out
}
