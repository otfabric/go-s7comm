// Package wire provides S7 PDU encoding and parsing (headers, read/write, setup, SZL, blocks)
// plus S7 TSAP helpers. Live transport uses go-cotp TSDUs; this package does not own TPKT or COTP framing.
//
// Parsing philosophy: the wire layer is strict on framing and declared lengths—buffers are
// validated, lengths are checked, and overruns or mismatches return errors. Semantic
// interpretation (e.g. best-effort field extraction, device-dependent layouts) is left to
// the client and model layers.
package wire
