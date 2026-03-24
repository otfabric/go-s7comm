package client

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-s7comm/model"
)

func TestPopulateInfoFromRaw(t *testing.T) {
	info := &model.DeviceInfo{}
	raw := []byte("CPU 1516-3 PN/DP 6ES7516-3AN01-0AB0 FW V2.9.1 SERIAL ABCD12345678")
	populateInfoFromRaw(info, raw)

	if info.OrderNumber == "" {
		t.Fatal("expected order number extracted from raw")
	}
	if info.CPUType == "" {
		t.Fatal("expected cpu type extracted from raw")
	}
	if info.FWVersion == "" {
		t.Fatal("expected firmware extracted from raw")
	}
	if info.SerialNumber == "" {
		t.Fatal("expected serial extracted from raw")
	}
}

func TestIdentifyReturnsEmptyForUnknownFields(t *testing.T) {
	// Library no longer fills "N/A"; unknown fields remain empty.
	info := &model.DeviceInfo{}
	if info.OrderNumber != "" || info.ModuleName != "" || info.CPUType != "" {
		t.Fatal("zero value DeviceInfo should have empty string fields")
	}
}

func TestReadDiagBufferRawNotConnected(t *testing.T) {
	c := New("host")
	ctx := context.Background()
	_, err := c.ReadDiagBufferRaw(ctx)
	if err == nil {
		t.Fatal("ReadDiagBufferRaw without connection should return error")
	}
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}
