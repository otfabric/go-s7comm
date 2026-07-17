package client

import (
	"context"
	"errors"
	"fmt"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// ReadBit reads a single S7 bit using native BIT transport (not a byte read).
func (c *Client) ReadBit(ctx context.Context, addr model.BitAddress) (bool, error) {
	if err := validateBitAddress(addr); err != nil {
		return false, err
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return false, ErrNotConnected
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	bitAddr := wire.S7AnyBitAddress{
		Area:       byte(addr.Area),
		DBNumber:   addr.DBNumber,
		ByteOffset: addr.ByteOffset,
		BitOffset:  addr.BitOffset,
	}
	ref := c.nextPDURef()
	req := wire.EncodeReadVarBitRequest(ref, bitAddr)
	param, data, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return false, err
	}
	items, err := wire.ParseReadVarResponse(param, data)
	if err != nil {
		return false, fmt.Errorf("parse bit read response: %w", errors.Join(err, ErrProtocolFailure))
	}
	if len(items) != 1 {
		return false, fmt.Errorf("bit read: expected 1 item, got %d: %w", len(items), ErrProtocolFailure)
	}
	item := items[0]
	if err := wire.ReturnCodeError(item.ReturnCode); err != nil {
		return false, err
	}
	v, err := wire.DecodeAsBit(item)
	if err != nil {
		return false, fmt.Errorf("decode bit: %w", errors.Join(err, ErrProtocolFailure))
	}
	return v, nil
}

// WriteBit writes a single S7 bit using native BIT transport (not byte read-modify-write).
func (c *Client) WriteBit(ctx context.Context, addr model.BitAddress, value bool) error {
	if err := validateBitAddress(addr); err != nil {
		return err
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return ErrNotConnected
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	bitAddr := wire.S7AnyBitAddress{
		Area:       byte(addr.Area),
		DBNumber:   addr.DBNumber,
		ByteOffset: addr.ByteOffset,
		BitOffset:  addr.BitOffset,
	}
	ref := c.nextPDURef()
	req := wire.EncodeWriteVarBitRequest(ref, bitAddr, value)
	param, respData, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return err
	}
	return wire.ParseWriteVarResponse(param, respData)
}

// ReadDBBit reads DBNumber.DBXByteOffset.BitOffset.
func (c *Client) ReadDBBit(ctx context.Context, dbNumber, byteOffset, bitOffset int) (bool, error) {
	return c.ReadBit(ctx, model.BitAddress{
		Area:       model.AreaDB,
		DBNumber:   dbNumber,
		ByteOffset: byteOffset,
		BitOffset:  bitOffset,
	})
}

// WriteDBBit writes DBNumber.DBXByteOffset.BitOffset using native BIT transport.
func (c *Client) WriteDBBit(ctx context.Context, dbNumber, byteOffset, bitOffset int, value bool) error {
	return c.WriteBit(ctx, model.BitAddress{
		Area:       model.AreaDB,
		DBNumber:   dbNumber,
		ByteOffset: byteOffset,
		BitOffset:  bitOffset,
	}, value)
}
