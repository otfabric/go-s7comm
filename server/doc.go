// SPDX-License-Identifier: MIT

// Package server is an S7 communication server for tests: it holds data
// blocks, inputs, outputs and markers in memory and answers classic S7
// clients on ISO-on-TCP (RFC 1006, COTP TP0 through go-cotp).
//
// It answers Setup Communication, Read Var and Write Var (S7ANY items, up
// to 20 per job), with the item return codes, response transport sizes
// and PDU negotiation of the Snap7 server (SCADACS/snap7 f6ff903, the one
// in snap7-interop). It accepts any rack, slot and TSAP. User data (SZL,
// clock, block info), block transfer and PLC control get no answer, as
// Snap7 answers jobs it cannot handle; a malformed PDU closes the
// connection. It is test infrastructure, not a PLC: there is no program,
// no timers or counters, no access protection and no TLS.
//
//	srv, err := server.New(server.Config{})
//	if err != nil { ... }
//	_ = srv.RegisterDB(1, make([]byte, 64), server.AreaOptions{})
//	_ = srv.RegisterArea(server.AreaMerkers, make([]byte, 32), server.AreaOptions{})
//	err = srv.ListenAndServe(ctx, "127.0.0.1:1102")
package server
