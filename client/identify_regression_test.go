// SPDX-License-Identifier: MIT

package client

import (
	"encoding/binary"
	"testing"

	"github.com/otfabric/go-s7comm/wire"
)

// A module SZL record is 2 index bytes plus a 20-byte order number. A 20- or 21-byte payload
// must be reported as short, not sliced past its end.
func TestIdentify_ModulePayloadShorterThanRecord(t *testing.T) {
	for _, size := range []int{20, 21} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = 'x'
		}
		port := startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
			if binary.BigEndian.Uint16(req[22:24]) != wire.SZLModuleID {
				return buildSZLReply(ref, wire.SZLComponentID, nil)
			}
			// Trim the padding buildSZLReply adds so the record ends the TSDU.
			resp := buildSZLReply(ref, wire.SZLModuleID, payload)
			resp = resp[:len(resp)-4]
			binary.BigEndian.PutUint16(resp[8:10], binary.BigEndian.Uint16(resp[8:10])-4)
			return resp
		})
		c := connectScripted(t, port)

		info, err := c.Identify(testContext(t))
		if err == nil {
			t.Fatalf("size %d: expected a short payload error", size)
		}
		if info == nil || info.OrderNumber != "" {
			t.Fatalf("size %d: info = %+v, want partial info without an order number", size, info)
		}
	}
}
