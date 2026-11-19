// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"testing"

	"github.com/otfabric/go-s7comm/wire"
)

func TestCheckReadAsksEachDBReadRange(t *testing.T) {
	s := testSession(t, 480)
	var asked []Item
	s.srv.cfg.CheckRead = func(it Item) byte {
		asked = append(asked, it)
		if it.DB == 3 {
			return wire.RetCodeHWFault
		}
		return wire.RetCodeSuccess
	}
	_, data := answer(t, s, readJob(dbreadItem([3]int{2, 1, 0}, [3]int{1, 1, 7})))
	if want := []byte{0xFF, 0x09, 0x00, 0x05, 0xFF, 0x01, 0x02, 0xFF, 0x80}; !bytes.Equal(data, want) {
		t.Fatalf("data % x\nwant % x", data, want)
	}
	if len(asked) != 2 || asked[0].Area != AreaDB || asked[0].DB != 1 || asked[0].Start != 0 || asked[0].Size != 2 || asked[0].Bit != -1 || asked[1].Start != 7 || asked[1].Size != 1 {
		t.Errorf("asked %+v", asked)
	}
	// One refused range refuses the whole item with its code.
	_, data = answer(t, s, readJob(dbreadItem([3]int{2, 1, 0}, [3]int{2, 3, 0})))
	if len(data) < 1 || data[0] != wire.RetCodeHWFault {
		t.Errorf("refused range: % x", data)
	}
}
