package client

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

var (
	orderRegex    = regexp.MustCompile(`(?i)6ES7[0-9A-Z\-\/]{4,}`)
	firmwareRegex = regexp.MustCompile(`(?i)V\s*\d+(?:\.\d+){1,3}`)
	serialRegex   = regexp.MustCompile(`(?i)[A-Z0-9]{8,24}`)
)

// readModuleIDSZL performs the SZL 0x0011 (Module ID) read and returns the raw SZL data payload.
// Transport-only; no heuristic parsing. Used by Identify.
func (c *Client) readModuleIDSZL(ctx context.Context) ([]byte, error) {
	ref := c.nextPDURef()
	req := wire.EncodeSZLRequest(ref, wire.SZLModuleID, 0)
	_, data, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	resp, err := wire.ParseSZLResponse(data)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// readComponentIDSZL performs the SZL 0x001C (Component ID) read and returns the raw SZL data payload.
// Transport-only; no heuristic parsing. Used by Identify.
func (c *Client) readComponentIDSZL(ctx context.Context) ([]byte, error) {
	ref := c.nextPDURef()
	req := wire.EncodeSZLRequest(ref, wire.SZLComponentID, 0)
	_, data, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	resp, err := wire.ParseSZLResponse(data)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// Identify is best-effort and may return partial device info with a non-nil error.
// It reads device identification from SZL (Module ID and Component ID) and interprets
// fields heuristically. Return contract: info != nil with partial data and err != nil is
// expected when one SZL succeeds and the other fails; (nil, error) only when both reads fail.
// Callers should treat (info, err) as "partial info plus errors" and (nil, err) as "no info".
// Unknown fields remain empty; parsing is heuristic.
func (c *Client) Identify(ctx context.Context) (*model.DeviceInfo, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	info := &model.DeviceInfo{}
	var errs []error

	rawModule, errMod := c.readModuleIDSZL(ctx)
	if errMod != nil {
		errs = append(errs, errMod)
	} else if len(rawModule) >= 20 {
		info.OrderNumber = trimString(rawModule[2:22])
		populateInfoFromRaw(info, rawModule)
	} else if len(rawModule) > 0 {
		errs = append(errs, fmt.Errorf("module ID SZL payload too short: got %d", len(rawModule)))
	}

	rawComponent, errComp := c.readComponentIDSZL(ctx)
	if errComp != nil {
		errs = append(errs, errComp)
	} else if len(rawComponent) >= 34 {
		info.ModuleName = trimString(rawComponent[2:26])
		info.PlantID = trimString(rawComponent[26:34])
		populateInfoFromRaw(info, rawComponent)
	} else if len(rawComponent) > 0 {
		errs = append(errs, fmt.Errorf("component ID SZL payload too short: got %d", len(rawComponent)))
	}

	if len(errs) == 2 {
		return nil, errors.Join(errs...)
	}
	if len(errs) == 1 {
		return info, errs[0]
	}
	return info, nil
}

// GetCPUState returns the current CPU state
func (c *Client) GetCPUState(ctx context.Context) (model.CPUState, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	ref := c.nextPDURef()
	req := wire.EncodeSZLRequest(ref, wire.SZLCPUState, 0)
	_, data, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return model.CPUStateUnknown, err
	}

	resp, err := wire.ParseSZLResponse(data)
	if err != nil {
		return model.CPUStateUnknown, err
	}

	if len(resp.Data) >= 4 {
		state := resp.Data[2]
		switch state {
		case 0x08:
			return model.CPUStateRun, nil
		case 0x04:
			return model.CPUStateStop, nil
		case 0x01:
			return model.CPUStateStartup, nil
		}
	}

	return model.CPUStateUnknown, nil
}

// GetProtectionLevel returns the PLC protection level
func (c *Client) GetProtectionLevel(ctx context.Context) (model.ProtectionLevel, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	ref := c.nextPDURef()
	req := wire.EncodeSZLRequest(ref, wire.SZLProtectionInfo, 0)
	_, data, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return model.ProtectionNone, err
	}

	resp, err := wire.ParseSZLResponse(data)
	if err != nil {
		return model.ProtectionNone, err
	}

	if len(resp.Data) >= 4 {
		level := resp.Data[2]
		return model.ProtectionLevel(level & 0x03), nil
	}

	return model.ProtectionNone, nil
}

