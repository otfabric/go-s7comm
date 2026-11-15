// SPDX-License-Identifier: MIT

package client

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// What an answer must be, beyond carrying the request's PDU reference: of a
// message type that answers the request, for user data of the function
// group and subfunction asked for, and for an SZL read of the list asked
// for. The recorded CPUs answer this way (testdata/captures); the capture
// replay tests run every client call against their answers.

// pduOfType builds an S7 PDU of any message type with the given sections.
// Ack and Ack-Data carry the two error bytes, the other types do not.
func pduOfType(rosctr wire.ROSCTR, ref uint16, param, data []byte) []byte {
	h := []byte{0x32, byte(rosctr), 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(h[4:6], ref)
	binary.BigEndian.PutUint16(h[6:8], uint16(len(param)))
	binary.BigEndian.PutUint16(h[8:10], uint16(len(data)))
	if rosctr.IsAck() || rosctr.IsAckData() {
		h = append(h, 0, 0)
	}
	return append(append(h, param...), data...)
}

// A job is answered by an acknowledgement. A PDU of another message type
// with the job's reference is refused, also when its sections would decode
// as a successful answer.
func TestJobAnswerMessageType(t *testing.T) {
	writeOK := []byte{wire.FuncWriteVar, 1}
	for _, tc := range []struct {
		name   string
		rosctr wire.ROSCTR
		ok     bool
	}{
		{"Ack-Data", wire.ROSCTRAckData, true},
		{"Ack", wire.ROSCTRAck, true},
		{"Job", wire.ROSCTRJob, false},
		{"user data", wire.ROSCTRUserdata, false},
		{"type 0x00", 0x00, false},
		{"type 0x08", 0x08, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
				return pduOfType(tc.rosctr, ref, writeOK, []byte{wire.RetCodeSuccess})
			})
			c := connectScripted(t, port)
			err := c.WriteDB(testContext(t), 1, 0, []byte{1})
			if tc.ok && err != nil {
				t.Fatalf("WriteDB: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrProtocolFailure) {
				t.Fatalf("WriteDB answered by a PDU of type 0x%02X: err = %v, want ErrProtocolFailure", byte(tc.rosctr), err)
			}
			if c.PDUSize() == 0 {
				t.Error("the session ended; a refused answer leaves it open")
			}
		})
	}
}

// A job refused with an Ack PDU, as a recorded CPU 315-2 PN/DP refuses a
// start upload (header error 0xD2 0x0C, no parameter section), is reported
// with its error class and code.
func TestJobRefusedByAckIsAnS7Error(t *testing.T) {
	port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
		ack := pduOfType(wire.ROSCTRAck, ref, nil, nil)
		ack[10], ack[11] = 0xd2, 0x0c
		return ack
	})
	c := connectScripted(t, port)
	_, err := c.UploadBlock(testContext(t), model.BlockSDB, 0)
	var s7Err *wire.S7Error
	if !errors.As(err, &s7Err) || s7Err.Class != 0xd2 || s7Err.Code != 0x0c {
		t.Fatalf("err = %v, want S7Error 0xD2 0x0C", err)
	}
}

// The answer to Setup Communication is an acknowledgement too: the request
// sent back, or the same parameter in a user data PDU, does not connect.
func TestSetupAnswerMessageType(t *testing.T) {
	setupParam := []byte{wire.FuncSetupComm, 0, 0, 1, 0, 1, 0x01, 0xe0}
	for _, tc := range []struct {
		name   string
		rosctr wire.ROSCTR
		ok     bool
	}{
		{"Ack-Data", wire.ROSCTRAckData, true},
		{"the request sent back", wire.ROSCTRJob, false},
		{"user data", wire.ROSCTRUserdata, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, func(ref uint16) []byte { return pduOfType(tc.rosctr, ref, setupParam, nil) }, nil)
			c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1))
			err := c.Connect(testContext(t))
			defer func() { _ = c.Close() }()
			if tc.ok {
				if err != nil || c.PDUSize() != 480 {
					t.Fatalf("Connect = %v, PDU size %d", err, c.PDUSize())
				}
				return
			}
			if !errors.Is(err, ErrProtocolFailure) {
				t.Fatalf("Connect = %v, want ErrProtocolFailure", err)
			}
			requireDisconnected(t, c)
		})
	}
}

