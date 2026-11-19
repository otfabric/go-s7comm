// SPDX-License-Identifier: MIT

package server_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/client"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/server"
	"github.com/otfabric/go-s7comm/wire"
)

// itemCode is the item return code a client's error carries, or 0.
func itemCode(err error) byte {
	if e, ok := errors.AsType[*wire.S7Error](err); ok {
		return e.Code
	}
	return 0
}

type seen struct {
	mu     sync.Mutex
	reads  []server.Item
	writes []server.Item
	data   [][]byte
}

func (s *seen) read(it server.Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, it)
}

func (s *seen) write(it server.Item, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, it)
	s.data = append(s.data, data)
}

func TestOnWriteAppliesWhatItAccepts(t *testing.T) {
	var got seen
	srv := newServer(t, server.Config{OnWrite: func(_ context.Context, it server.Item, data []byte) byte {
		got.write(it, data)
		if it.Area == server.AreaDB && it.Start == 10 {
			return wire.RetCodeHWFault
		}
		return wire.RetCodeSuccess
	}})
	c := dial(t, start(t, srv))
	ctx := context.Background()

	must(t, c.WriteDB(ctx, 1, 0, []byte{9, 8}))
	if b, _ := srv.Read(server.AreaDB, 1, 0, 2); !bytes.Equal(b, []byte{9, 8}) {
		t.Errorf("accepted write: DB1 = %v", b)
	}
	err := c.WriteDB(ctx, 1, 10, []byte{7, 7})
	if itemCode(err) != wire.RetCodeHWFault {
		t.Errorf("refused write: err = %v, want item code 0x01", err)
	}
	if b, _ := srv.Read(server.AreaDB, 1, 10, 2); !bytes.Equal(b, []byte{0, 0}) {
		t.Errorf("refused write changed DB1: %v", b)
	}
	must(t, c.WriteDBBit(ctx, 1, 20, 3, true))
	if b, _ := srv.Read(server.AreaDB, 1, 20, 1); b[0] != 0b1000 {
		t.Errorf("bit write: DB1.DBB20 = %08b", b[0])
	}

	got.mu.Lock()
	defer got.mu.Unlock()
	if len(got.writes) != 3 {
		t.Fatalf("OnWrite asked %d times, want 3", len(got.writes))
	}
	w := got.writes[0]
	if w.Area != server.AreaDB || w.DB != 1 || w.Start != 0 || w.Size != 2 || w.Bit != -1 || w.Client == nil {
		t.Errorf("byte item = %+v", w)
	}
	if !bytes.Equal(got.data[0], []byte{9, 8}) {
		t.Errorf("byte item data = %v", got.data[0])
	}
	b := got.writes[2]
	if b.Start != 20 || b.Bit != 3 || b.Size != 1 || got.data[2][0]&1 != 1 {
		t.Errorf("bit item = %+v data %v", b, got.data[2])
	}
}

func TestOnWriteMayChangeTheDataItApplies(t *testing.T) {
	srv := newServer(t, server.Config{OnWrite: func(_ context.Context, _ server.Item, data []byte) byte {
		data[1] = 0x42
		return wire.RetCodeSuccess
	}})
	c := dial(t, start(t, srv))
	must(t, c.WriteDB(context.Background(), 1, 30, []byte{1, 2}))
	if b, _ := srv.Read(server.AreaDB, 1, 30, 2); !bytes.Equal(b, []byte{1, 0x42}) {
		t.Errorf("DB1 = %v, want the hook's bytes", b)
	}
}

func TestOnWriteIsNotAskedForRefusedItems(t *testing.T) {
	var got seen
	srv := newServer(t, server.Config{OnWrite: func(_ context.Context, it server.Item, data []byte) byte {
		got.write(it, data)
		return wire.RetCodeSuccess
	}})
	c := dial(t, start(t, srv))
	ctx := context.Background()
	if itemCode(c.WriteDB(ctx, 2, 0, []byte{0})) != wire.RetCodeAccessFault {
		t.Error("write to read-only DB2 not refused with 0x03")
	}
	if c.WriteDB(ctx, 9, 0, []byte{0}) == nil {
		t.Error("write to unregistered DB9 succeeded")
	}
	if c.WriteDB(ctx, 1, 63, []byte{1, 2}) == nil {
		t.Error("write past the end of DB1 succeeded")
	}
	got.mu.Lock()
	defer got.mu.Unlock()
	if len(got.writes) != 0 {
		t.Errorf("OnWrite asked for refused items: %+v", got.writes)
	}
}

