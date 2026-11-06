// SPDX-License-Identifier: MIT

// Package server is an S7 communication server for tests: it holds data
// blocks, inputs, outputs and markers in memory and answers classic S7
// clients on ISO-on-TCP (RFC 1006, COTP TP0 through go-cotp).
//
// It answers Setup Communication, Read Var and Write Var (S7ANY items)
// with the item return codes, header errors and data transport sizes
// Siemens defines, as Wireshark's s7comm dissector lists them: 0x0A object
// does not exist, 0x05 invalid address, 0x06 data type not supported, 0x07
// data type inconsistent, 0x03 accessing the object not allowed (read-only
// area); header error 0x8500 for an answer past the PDU and 0x8104 (service
// not implemented) for other jobs and user data. It accepts any rack, slot
// and TSAP. A PDU whose header cannot be read closes the connection. It is
// test infrastructure, not a PLC: there is no program, no timers or
// counters, no access protection and no TLS.
//
//	srv, err := server.New(server.Config{})
//	if err != nil { ... }
//	_ = srv.RegisterDB(1, make([]byte, 64), server.AreaOptions{})
//	_ = srv.RegisterArea(server.AreaMerkers, make([]byte, 32), server.AreaOptions{})
//	err = srv.ListenAndServe(ctx, "127.0.0.1:1102")
package server
