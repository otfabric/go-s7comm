package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

const (
	maxUploadChunks  = 4096
	maxUploadRetries = 3
)

// isUploadProtocolError reports whether the error is a protocol/parse failure (do not retry).
func isUploadProtocolError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrProtocolFailure) {
		return true
	}
	var s7Err *wire.S7Error
	return errors.As(err, &s7Err)
}

// ListBlocks returns a list of blocks of the specified type
func (c *Client) ListBlocks(ctx context.Context, bt model.BlockType) ([]model.BlockInfo, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	ref := c.nextPDURef()
	req := wire.EncodeBlockListRequest(ref, byte(bt))
	_, data, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return nil, err
	}

	resp, err := wire.ParseSZLResponse(data)
	if err != nil {
		return nil, err
	}

	entries, err := wire.ParseBlockListResponse(resp.Data)
	if err != nil {
		return nil, err
	}

	var blocks []model.BlockInfo
	for _, e := range entries {
		blocks = append(blocks, model.BlockInfo{
			Type:     model.BlockType(e.BlockType),
			Number:   int(e.BlockNumber),
			Language: model.BlockLang(e.Language),
			Flags:    e.Flags,
		})
	}

	return blocks, nil
}

// ListAllBlocks is best-effort and may return partial results with aggregated errors.
// It returns all blocks in the PLC. If any block type enumeration fails,
// it returns the partial list plus an aggregated error (e.g. errors.Join), so callers
// can distinguish full success from partial enumeration.
func (c *Client) ListAllBlocks(ctx context.Context) ([]model.BlockInfo, error) {
	var all []model.BlockInfo
	var errs []error
	types := []model.BlockType{
		model.BlockOB,
		model.BlockDB,
		model.BlockSDB,
		model.BlockFC,
		model.BlockFB,
		model.BlockSFC,
		model.BlockSFB,
	}

	for _, bt := range types {
		blocks, err := c.ListBlocks(ctx, bt)
		if err != nil {
			errs = append(errs, fmt.Errorf("list blocks %s: %w", bt, err))
			continue
		}
		all = append(all, blocks...)
	}

	if len(errs) > 0 {
		return all, errors.Join(errs...)
	}
	return all, nil
}

// GetBlockInfo is best-effort and may return partial info with a non-nil error.
// On transport/protocol failure before a valid response: returns nil, err.
// On parse failure after transport success: returns partial BlockInfo with Type and Number set, plus err.
// Only LoadMemory, LocalData, and MC7Size are parsed; layout is device-dependent and may be partial.
// Block numbers must be in 0..65535; invalid num returns ValidationError.
func (c *Client) GetBlockInfo(ctx context.Context, bt model.BlockType, num int) (*model.BlockInfo, error) {
	if num < 0 || num > 0xFFFF {
		return nil, &ValidationError{Message: fmt.Sprintf("block number %d out of range (0..65535)", num)}
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	szlIndex := uint16(bt)<<8 | uint16(num)
	ref := c.nextPDURef()
	req := wire.EncodeSZLRequest(ref, wire.SZLBlockInfo, szlIndex)
	_, data, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return nil, err
	}

	resp, err := wire.ParseSZLResponse(data)
	if err != nil {
		return nil, err
	}

	info := &model.BlockInfo{
		Type:   bt,
		Number: num,
	}
	blockInfo, parseErr := wire.ParseBlockInfoResponse(resp.Data)
	if parseErr != nil {
		return info, parseErr
	}
	info.LoadMemory = blockInfo.LoadMemory
	info.LocalData = blockInfo.LocalData
	info.MC7Size = blockInfo.MC7Size
	return info, nil
}

// UploadBlock uploads a block from the PLC. Block numbers must be in 0..65535.
// It performs a best-effort end-upload cleanup before returning; this may add a short delay on completion.
// Cleanup errors are not returned to the caller; cleanup uses a 500ms timeout to limit that delay.
func (c *Client) UploadBlock(ctx context.Context, bt model.BlockType, num int) (*model.BlockData, error) {
	if num < 0 || num > 0xFFFF {
		return nil, &ValidationError{Message: fmt.Sprintf("block number %d out of range (0..65535)", num)}
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	ref := c.nextPDURef()
	req := wire.EncodeStartUploadRequest(ref, byte(bt), num)
	param, _, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return nil, err
	}

	sessionID, err := wire.ParseStartUploadResponse(param)
	if err != nil {
		return nil, fmt.Errorf("parse start upload: %w", errors.Join(err, ErrProtocolFailure))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		endRef := c.nextPDURef()
		_, _, err := c.sendReceive(cleanupCtx, wire.EncodeEndUploadRequest(endRef, sessionID), endRef)
		if c.opts.logger != nil {
			if err != nil {
				c.opts.logger.Debug("upload cleanup: end-upload failed (best-effort): %v", err)
			} else {
				c.opts.logger.Debug("upload cleanup: end-upload sent")
			}
		}
	}()

	var payload bytes.Buffer
	chunkCount := 0
	done := false
	for i := 0; i < maxUploadChunks; i++ {
		var chunk *wire.UploadChunk
		var lastErr error
		for attempt := 0; attempt < maxUploadRetries; attempt++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			chunkRef := c.nextPDURef()
			req = wire.EncodeUploadRequest(chunkRef, sessionID)
			param, data, e := c.sendReceive(ctx, req, chunkRef)
			if e != nil {
				if isUploadProtocolError(e) {
					return nil, fmt.Errorf("upload chunk %d: %w", i, e)
				}
				lastErr = e
				continue
			}

			chunk, e = wire.ParseUploadResponse(param, data)
			if e != nil {
				e = fmt.Errorf("parse upload response: %w", errors.Join(e, ErrProtocolFailure))
				if isUploadProtocolError(e) {
					return nil, fmt.Errorf("upload chunk %d: %w", i, e)
				}
				lastErr = e
				continue
			}
			lastErr = nil
			break
		}

		if lastErr != nil {
			return nil, fmt.Errorf("upload chunk %d failed after %d retries (received %d bytes so far): %w", i, maxUploadRetries, payload.Len(), lastErr)
		}

		if len(chunk.Data) > 0 {
			_, _ = payload.Write(chunk.Data)
			chunkCount++
		}
		if chunk.Done {
			done = true
			break
		}
	}
	if !done {
		return nil, fmt.Errorf("upload did not complete within %d chunks", maxUploadChunks)
	}
	if payload.Len() == 0 && chunkCount == 0 {
		return nil, fmt.Errorf("upload completed with no payload")
	}

	return &model.BlockData{
		Info: model.BlockInfo{
			Type:   bt,
			Number: num,
		},
		Data: payload.Bytes(),
	}, nil
}