func TestOnWriteAsksEachItemOfAJobInOrder(t *testing.T) {
	var got seen
	srv := newServer(t, server.Config{OnWrite: func(_ context.Context, it server.Item, data []byte) byte {
		got.write(it, data)
		if it.Area == server.AreaMerkers {
			return wire.RetCodeAccessFault
		}
		return wire.RetCodeSuccess
	}})
	c := dial(t, start(t, srv))
	errs, err := c.WriteMulti(context.Background(), []client.WriteItem{
		{Addr: model.Address{Area: model.AreaDB, DBNumber: 1, Start: 4, Size: 2}, Data: []byte{1, 1}},
		{Addr: model.Address{Area: model.AreaMerkers, Start: 0, Size: 1}, Data: []byte{2}},
		{Addr: model.Address{Area: model.AreaOutputs, Start: 1, Size: 1}, Data: []byte{3}},
	})
	must(t, err)
	if errs[0] != nil || itemCode(errs[1]) != wire.RetCodeAccessFault || errs[2] != nil {
		t.Errorf("item errors = %v", errs)
	}
	if b, _ := srv.Read(server.AreaMerkers, 0, 0, 1); b[0] != 0 {
		t.Errorf("refused item changed M: %v", b)
	}
	if b, _ := srv.Read(server.AreaOutputs, 0, 1, 1); b[0] != 3 {
		t.Errorf("Q1 = %v", b)
	}
	got.mu.Lock()
	defer got.mu.Unlock()
	if len(got.writes) != 3 || got.writes[0].Area != server.AreaDB || got.writes[1].Area != server.AreaMerkers || got.writes[2].Area != server.AreaOutputs {
		t.Errorf("order = %+v", got.writes)
	}
}

func TestOnWriteContextEndsWithClose(t *testing.T) {
	entered := make(chan struct{})
	ended := make(chan error, 1)
	srv := newServer(t, server.Config{OnWrite: func(ctx context.Context, _ server.Item, _ []byte) byte {
		close(entered)
		<-ctx.Done()
		ended <- ctx.Err()
		return wire.RetCodeHWFault
	}})
	c := dial(t, start(t, srv))
	go func() { _ = c.WriteDB(context.Background(), 1, 0, []byte{1}) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("OnWrite not called")
	}
	done := make(chan struct{})
	go func() { _ = srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited on a write hook")
	}
	if err := <-ended; !errors.Is(err, context.Canceled) {
		t.Errorf("hook context ended with %v", err)
	}
}

func TestCheckReadRefusesItems(t *testing.T) {
	var got seen
	srv := newServer(t, server.Config{CheckRead: func(it server.Item) byte {
		got.read(it)
		if it.Area == server.AreaDB && it.Start >= 8 {
			return wire.RetCodeHWFault
		}
		return wire.RetCodeSuccess
	}})
	c := dial(t, start(t, srv))
	ctx := context.Background()

	if b := readDB(t, c, 1, 0, 4); !bytes.Equal(b, []byte{1, 2, 3, 4}) {
		t.Errorf("checked read = %v", b)
	}
	res, err := c.ReadDB(ctx, 1, 8, 2)
	must(t, err)
	if res.ReturnCode != wire.RetCodeHWFault {
		t.Errorf("refused read: code %#x, want 0x01", res.ReturnCode)
	}
	bit, err := c.ReadDBBit(ctx, 1, 0, 0)
	must(t, err)
	if !bit {
		t.Error("DB1.DBX0.0 = false")
	}
	results, err := c.ReadMulti(ctx, []model.Address{
		{Area: model.AreaDB, DBNumber: 1, Start: 2, Size: 2},
		{Area: model.AreaDB, DBNumber: 1, Start: 12, Size: 2},
		{Area: model.AreaInputs, Start: 0, Size: 2},
	})
	must(t, err)
	if results[0].Err() != nil || results[1].ReturnCode != wire.RetCodeHWFault || results[2].Err() != nil {
		t.Errorf("multi read codes = %#x %#x %#x", results[0].ReturnCode, results[1].ReturnCode, results[2].ReturnCode)
	}

	got.mu.Lock()
	defer got.mu.Unlock()
	first := got.reads[0]
	if first.Area != server.AreaDB || first.DB != 1 || first.Start != 0 || first.Size != 4 || first.Bit != -1 || first.Client == nil {
		t.Errorf("read item = %+v", first)
	}
	if b := got.reads[2]; b.Bit != 0 || b.Size != 1 {
		t.Errorf("bit read item = %+v", b)
	}
}
