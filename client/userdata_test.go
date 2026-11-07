// SPDX-License-Identifier: MIT

package client

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// A list longer than one PDU arrives in packets marked "more data
// follows"; the client asks for each further one with the sequence number
// of the last answer, as Snap7 and TIA Portal do.
func TestUserdata_FollowUpsJoinPackets(t *testing.T) {
	var seqSeen atomic.Int32
	port := startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
		param := req[10:]
		if param[3] == 0x04 { // first request
			data := append([]byte{wire.RetCodeSuccess, 0x09, 0x00, 0x08, 0x00, 0xa0, 0x00, 0x00}, 0x00, 0x14, 0x00, 0x02)
			data[3] = byte(len(data) - 4)
			return buildUserdataResponse(ref, wire.UserdataGroupCPU, wire.UserdataSZL, 7, true, data)
		}
		seqSeen.Store(int32(param[7]))
		recs := append(append(make([]byte, 0, 40), 0x43, 0x02, 0xff), make([]byte, 17)...)
		recs = append(recs, 0x13, 0x81, 0x1b)
		recs = append(recs, make([]byte, 17)...)
		return buildUserdataResponse(ref, wire.UserdataGroupCPU, wire.UserdataSZL, 7, false, append([]byte{wire.RetCodeSuccess, 0x09, 0x00, byte(len(recs))}, recs...))
	})
	c := connectScripted(t, port)
	buf, err := c.ReadDiagBuffer(testContext(t))
	if err != nil {
		t.Fatalf("ReadDiagBuffer: %v", err)
	}
	if seqSeen.Load() != 7 || len(buf.Entries) != 2 || buf.Entries[1].EventID != 0x1381 {
		t.Fatalf("sequence %d, entries %+v", seqSeen.Load(), buf.Entries)
	}
}

func TestUserdata_FollowUpFailures(t *testing.T) {
	first := func(ref uint16) []byte {
		return buildUserdataResponse(ref, wire.UserdataGroupBlock, wire.UserdataBlockCount, 1, true,
			[]byte{wire.RetCodeSuccess, 0x09, 0x00, 0x04, 0x30, 0x38, 0x00, 0x01})
	}
	cases := []struct {
		name   string
		follow func(ref uint16) []byte
		want   string
	}{
		{"never ends", first, "longer than 256 packets"},
		{"refused", func(ref uint16) []byte {
			return buildUserdataResponse(ref, wire.UserdataGroupBlock, wire.UserdataBlockCount, 1, false, []byte{0x0a, 0, 0, 0})
		}, "not available"},
		{"bad parameter", func(ref uint16) []byte { return buildAckData(ref, 0, 0, []byte{0x00}, nil) }, "user data follow-up"},
		{"header error", func(ref uint16) []byte { return buildAckData(ref, 0x81, 0x04, nil, nil) }, "0x81"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
				if req[10+3] == 0x04 {
					return first(ref)
				}
				return tc.follow(ref)
			})
			c := connectScripted(t, port)
			if _, err := c.BlockCounts(testContext(t)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestUserdata_FirstAnswerFailures(t *testing.T) {
	cases := []struct {
		name  string
		reply func(ref uint16) []byte
		want  error
	}{
		{"not user data", func(ref uint16) []byte { return buildAckData(ref, 0, 0, []byte{0x00}, nil) }, ErrProtocolFailure},
		{"bad block count", func(ref uint16) []byte {
			return buildUserdataResponse(ref, wire.UserdataGroupBlock, wire.UserdataBlockCount, 0, false, []byte{wire.RetCodeSuccess, 0x09, 0x00, 0x03, 1, 2, 3})
		}, ErrProtocolFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte { return tc.reply(ref) })
			c := connectScripted(t, port)
			if _, err := c.BlockCounts(testContext(t)); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadDiagBuffer_RecordErrors(t *testing.T) {
	for name, data := range map[string][]byte{
		"record too short": szlList(10, []byte{0x43, 0x02}),
		"record length 0":  {0, 0, 0, 1},
	} {
		t.Run(name, func(t *testing.T) {
			port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
				return buildSZLReply(ref, wire.SZLDiagBuffer, data)
			})
			c := connectScripted(t, port)
			if _, err := c.ReadDiagBuffer(testContext(t)); err == nil {
				t.Fatal("no error")
			}
		})
	}
}

func TestGetBlockInfo_Validation(t *testing.T) {
	port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte { return buildSZLItemError(ref, 0x0a) })
	c := connectScripted(t, port)
	for _, tc := range []struct {
		bt  model.BlockType
		num int
	}{{model.BlockDB, -1}, {model.BlockDB, wire.MaxUploadBlockNumber + 1}, {model.BlockType(0x99), 1}} {
		var v *ValidationError
		if _, err := c.GetBlockInfo(testContext(t), tc.bt, tc.num); !errors.As(err, &v) {
			t.Errorf("%v %d: err = %v, want ValidationError", tc.bt, tc.num, err)
		}
	}
}