// ReadDiagBufferRaw returns the raw SZL payload for the diagnostic buffer (SZL 0x00A0).
// Callers can parse the layout themselves; device layout and entry stride are variant-dependent.
func (c *Client) ReadDiagBufferRaw(ctx context.Context) ([]byte, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	ref := c.nextPDURef()
	req := wire.EncodeSZLRequest(ref, wire.SZLDiagBuffer, 0)
	_, data, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return nil, err
	}

	resp, err := wire.ParseSZLResponse(data)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ReadDiagBuffer is best-effort and may return partial entries with a non-nil error.
// It performs partial diagnostic parsing of the SZL diagnostic buffer.
// It uses a fixed 20-byte entry stride from offset 0 and reads only EventID, EventClass,
// and Priority per entry. Layout is device-dependent; for full control use ReadDiagBufferRaw.
func (c *Client) ReadDiagBuffer(ctx context.Context) (*model.DiagBuffer, error) {
	raw, err := c.ReadDiagBufferRaw(ctx)
	if err != nil {
		return nil, err
	}

	buf := &model.DiagBuffer{}
	const diagEntrySize = 20
	offset := 0
	for offset+diagEntrySize <= len(raw) {
		entry := model.DiagEntry{
			EventID:    uint16(raw[offset])<<8 | uint16(raw[offset+1]),
			EventClass: raw[offset+2],
			Priority:   raw[offset+3],
		}
		buf.Entries = append(buf.Entries, entry)
		offset += diagEntrySize
	}
	buf.TotalCount = len(buf.Entries)
	return buf, nil
}

func trimString(data []byte) string {
	end := len(data)
	for end > 0 && (data[end-1] == 0 || data[end-1] == ' ') {
		end--
	}
	return string(data[:end])
}

func populateInfoFromRaw(info *model.DeviceInfo, raw []byte) {
	tokens := extractPrintableTokens(raw)
	joined := strings.Join(tokens, " ")

	if info.OrderNumber == "" {
		if m := orderRegex.FindString(joined); m != "" {
			info.OrderNumber = normalizeToken(m)
		}
	}
	if info.FWVersion == "" {
		if m := firmwareRegex.FindString(joined); m != "" {
			info.FWVersion = normalizeToken(m)
		}
	}

	for _, tok := range tokens {
		tok = normalizeToken(tok)
		u := strings.ToUpper(tok)
		if info.OrderNumber == "" && strings.Contains(u, "6ES7") {
			info.OrderNumber = tok
		}
		if info.CPUType == "" && (strings.Contains(u, "CPU") || strings.Contains(u, "S7-")) {
			info.CPUType = tok
		}
		if info.FWVersion == "" && strings.Contains(u, "V") && strings.IndexFunc(u, unicode.IsDigit) >= 0 {
			info.FWVersion = tok
		}
		if info.SerialNumber == "" && isLikelySerial(u) {
			info.SerialNumber = tok
		}
	}

	if info.SerialNumber == "" {
		for _, m := range serialRegex.FindAllString(joined, -1) {
			u := strings.ToUpper(m)
			if isLikelySerial(u) && !strings.Contains(u, "6ES7") {
				info.SerialNumber = normalizeToken(m)
				break
			}
		}
	}
}

func extractPrintableTokens(raw []byte) []string {
	parts := strings.FieldsFunc(string(raw), func(r rune) bool {
		return r == 0 || !unicode.IsPrint(r)
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = normalizeToken(strings.TrimSpace(p))
		if len(p) >= 4 {
			out = append(out, p)
		}
	}
	return out
}

func normalizeToken(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'[](){};,")
	s = strings.ReplaceAll(s, "  ", " ")
	return s
}

func isLikelySerial(s string) bool {
	if len(s) < 8 || len(s) > 24 {
		return false
	}
	hasLetter := false
	hasDigit := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}
