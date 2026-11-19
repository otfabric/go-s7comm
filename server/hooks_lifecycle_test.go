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

// L9. OnWrite runs with no lock held, so it may call the server's Read and
// Write: here it reads the area it is asked about and mirrors the value into
// the markers. The client's write is applied and the mirror holds it.
func TestOnWriteMayCallReadAndWrite(t *testing.T) {
	var srv *server.Server
	srv = newServer(t, server.Config{OnWrite: func(_ context.Context, it server.Item, data []byte) byte {
		if _, err := srv.Read(it.Area, it.DB, it.Start, it.Size); err != nil {
			t.Errorf("Read from OnWrite: %v", err)
		}
		if err := srv.Write(server.AreaMerkers, 0, it.Start, data); err != nil {
			t.Errorf("Write from OnWrite: %v", err)
		}
		return wire.RetCodeSuccess
	}})
	c := dial(t, start(t, srv))
	if err := c.WriteDB(context.Background(), 1, 4, []byte{0x5A, 0xA5}); err != nil {
		t.Fatalf("WriteDB: %v", err)
	}
	for _, chk := range []struct {
		area server.Area
		db   int
	}{{server.AreaDB, 1}, {server.AreaMerkers, 0}} {
		if got, _ := srv.Read(chk.area, chk.db, 4, 2); !bytes.Equal(got, []byte{0x5A, 0xA5}) {
			t.Errorf("%s at 4 = % x, want 5a a5", chk.area, got)
		}
	}
}

// L10. Close waits for every connection goroutine, and a hook runs on one, so
// a hook that stops the server calls Close in a goroutine of its own (the
// documented way): Close ends the hook's context, completes once the hook
// returns, and the client's job gets no answer.
func TestHookStartsClose(t *testing.T) {
	closed := make(chan error, 1)
	var srv *server.Server
	srv = newServer(t, server.Config{OnWrite: func(ctx context.Context, _ server.Item, _ []byte) byte {
		go func() { closed <- srv.Close() }()
		<-ctx.Done()
		return wire.RetCodeHWFault
	}})
	c := dial(t, start(t, srv))
	err := c.WriteDB(context.Background(), 1, 0, []byte{1})
	if err == nil {
		t.Error("WriteDB succeeded on a server closed during the job")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close started from a hook did not complete")
	}
	if got, _ := srv.Read(server.AreaDB, 1, 0, 1); got[0] != 1 {
		// DB1 byte 0 is 1 from newServer: the refused write left it.
		t.Errorf("DB1 byte 0 = %d, want 1 (unchanged)", got[0])
	}
}

// The OnWrite path under load: four clients send multi-item jobs to their own
// bytes while the host reads and writes other areas, and CheckRead and
// OnWrite run concurrently (under -race). OnWrite refuses every value that is
// a multiple of 4; each client's bytes end with its last accepted value, and
// every refused item carries OnWrite's code.
func TestOnWriteUnderConcurrentClientsAndHostAccess(t *testing.T) {
	const clients, jobs = 4, 100
	srv := newServer(t, server.Config{
		CheckRead: func(it server.Item) byte {
			if it.Area == server.AreaMerkers && it.Start == 31 {
				return wire.RetCodeAccessFault
			}
			return wire.RetCodeSuccess
		},
		OnWrite: func(_ context.Context, _ server.Item, data []byte) byte {
			if data[0]%4 == 0 {
				return wire.RetCodeHWFault
			}
			return wire.RetCodeSuccess
		},
	})
	port := start(t, srv)
	stop := make(chan struct{})
	var host sync.WaitGroup
	host.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := srv.Write(server.AreaMerkers, 0, i%30, []byte{byte(i)}); err != nil {
				t.Errorf("host Write: %v", err)
				return
			}
			if _, err := srv.Read(server.AreaDB, 1, 0, 64); err != nil {
				t.Errorf("host Read: %v", err)
				return
			}
		}
	})
	var wg sync.WaitGroup
	for k := range clients {
		c := dial(t, port)
		wg.Go(func() {
			for v := 1; v <= jobs; v++ {
				items := make([]client.WriteItem, 3)
				for j := range items {
					items[j] = client.WriteItem{Addr: model.Address{Area: model.AreaDB, DBNumber: 1, Start: 8*k + j}, Data: []byte{byte(v)}}
				}
				errs, err := c.WriteMulti(context.Background(), items)
				if err != nil {
					t.Errorf("client %d job %d: %v", k, v, err)
					return
				}
				for j, e := range errs {
					if refused := v%4 == 0; refused != (e != nil) || (refused && itemCode(e) != wire.RetCodeHWFault) {
						t.Errorf("client %d job %d item %d: %v", k, v, j, e)
					}
				}
				res, rerr := c.ReadMulti(context.Background(), []model.Address{{Area: model.AreaMerkers, Start: 31, Size: 1}, {Area: model.AreaDB, DBNumber: 1, Start: 8 * k, Size: 3}})
				if rerr != nil || res[0].Status != client.ReadStatusRejected || !res[1].OK() {
					t.Errorf("client %d job %d: read %+v %+v, %v", k, v, res[0], res[1], rerr)
				}
			}
		})
	}
	wg.Wait()
	close(stop)
	host.Wait()
	for k := range clients {
		if got, _ := srv.Read(server.AreaDB, 1, 8*k, 3); !bytes.Equal(got, []byte{99, 99, 99}) {
			t.Errorf("client %d bytes % x, want its last accepted value 99", k, got)
		}
	}
}

// A hook that ignores its context holds Close until it returns: Close does
// not abandon a connection goroutine. Close returns once the hook does.
func TestCloseWaitsForAHookThatIgnoresItsContext(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := newServer(t, server.Config{OnWrite: func(context.Context, server.Item, []byte) byte {
		close(entered)
		<-release
		return wire.RetCodeSuccess
	}})
	c := dial(t, start(t, srv))
	go func() { _ = c.WriteDB(context.Background(), 1, 0, []byte{1}) }()
	<-entered
	done := make(chan error, 1)
	go func() { done <- srv.Close() }()
	select {
	case err := <-done:
		t.Fatalf("Close returned (%v) while a hook was running", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, server.ErrServerClosed) {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the hook did")
	}
}
