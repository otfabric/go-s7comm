// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-s7comm/model"
)

func TestIdentifyReturnsEmptyForUnknownFields(t *testing.T) {
	// Fields a CPU does not report stay empty, never "N/A".
	info := &model.DeviceInfo{}
	if err := applyComponentID(info, szlList(34, append([]byte{0x00, 0x09}, "vendor"...))); err != nil {
		t.Fatal(err)
	}
	if *info != (model.DeviceInfo{}) {
		t.Fatalf("unknown index filled fields: %+v", info)
	}
}

func TestReadDiagBufferRawNotConnected(t *testing.T) {
	_, err := New("host").ReadDiagBufferRaw(context.Background())
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}
