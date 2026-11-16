// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// maxUserdataFollowUps bounds how many further packets one user data
// response may take (an SZL list or a block list), so a CPU that keeps
// answering "more data follows" cannot hold the client.
const maxUserdataFollowUps = 256

// userdataAnswer decodes one packet of a user data answer to a request of
// the given function group and subfunction: its parameter section and the
// payload of its data section. what names the packet in an error.
//
// The packet must be a response (type 8) of the group and subfunction that
// were asked for, as every recorded answer is (testdata/captures); anything
// else is an ErrProtocolFailure error. A refusal comes back as an S7Error
// (userdataPayload).
func userdataAnswer(what string, param, data []byte, group, subfunc byte) (*wire.UserdataParam, []byte, error) {
	up, err := wire.ParseUserdataParam(param)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", what, errors.Join(err, ErrProtocolFailure))
	}
	if param[5]&0xf0 != wire.UserdataTypeResponse || up.Group != group || up.Subfunction != subfunc {
		return nil, nil, fmt.Errorf("%s: type 0x%X, group %d, subfunction 0x%02X answers a request of group %d, subfunction 0x%02X: %w",
			what, param[5]>>4, up.Group, up.Subfunction, group, subfunc, ErrProtocolFailure)
	}
	payload, err := userdataPayload(up, data)
	if err != nil {
		return nil, nil, err
	}
	return up, payload, nil
}

// userdataPayload returns the payload of a user data answer, or the error
// the answer reports. A CPU refuses a request with an item return code in
// the data section and often an error code in the parameter section as well
// (recorded: 0xD402 for an SZL list, 0xD209 for a block). The error keeps
// the return code in Code and names the parameter error in its message. An
// error code on an answer whose data section does not report a refusal is
// returned as an S7Error of that code: Class its high byte, Code its low.
func userdataPayload(up *wire.UserdataParam, data []byte) ([]byte, error) {
	payload, err := wire.ParseUserdataData(data)
	if up.ErrorCode == 0 {
		return payload, err
	}
	text := wire.ParamErrorCodeString(up.ErrorCode)
	if refusal, ok := errors.AsType[*wire.S7Error](err); ok && refusal.Code != 0 {
		return nil, &wire.S7Error{
			Class:   refusal.Class,
			Code:    refusal.Code,
			Message: fmt.Sprintf("%s (parameter error 0x%04X: %s)", refusal.Message, up.ErrorCode, text),
		}
	}
	return nil, &wire.S7Error{Class: byte(up.ErrorCode >> 8), Code: byte(up.ErrorCode), Message: text}
}

// userdata sends a user data request and collects the payload of its
// response, asking for each further packet while the CPU marks it "more
// data follows" (SZL lists and block lists longer than one PDU).
func (c *Client) userdata(ctx context.Context, req []byte, ref uint16, group, subfunc byte) (first []byte, payload []byte, err error) {
	param, data, err := c.sendReceive(ctx, req, ref)
	if err != nil {
		return nil, nil, err
	}
	up, payload, err := userdataAnswer("user data response", param, data, group, subfunc)
	if err != nil {
		return nil, nil, err
	}
	first = data
	payload = append([]byte(nil), payload...)
	for n := 0; up.More; n++ {
		if n == maxUserdataFollowUps {
			return nil, nil, fmt.Errorf("user data response longer than %d packets: %w", maxUserdataFollowUps, ErrProtocolFailure)
		}
		ref = c.nextPDURef()
		param, data, err = c.sendReceive(ctx, wire.EncodeUserdataFollowUp(ref, group, subfunc, up.Sequence), ref)
		if err != nil {
			return nil, nil, err
		}
		var more []byte
		if up, more, err = userdataAnswer("user data follow-up", param, data, group, subfunc); err != nil {
			return nil, nil, err
		}
		payload = append(payload, more...)
	}
	return first, payload, nil
}

// readSZL reads one SZL list, all packets of it. Data of the result is the
// list header (record length, record count) and every record. The answer
// must carry the SZL ID that was asked for. Its index is not compared: a
// recorded CPU 315-2 PN/DP answers SZL 0x0424 index 4 with index 0
// (testdata/captures, tia-v13_s7-300_go-online).
func (c *Client) readSZL(ctx context.Context, id, index uint16) (*wire.SZLResponse, error) {
	ref := c.nextPDURef()
	first, payload, err := c.userdata(ctx, wire.EncodeSZLRequest(ref, id, index), ref, wire.UserdataGroupCPU, wire.UserdataSZL)
	if err != nil {
		return nil, err
	}
	resp, err := wire.ParseSZLResponse(first)
	if err != nil {
		return nil, err
	}
	if resp.SZLID != id {
		return nil, fmt.Errorf("SZL answer is list 0x%04X, asked for 0x%04X: %w", resp.SZLID, id, ErrProtocolFailure)
	}
	resp.Data = payload[4:] // after SZL ID and index
	return resp, nil
}

