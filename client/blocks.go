// SPDX-License-Identifier: MIT

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

// ListBlocks returns the blocks of one type (block functions, list
// blocks of type), all packets of the list.
func (c *Client) ListBlocks(ctx context.Context, bt model.BlockType) ([]model.BlockInfo, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	ref := c.nextPDURef()
	_, payload, err := c.userdata(ctx, wire.EncodeBlockListRequest(ref, byte(bt)), ref, wire.UserdataGroupBlock, wire.UserdataBlockList)
	if err != nil {
		return nil, err
	}
	entries, err := wire.ParseBlockListResponse(payload)
	if err != nil {
		return nil, errors.Join(err, ErrProtocolFailure)
	}
	blocks := make([]model.BlockInfo, 0, len(entries))
	for _, e := range entries {
		blocks = append(blocks, model.BlockInfo{
			Type:     bt,
			Number:   int(e.BlockNumber),
			Language: model.BlockLang(e.Language),
			Flags:    e.Flags,
		})
	}
	return blocks, nil
}

// BlockCounts returns how many blocks of each type the CPU holds (block
// functions, list blocks), keyed by block type.
func (c *Client) BlockCounts(ctx context.Context) (map[model.BlockType]int, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	ref := c.nextPDURef()
	_, payload, err := c.userdata(ctx, wire.EncodeBlockCountRequest(ref), ref, wire.UserdataGroupBlock, wire.UserdataBlockCount)
	if err != nil {
		return nil, err
	}
	counts, err := wire.ParseBlockCountResponse(payload)
	if err != nil {
		return nil, errors.Join(err, ErrProtocolFailure)
	}
	out := make(map[model.BlockType]int, len(counts))
	for _, bc := range counts {
		out[model.BlockType(bc.BlockType)] = bc.Count
	}
	return out, nil
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

// GetBlockInfo returns the header of one block (block functions, get
// block info): language, flags, lengths, author, family, name, version and
// checksum. Block numbers must be in 0..99999 (the five-digit block
// address); an invalid number or block type returns ValidationError without
// sending a request. When the answer cannot be decoded it returns a
// BlockInfo with only Type and Number set, and the error.
func (c *Client) GetBlockInfo(ctx context.Context, bt model.BlockType, num int) (*model.BlockInfo, error) {
	if num < 0 || num > wire.MaxUploadBlockNumber {
		return nil, &ValidationError{Message: fmt.Sprintf("block number %d out of range (0..%d)", num, wire.MaxUploadBlockNumber)}
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	ref := c.nextPDURef()
	req, err := wire.EncodeBlockInfoRequest(ref, byte(bt), num)
	if err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}
	_, payload, err := c.userdata(ctx, req, ref, wire.UserdataGroupBlock, wire.UserdataBlockInfo)
	if err != nil {
		return nil, err
	}
	bi, err := wire.ParseBlockInfoResponse(payload)
	if err != nil {
		return &model.BlockInfo{Type: bt, Number: num}, errors.Join(err, ErrProtocolFailure)
	}
	return &model.BlockInfo{
		Type:       bt,
		Number:     bi.Number,
		Language:   model.BlockLang(bi.Language),
		Flags:      bi.Flags,
		LoadMemory: bi.LoadMemory,
		LocalData:  bi.LocalData,
		MC7Size:    bi.MC7Size,
		SBBLength:  bi.SSBLength,
		Author:     bi.Author,
		Family:     bi.Family,
		Name:       bi.Name,
		Version:    bi.Version,
		CheckSum:   bi.Checksum,
	}, nil
}

// UploadBlock uploads a block from the PLC. Block numbers must be in 0..65535.
// An unknown block type returns ValidationError without sending a request.
// It performs a best-effort end-upload cleanup before returning; this may add a short delay on completion.
// Cleanup errors are not returned to the caller; cleanup uses a 500ms timeout to limit that delay.
func (c *Client) UploadBlock(ctx context.Context, bt model.BlockType, num int) (*model.BlockData, error) {
	if num < 0 || num > 0xFFFF {
		return nil, &ValidationError{Message: fmt.Sprintf("block number %d out of range (0..65535)", num)}
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	ref := c.nextPDURef()
	req, err := wire.EncodeStartUploadRequest(ref, byte(bt), num)
	if err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}
	param, _, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return nil, err
	}

	start, err := wire.ParseStartUploadResponse(param)
	if err != nil {
		return nil, fmt.Errorf("parse start upload: %w", errors.Join(err, ErrProtocolFailure))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		endRef := c.nextPDURef()
		_, _, err := c.sendReceive(cleanupCtx, wire.EncodeEndUploadRequest(endRef, start.UploadID), endRef)
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
	for i := range maxUploadChunks {
		var chunk *wire.UploadChunk
		var lastErr error
		for range maxUploadRetries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			chunkRef := c.nextPDURef()
			req = wire.EncodeUploadRequest(chunkRef, start.UploadID)
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
