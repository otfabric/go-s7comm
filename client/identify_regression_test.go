// SPDX-License-Identifier: MIT

package client

import (
	"encoding/binary"
	"testing"

	"github.com/otfabric/go-s7comm/wire"
)

// SZL 0x0011 records are 28 bytes. A record length or record count that
// runs past the data received must be reported, not sliced past its end.
func TestIdentify_ModuleRecordsShorterThanLayout(t *testing.T) {
	for _, data := range [][]byte{
		szlList(20, []byte{0x00, 0x01, 'x'}),      // records shorter than the layout
		{0x00, 0x1c, 0x00, 0x05, 0x00, 0x01, 'x'}, // count past the data: no whole record
		{0x00, 0x00, 0x00, 0x01},                  // record length 0
	} {
		port := startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
			if binary.BigEndian.Uint16(req[22:24]) != wire.SZLModuleID {
				return buildSZLReply(ref, wire.SZLComponentID, szlList(34))
			}
			return buildSZLReply(ref, wire.SZLModuleID, data)
		})
		c := connectScripted(t, port)

		info, err := c.Identify(testContext(t))
		if info == nil || info.OrderNumber != "" {
			t.Fatalf("% x: info = %+v, err = %v, want partial info without an order number", data, info, err)
		}
	}
}