// szlRecords splits SZL list data (record length, record count, records)
// into its records. A count past the data received is cut to what arrived.
func szlRecords(data []byte) ([][]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("SZL list header: %d bytes", len(data))
	}
	size := int(binary.BigEndian.Uint16(data[0:2]))
	count := int(binary.BigEndian.Uint16(data[2:4]))
	if size == 0 {
		return nil, fmt.Errorf("SZL record length 0")
	}
	recs := make([][]byte, 0, min(count, (len(data)-4)/size))
	for off := 4; len(recs) < count && off+size <= len(data); off += size {
		recs = append(recs, data[off:off+size])
	}
	return recs, nil
}

// SZL 0x0011 (module identification) records: index, order number (20),
// module type, version fields. SZL 0x001C (component identification)
// records: index, then a 32-byte text.
const (
	moduleIDRecordSize    = 28
	componentIDRecordSize = 34
)

// readModuleIDSZL performs the SZL 0x0011 (Module ID) read and returns the SZL list data.
func (c *Client) readModuleIDSZL(ctx context.Context) ([]byte, error) {
	resp, err := c.readSZL(ctx, wire.SZLModuleID, 0)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// readComponentIDSZL performs the SZL 0x001C (Component ID) read and returns the SZL list data.
func (c *Client) readComponentIDSZL(ctx context.Context) ([]byte, error) {
	resp, err := c.readSZL(ctx, wire.SZLComponentID, 0)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// applyModuleID fills info from SZL 0x0011 records: index 0x0001 is the
// module (order number), 0x0007 the firmware ('V' and three version bytes).
func applyModuleID(info *model.DeviceInfo, data []byte) error {
	recs, err := szlRecords(data)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if len(r) < moduleIDRecordSize {
			return fmt.Errorf("module ID record of %d bytes", len(r))
		}
		switch binary.BigEndian.Uint16(r[0:2]) {
		case 0x0001:
			info.OrderNumber = trimString(r[2:22])
		case 0x0007:
			if r[24] == 'V' {
				info.FWVersion = fmt.Sprintf("V%d.%d.%d", r[25], r[26], r[27])
			}
		}
	}
	return nil
}

// applyComponentID fills info from SZL 0x001C records by index.
func applyComponentID(info *model.DeviceInfo, data []byte) error {
	recs, err := szlRecords(data)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if len(r) < componentIDRecordSize {
			return fmt.Errorf("component ID record of %d bytes", len(r))
		}
		text := trimString(r[2:34])
		switch binary.BigEndian.Uint16(r[0:2]) {
		case 0x0001:
			info.SystemName = text
		case 0x0002:
			info.ModuleName = text
		case 0x0003:
			info.PlantID = text
		case 0x0004:
			info.Copyright = text
		case 0x0005:
			info.SerialNumber = text
		case 0x0007:
			info.ModuleType = text
			if strings.HasPrefix(strings.ToUpper(text), "CPU") {
				info.CPUType = text
			}
		case 0x0008:
			info.MemoryCardSerial = text
		}
	}
	return nil
}

// Identify reads device identification from SZL 0x0011 (module
// identification) and 0x001C (component identification), each record by
// its index. Return contract: info != nil with partial data and err != nil
// when one SZL succeeds and the other fails; (nil, error) only when both
// fail. Fields a CPU does not report stay empty.
func (c *Client) Identify(ctx context.Context) (*model.DeviceInfo, error) {
	if err := c.beginCall(ctx); err != nil {
		return nil, err
	}
	defer c.endCall()

	info := &model.DeviceInfo{}
	var errs []error
	if raw, err := c.readModuleIDSZL(ctx); err != nil {
		errs = append(errs, err)
	} else if err := applyModuleID(info, raw); err != nil {
		errs = append(errs, fmt.Errorf("module ID SZL: %w", err))
	}
	if raw, err := c.readComponentIDSZL(ctx); err != nil {
		errs = append(errs, err)
	} else if err := applyComponentID(info, raw); err != nil {
		errs = append(errs, fmt.Errorf("component ID SZL: %w", err))
	}
	switch len(errs) {
	case 2:
		return nil, errors.Join(errs...)
	case 1:
		return info, errs[0]
	}
	return info, nil
}