// A user data request is answered by a user data response of the function
// group and subfunction that were asked for. An answer of another group or
// subfunction, or a request sent back, is refused although its data section
// would decode.
func TestUserdataAnswerMatchesRequest(t *testing.T) {
	counts := []byte{wire.RetCodeSuccess, 0x09, 0x00, 0x04, 0x30, wire.BlockTypeDB, 0x00, 0x02}
	answer := func(typeAndGroup, subfunc byte) func(ref uint16, _ []byte) []byte {
		return func(ref uint16, _ []byte) []byte {
			param := []byte{0x00, 0x01, 0x12, 0x08, wire.UserdataRes, typeAndGroup, subfunc, 0x00, 0x00, 0x00, 0x00, 0x00}
			return pduOfType(wire.ROSCTRUserdata, ref, param, counts)
		}
	}
	for _, tc := range []struct {
		name  string
		reply func(ref uint16, req []byte) []byte
		ok    bool
	}{
		{"the block count response", answer(wire.UserdataTypeResponse|wire.UserdataGroupBlock, wire.UserdataBlockCount), true},
		{"another subfunction", answer(wire.UserdataTypeResponse|wire.UserdataGroupBlock, wire.UserdataBlockList), false},
		{"another function group", answer(wire.UserdataTypeResponse|wire.UserdataGroupCPU, wire.UserdataBlockCount), false},
		{"a request", answer(wire.UserdataTypeRequest|wire.UserdataGroupBlock, wire.UserdataBlockCount), false},
		{"a push", answer(wire.UserdataGroupBlock, wire.UserdataBlockCount), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := connectScripted(t, startScriptedPeer(t, nil, tc.reply))
			got, err := c.BlockCounts(testContext(t))
			if tc.ok {
				if err != nil || got[model.BlockDB] != 2 {
					t.Fatalf("BlockCounts = %v, %v", got, err)
				}
				return
			}
			if !errors.Is(err, ErrProtocolFailure) || got != nil {
				t.Fatalf("BlockCounts = %v, %v; want ErrProtocolFailure", got, err)
			}
		})
	}
}

// A follow-up packet is checked like the first one.
func TestUserdataFollowUpMatchesRequest(t *testing.T) {
	c := connectScripted(t, startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
		data := []byte{wire.RetCodeSuccess, 0x09, 0x00, 0x04, 0x00, 0x01, 0x22, 0x05}
		if req[wire.S7HeaderSize+3] == 0x04 {
			return buildUserdataResponse(ref, wire.UserdataGroupBlock, wire.UserdataBlockList, 1, true, data)
		}
		return buildUserdataResponse(ref, wire.UserdataGroupBlock, wire.UserdataBlockInfo, 1, false, data)
	}))
	if blocks, err := c.ListBlocks(testContext(t), model.BlockDB); !errors.Is(err, ErrProtocolFailure) || blocks != nil {
		t.Fatalf("ListBlocks = %v, %v; want ErrProtocolFailure", blocks, err)
	}
}

// userdataWithError builds an SZL answer whose parameter section carries
// errorCode and whose data section is data.
func userdataWithError(ref, errorCode uint16, data []byte) []byte {
	param := []byte{0x00, 0x01, 0x12, 0x08, wire.UserdataRes, wire.UserdataTypeResponse | wire.UserdataGroupCPU, wire.UserdataSZL, 0x00, 0x00, 0x00, 0x00, 0x00}
	binary.BigEndian.PutUint16(param[10:12], errorCode)
	return pduOfType(wire.ROSCTRUserdata, ref, param, data)
}

