// SPDX-License-Identifier: MIT

// Package server is an S7 communication server for tests: it holds data
// blocks, inputs, outputs, markers, timers and counters in memory and answers classic S7
// clients on ISO-on-TCP (RFC 1006, COTP TP0 through go-cotp).
//
// It answers Setup Communication, Read Var and Write Var (S7ANY items, and
// DBREAD items as S7-400 CPUs take them, read only)
// with the item return codes, header errors and data transport sizes
// Siemens defines (STEP 7, Use of FETCH/WRITE; Wireshark's s7comm
// dissector names them the same way): 0x0A object does not exist, 0x05 invalid address, 0x06 data type not supported, 0x07
// data type inconsistent, 0x03 accessing the object not allowed (read-only
// area, timers, counters); header error 0x8500 for an answer past the PDU and 0x8104 (service
// not implemented) for other jobs and user data. It accepts any rack, slot
// and TSAP. A PDU whose header cannot be read closes the connection. It is
// test infrastructure, not a PLC: there is no program, timers and counters
// do not run, and there is no access protection and no TLS. For every
// Setup Communication, Read Var and Write Var job recorded from real
// S7-300, S7-400 and S7-1200 CPUs (testdata/captures) it gives the CPU's answer
// byte for byte.
//
//	srv, err := server.New(server.Config{})
//	if err != nil { ... }
//	_ = srv.RegisterDB(1, make([]byte, 64), server.AreaOptions{})
//	_ = srv.RegisterArea(server.AreaMerkers, make([]byte, 32), server.AreaOptions{})
//	err = srv.ListenAndServe(ctx, "127.0.0.1:1102")
package server