// GetCPUState returns the current CPU mode from SZL 0x0424 (modes): the
// low nibble of the first record's bzu-id byte, 1-4 STOP, 5-7 STARTUP,
// 8-9 RUN, 10 HOLD.
func (c *Client) GetCPUState(ctx context.Context) (model.CPUState, error) {
	if err := c.beginCall(ctx); err != nil {
		return model.CPUStateUnknown, err
	}
	defer c.endCall()

	resp, err := c.readSZL(ctx, wire.SZLCPUState, 0)
	if err != nil {
		return model.CPUStateUnknown, err
	}
	recs, err := szlRecords(resp.Data)
	if err != nil {
		return model.CPUStateUnknown, err
	}
	if len(recs) == 0 || len(recs[0]) < 4 {
		return model.CPUStateUnknown, fmt.Errorf("SZL 0x0424 holds no mode record")
	}
	switch mode := recs[0][3] & 0x0f; {
	case mode >= 1 && mode <= 4:
		return model.CPUStateStop, nil
	case mode >= 5 && mode <= 7:
		return model.CPUStateStartup, nil
	case mode == 8 || mode == 9:
		return model.CPUStateRun, nil
	case mode == 10:
		return model.CPUStateHold, nil
	}
	return model.CPUStateUnknown, nil
}

// szlProtectionIndex is the SZL 0x0232 index of the protection record.
const szlProtectionIndex = 0x0004

// GetProtectionLevel returns the protection level in force (sch_rel of SZL
// 0x0232 index 4): Siemens level 1 (no protection), 2 (write protection)
// or 3 (read and write protection).
func (c *Client) GetProtectionLevel(ctx context.Context) (model.ProtectionLevel, error) {
	if err := c.beginCall(ctx); err != nil {
		return model.ProtectionNone, err
	}
	defer c.endCall()

	resp, err := c.readSZL(ctx, wire.SZLProtectionInfo, szlProtectionIndex)
	if err != nil {
		return model.ProtectionNone, err
	}
	recs, err := szlRecords(resp.Data)
	if err != nil {
		return model.ProtectionNone, err
	}
	if len(recs) == 0 || len(recs[0]) < 8 {
		return model.ProtectionNone, fmt.Errorf("SZL 0x0232 holds no protection record")
	}
	switch binary.BigEndian.Uint16(recs[0][6:8]) {
	case 1:
		return model.ProtectionNone, nil
	case 2:
		return model.ProtectionRead, nil
	case 3:
		return model.ProtectionReadWrite, nil
	}
	return model.ProtectionNone, fmt.Errorf("SZL 0x0232: unknown protection level %d", binary.BigEndian.Uint16(recs[0][6:8]))
}

// ReadDiagBufferRaw returns the SZL 0x00A0 (diagnostic buffer) list data:
// record length, record count and the records, all packets of it.
func (c *Client) ReadDiagBufferRaw(ctx context.Context) ([]byte, error) {
	if err := c.beginCall(ctx); err != nil {
		return nil, err
	}
	defer c.endCall()

	resp, err := c.readSZL(ctx, wire.SZLDiagBuffer, 0)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// diagRecordSize is the length of one diagnostic buffer record.
const diagRecordSize = 20

// ReadDiagBuffer reads the diagnostic buffer (SZL 0x00A0): per record the
// event ID, its event class (high nibble), the priority class, and the
// two info fields; Timestamp is the BCD time stamp in hex.
func (c *Client) ReadDiagBuffer(ctx context.Context) (*model.DiagBuffer, error) {
	raw, err := c.ReadDiagBufferRaw(ctx)
	if err != nil {
		return nil, err
	}
	recs, err := szlRecords(raw)
	if err != nil {
		return nil, err
	}
	buf := &model.DiagBuffer{}
	for _, r := range recs {
		if len(r) < diagRecordSize {
			return buf, fmt.Errorf("diagnostic record of %d bytes", len(r))
		}
		buf.Entries = append(buf.Entries, model.DiagEntry{
			EventID:    binary.BigEndian.Uint16(r[0:2]),
			EventClass: r[0] >> 4,
			Priority:   r[2],
			Info1:      uint32(binary.BigEndian.Uint16(r[6:8])),
			Info2:      binary.BigEndian.Uint32(r[8:12]),
			Timestamp:  fmt.Sprintf("%x", r[12:20]),
		})
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