// The error code in the parameter section of a user data answer is read. A
// refusal that also carries an item return code, as recorded CPUs send it,
// keeps that code and names the parameter error; an error code on an answer
// whose data section holds a list is an error of that code, and the list is
// not returned.
func TestUserdataParameterErrorCode(t *testing.T) {
	list := append([]byte{wire.RetCodeSuccess, 0x09, 0x00, 0x08, 0x00, 0xa0, 0x00, 0x00}, szlList(20)...)
	for _, tc := range []struct {
		name        string
		errorCode   uint16
		data        []byte
		ok          bool
		class, code byte
		message     string
	}{
		{"no error", 0, list, true, 0, 0, ""},
		{"refusal, return code only", 0, []byte{wire.RetCodeNotAvailable, 0, 0, 0}, false, 0, wire.RetCodeNotAvailable, "object not available"},
		{"refusal, both codes", 0xd402, []byte{wire.RetCodeNotAvailable, 0, 0, 0}, false, 0, wire.RetCodeNotAvailable, "object not available (parameter error 0xD402: information function unavailable)"},
		{"refusal, unknown error code", 0xd4ff, []byte{wire.RetCodeAccessFault, 0, 0, 0}, false, 0, wire.RetCodeAccessFault, "access denied (parameter error 0xD4FF: parameter error 0xD4FF)"},
		{"error code with a list", 0xd402, list, false, 0xd4, 0x02, "information function unavailable"},
		{"error code with a cut data section", 0x8104, []byte{wire.RetCodeSuccess, 0x09}, false, 0x81, 0x04, "service not implemented or frame error"},
		{"error code 1", 0x0001, list, false, 0x00, 0x01, "parameter error 0x0001"},
		{"error code 0xFFFF", 0xffff, list, false, 0xff, 0xff, "parameter error 0xFFFF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := connectScripted(t, startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
				return userdataWithError(ref, tc.errorCode, tc.data)
			}))
			raw, err := c.ReadDiagBufferRaw(testContext(t))
			if tc.ok {
				if err != nil || len(raw) != 4 {
					t.Fatalf("ReadDiagBufferRaw = % x, %v", raw, err)
				}
				return
			}
			var s7Err *wire.S7Error
			if !errors.As(err, &s7Err) || raw != nil {
				t.Fatalf("ReadDiagBufferRaw = % x, %v; want an S7Error and no data", raw, err)
			}
			if s7Err.Class != tc.class || s7Err.Code != tc.code || s7Err.Message != tc.message {
				t.Errorf("S7Error = class 0x%02X, code 0x%02X, %q\nwant         class 0x%02X, code 0x%02X, %q",
					s7Err.Class, s7Err.Code, s7Err.Message, tc.class, tc.code, tc.message)
			}
		})
	}
}

// An SZL answer carries the ID of the list that was asked for. Its index is
// not compared: a recorded CPU answers a request for index 4 with index 0.
func TestSZLAnswerIsTheListAskedFor(t *testing.T) {
	record := szlList(40, []byte{0, 4, 0, 1, 0, 0, 0, 2, 0, 2})
	for _, tc := range []struct {
		name      string
		id, index uint16
		ok        bool
	}{
		{"the list and index asked for", wire.SZLProtectionInfo, szlProtectionIndex, true},
		{"the list, index 0", wire.SZLProtectionInfo, 0, true},
		{"another list", wire.SZLCPUState, szlProtectionIndex, false},
		{"list 0", 0, szlProtectionIndex, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := connectScripted(t, startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
				answer := buildSZLReply(ref, tc.id, record)
				binary.BigEndian.PutUint16(answer[wire.S7HeaderSize+12+6:], tc.index)
				return answer
			}))
			level, err := c.GetProtectionLevel(testContext(t))
			if tc.ok {
				if err != nil || level != model.ProtectionRead {
					t.Fatalf("GetProtectionLevel = %v, %v", level, err)
				}
				return
			}
			if !errors.Is(err, ErrProtocolFailure) || !strings.Contains(err.Error(), "asked for 0x0232") {
				t.Fatalf("GetProtectionLevel = %v, %v; want ErrProtocolFailure naming the list", level, err)
			}
		})
	}
}
