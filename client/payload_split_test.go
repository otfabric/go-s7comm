// SPDX-License-Identifier: MIT

package client

import (
	"math"
	"testing"

	"github.com/otfabric/go-s7comm/wire"
)

// The two section lengths are 16 bits each, so together they can pass 65535.
// The split adds them as ints at every boundary.
func TestSplitS7Payload(t *testing.T) {
	for _, tc := range []struct {
		name            string
		param, data     uint16
		rest            int
		ok              bool
		wantParam, want int
	}{
		{"empty", 0, 0, 0, true, 0, 0},
		{"exact", 2, 3, 5, true, 2, 3},
		{"trailing octets ignored", 2, 3, 9, true, 2, 3},
		{"short by one", 2, 3, 4, false, 0, 0},
		{"data only", 0, math.MaxUint16, math.MaxUint16, true, 0, math.MaxUint16},
		{"sum 65535", math.MaxUint16 - 2, 2, math.MaxUint16, true, math.MaxUint16 - 2, 2},
		{"sum 65536, long enough", math.MaxUint16, 1, math.MaxUint16 + 1, true, math.MaxUint16, 1},
		{"sum 65537, long enough", math.MaxUint16, 2, math.MaxUint16 + 2, true, math.MaxUint16, 2},
		{"sum 65537, short", math.MaxUint16, 2, math.MaxUint16 + 1, false, 0, 0},
		{"both maximal", math.MaxUint16, math.MaxUint16, 2 * math.MaxUint16, true, math.MaxUint16, math.MaxUint16},
		{"both maximal, short", math.MaxUint16, math.MaxUint16, 2*math.MaxUint16 - 1, false, 0, 0},
	} {
		hdr := &wire.S7Header{ParamLength: tc.param, DataLength: tc.data}
		rest := make([]byte, tc.rest)
		param, data, ok := splitS7Payload(hdr, rest)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if ok && (len(param) != tc.wantParam || len(data) != tc.want) {
			t.Errorf("%s: %d parameter and %d data octets, want %d and %d", tc.name, len(param), len(data), tc.wantParam, tc.want)
		}
	}
}
