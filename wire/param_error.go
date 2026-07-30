// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"fmt"
)

// ParamErrorFromParam reads the 16-bit parameter error code at the standard offset (bytes 2-3, big-endian).
// Many S7 Ack-Data parameter sections carry this code when the response indicates an error.
// Returns (code, true) if param has at least 4 bytes; otherwise (0, false).
func ParamErrorFromParam(param []byte) (code uint16, ok bool) {
	if len(param) < 4 {
		return 0, false
	}
	return binary.BigEndian.Uint16(param[2:4]), true
}

// ParamErrorCodeString returns a human-readable string for the 16-bit S7 parameter error code.
// Codes are from the S7 parameter section of responses (block, upload, diagnostics, etc.).
// Unknown codes return a formatted hex string.
func ParamErrorCodeString(code uint16) string {
	if s, ok := paramErrorCodeNames[code]; ok {
		return s
	}
	return fmt.Sprintf("parameter error 0x%04X", code)
}

// paramErrorCodeNames maps 16-bit parameter error codes to descriptive strings (S7 parameter section).
var paramErrorCodeNames = map[uint16]string{
	0x0000: "no error",
	0x0110: "invalid block number",
	0x0111: "invalid request length",
	0x0112: "invalid parameter",
	0x0113: "invalid block type",
	0x0114: "block not found",
	0x0115: "block already exists",
	0x0116: "block is write-protected",
	0x0117: "block/OS update too large",
	0x0118: "invalid block number",
	0x0119: "incorrect password",
	0x011A: "PG resource error",
	0x011B: "PLC resource error",
	0x011C: "protocol error",
	0x011D: "too many blocks (module restriction)",
	0x011E: "no connection to database or invalid S7DOS handle",
	0x011F: "result buffer too small",
	0x0120: "end of block list",
	0x0140: "insufficient memory",
	0x0141: "job cannot be processed (lack of resources)",
	0x8001: "service cannot be performed while block is in current status",
	0x8003: "S7 protocol error: error while transferring block",
	0x8100: "application error: service unknown to remote module",
	0x8104: "service not implemented or frame error",
	0x8204: "type specification for object inconsistent",
	0x8205: "copied block already exists and is not linked",
	0x8301: "insufficient memory or storage medium not accessible",
	0x8302: "too few resources or processor resources not available",
	0x8304: "no further parallel upload possible (resource bottleneck)",
	0x8305: "function not available",
	0x8306: "insufficient work memory",
	0x8307: "not enough retentive work memory",
	0x8401: "S7 protocol error: invalid service sequence",
	0x8402: "service cannot execute (status of addressed object)",
	0x8404: "S7 protocol: function cannot be performed",
	0x8405: "remote block in DISABLE state (CFB)",
	0x8500: "S7 protocol error: wrong frames",
	0x8503: "alarm from module: service canceled prematurely",
	0x8701: "error addressing object (e.g. area length error)",
	0x8702: "requested service not supported by module",
	0x8703: "access to object refused",
	0x8704: "access error: object damaged",
	0xD001: "protocol error: illegal job number",
	0xD002: "parameter error: illegal job variant",
	0xD003: "parameter error: debugging function not supported by module",
	0xD004: "parameter error: illegal job status",
	0xD005: "parameter error: illegal job termination",
	0xD006: "parameter error: illegal link disconnection ID",
	0xD007: "parameter error: illegal number of buffer elements",
	0xD008: "parameter error: illegal scan rate",
	0xD009: "parameter error: illegal number of executions",
	0xD00A: "parameter error: illegal trigger event",
	0xD00B: "parameter error: illegal trigger condition",
	0xD011: "parameter error: block does not exist",
	0xD012: "parameter error: wrong address in block",
	0xD014: "parameter error: block being deleted/overwritten",
	0xD015: "parameter error: illegal tag address",
	0xD016: "parameter error: test jobs not possible (errors in user program)",
	0xD017: "parameter error: illegal trigger number",
	0xD025: "parameter error: invalid path",
	0xD031: "internal protocol error",
	0xD032: "parameter error: wrong result buffer length",
	0xD033: "protocol error: wrong job length",
	0xD03F: "coding error in parameter section",
	0xD041: "data error: illegal status list ID",
	0xD042: "data error: illegal tag address",
	0xD043: "data error: referenced job not found",
	0xD044: "data error: illegal tag value",
	0xD045: "data error: exiting ODIS control not allowed in HOLD",
	0xD046: "data error: illegal measuring stage (run-time measurement)",
	0xD047: "data error: illegal hierarchy in Read job list",
	0xD048: "data error: illegal deletion ID in Delete job",
	0xD049: "invalid substitute ID in Replace job",
	0xD04A: "error executing program status",
	0xD05F: "coding error in data section",
	0xD061: "resource error: no memory space for job",
	0xD062: "resource error: job list full",
	0xD063: "resource error: trigger event occupied",
	0xD081: "function not permitted in current mode",
	0xD082: "mode error: cannot exit HOLD mode",
	0xD0A1: "function not permitted in current protection level",
	0xD0A2: "function not possible (function modifying memory running)",
	0xD0A3: "too many modify tag jobs active on I/O",
	0xD0A4: "forcing has already been established",
	0xD0A5: "referenced job not found",
	0xD0A6: "job cannot be disabled/enabled",
	0xD0A7: "job cannot be deleted (e.g. currently being read)",
	0xD0A8: "job cannot be replaced (e.g. being read or deleted)",
	0xD0A9: "job cannot be read (e.g. currently being deleted)",
	0xD0AA: "time limit exceeded in processing operation",
	0xD0AB: "invalid job parameters in process operation",
	0xD0AC: "invalid job data in process operation",
	0xD0AD: "operating mode already set",
	0xD0AE: "job set up over different connection",
	0xD0C1: "at least one error while accessing tag(s)",
	0xD0C2: "change to STOP/HOLD mode",
	0xD201: "syntax error in block name",
	0xD202: "syntax error in function parameters",
	0xD209: "one of the given blocks not found on module",
	0xD20E: "no (further) block available",
	0xD210: "invalid block number",
	0xD216: "invalid user program - reset module",
	0xD231: "loaded OB cannot be copied (priority class does not exist)",
	0xD232: "block number of loaded block illegal",
	0xD234: "block exists twice",
	0xD235: "block contains incorrect checksum",
	0xD241: "function not permitted in current protection level",
	0xD250: "update and module ID or version do not match",
	0xD251: "incorrect sequence of operating system components",
	0xD252: "checksum error",
	0xD2A1: "another block function or trigger on block active",
	0xD2A2: "trigger active on block; complete debugging first",
	0xD2A3: "block not active (linked), occupied, or marked for deletion",
	0xD2A4: "block already being processed by another block function",
	0xD401: "information function unavailable",
	0xD402: "information function unavailable",
	0xD601: "syntax error in function parameter",
	0xD602: "incorrect password entered",
	0xD801: "at least one tag address invalid",
	0xD802: "specified job does not exist",
	0xDC01: "date and/or time invalid",
}
