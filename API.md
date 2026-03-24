# API Reference: otfabric/go-s7comm

This document describes the public API exposed by the module and gives practical behavior notes.

## Packages

- **client** — High-level PLC operations (connect, read/write, range scan, compare read, discovery, rack/slot probe, SZL, blocks)
- **model** — Domain data types, areas, value encoders/decoders, device and fingerprint structures
- **transport** — TCP connection wrapper with TPKT framing (go-tpkt); Send/Receive on TPDU payloads
- **wire** — S7 PDU encoding/parsing and COTP helpers (go-cotp)

## client

```go
import "github.com/otfabric/go-s7comm/client"
```

### Construction and lifecycle

```go
func New(host string, opts ...Option) *Client
func (c *Client) Connect(ctx context.Context) error
func (c *Client) Close() error
func (c *Client) ConnectionInfo() model.ConnectionInfo
func (c *Client) PDUSize() int
```

Behavior notes:

- Connect performs TCP dial, COTP setup, and S7 setup communication.
- A second Connect() on an already-connected client only replaces the session after the new handshake succeeds; a failed reconnect leaves the existing session intact.
- On setup failure, connection state is closed and cleared.
- Invalid caller input (port, timeout, max PDU, rack/slot) returns `*ValidationError`.
- Request methods are serialized internally for protocol safety.

### Read result model

Read operations return a structured result so callers can distinguish success, short-read, empty-read, and rejection. The library does not define JSON or other serialization; that is left to CLI/API layers.

```go
type ReadStatus string

const (
    ReadStatusSuccess      ReadStatus = "success"
    ReadStatusShortRead    ReadStatus = "short-read"
    ReadStatusEmptyRead    ReadStatus = "empty-read"
    ReadStatusRejected     ReadStatus = "rejected"
    ReadStatusTimeout      ReadStatus = "timeout"
    ReadStatusTransportErr ReadStatus = "transport-error"
    ReadStatusProtocolErr  ReadStatus = "protocol-error"
    ReadStatusInconclusive ReadStatus = "inconclusive"
)

type ReadResult struct {
    Status          ReadStatus
    RequestedLength int
    ReturnedLength  int
    Data            []byte
    Warnings        []string
    Message         string // human-readable; descriptive, not stable API
    Cause           error  // optional; for errors.Is/Unwrap
    ItemStatus      string
    ReturnCode      byte
}

func (r *ReadResult) OK() bool     // true if Status == ReadStatusSuccess
func (r *ReadResult) Success() bool // same as OK(); prefer res.Err() for flow
func (r *ReadResult) Err() error   // non-nil when Status is not success; returns ErrNilReadResult when r is nil

var ErrNilReadResult      = errors.New("read result is nil")  // returned by ReadResult.Err() when receiver is nil
var ErrNotConnected       = errors.New("not connected")       // stable: methods requiring a session may return or wrap this
var ErrRequestExceedsPDU  = errors.New("request exceeds negotiated PDU size")  // stable
var ErrProtocolFailure    = errors.New("protocol failure")    // advanced/diagnostic: malformed protocol; handshake and request path wrap it

type ValidationError struct { Message string }  // stable: caller-input validation; use errors.As(err, &ValidationError{}) to detect
type PDURefMismatchError struct { Expected, Got uint16 }     // stable: response correlation; use errors.As(err, *PDURefMismatchError)
```

**CLI contract (for s7commctl and other consumers):** To avoid ambiguity, CLIs should define:

- **Top-level `error`**: Use for connection/setup failure (e.g. `Connect` failed, transport error). If non-nil, exit with a failure code; do not treat as success.
- **`ReadResult.Status`**: Use for read outcome. If `err == nil` but `!result.OK()`, the read failed or was short/empty/rejected—exit failure unless the CLI explicitly allows it (e.g. `--allow-short`).
- **Default behavior**: Treat `success` as success; treat `short-read`, `empty-read`, `rejected`, and other non-success statuses as failures (non-zero exit) and surface status in output.
- **`--strict-read`**: If implemented, fail the command (non-zero exit) when status is not `success` or when `ReturnedLength != RequestedLength`; may also add a clear message in output. This should not change the *format* of output, only success/failure and optional wording.
- **`--allow-short`**: If implemented, allow short-read (and optionally empty-read) to be reported as success for exploratory use, while still showing the actual status and lengths in output.

### Read/write API

```go
func (c *Client) ReadArea(ctx context.Context, addr model.Address) (*ReadResult, error)
func (c *Client) WriteArea(ctx context.Context, addr model.Address, data []byte) error

func (c *Client) ReadDB(ctx context.Context, dbNum, offset, size int) (*ReadResult, error)
func (c *Client) WriteDB(ctx context.Context, dbNum, offset int, data []byte) error
func (c *Client) ReadInputs(ctx context.Context, offset, size int) (*ReadResult, error)
func (c *Client) ReadOutputs(ctx context.Context, offset, size int) (*ReadResult, error)
func (c *Client) ReadMerkers(ctx context.Context, offset, size int) (*ReadResult, error)
```

Behavior notes:

- Read methods return `*ReadResult` and a connection/setup `error`. Use `result.OK()` for success; `result.Err()` for a non-success read outcome; `result.Data` for the payload. Empty or short reads are never reported as success.
- ReadArea chunks requests based on negotiated PDU size. Status is derived from requested vs returned length (success, short-read, empty-read) or from S7 return codes (rejected).
- WriteArea writes `len(data)` bytes; `addr.Size` is ignored. Uses WriteVar with optional rate limiting. Invalid address returns `*ValidationError`.

### Range scan API (Phase 2)

Scan an area to discover readable byte ranges. The client must be connected for non-empty ranges.

```go
type RangeProbeRequest struct {
    Area        model.Area
    DBNumber    int
    Start       int
    End         int
    Step        int           // if 0, use ProbeSize
    ProbeSize   int
    Retries     int
    RetryDelay  time.Duration
    Repeat      int
    Interval    time.Duration
    Parallelism int
}

type ReadProbeObservation struct {
    Offset  int
    Request model.Address
    Result  ReadResult
    Stable  *bool
    AllZero *bool
}

type ReadableSpan struct {
    Start   int
    End     int
    Status  ReadStatus
    Stable  *bool
    AllZero *bool
    Notes   []string
}

type RangeProbeSummary struct {
    ReadableSpans     []ReadableSpan
    EmptySpans        []ReadableSpan
    FailedSpans       []ReadableSpan
    InconclusiveSpans []ReadableSpan
}

type RangeProbeResult struct {
    Area     model.Area
    DBNumber int
    Spans    []ReadableSpan
    Probes   []ReadProbeObservation
    Summary  RangeProbeSummary
}

func (c *Client) ProbeReadableRanges(ctx context.Context, req RangeProbeRequest) (*RangeProbeResult, error)
```

Behavior: For each offset in [Start, End) by Step, performs a read of ProbeSize bytes (one probe per offset). Adjacent probes with the same status are merged into spans. Summary aggregates spans by readable/empty/failed/inconclusive. Optional Repeat and Interval set Stable/AllZero; Retries with mixed outcomes yield Inconclusive. Read-only.

### Compare read API (Phase 3)

Perform the same read across multiple rack/slot candidates; detect if the endpoint responds identically (rack/slot-insensitive).

```go
type RackSlot struct {
    Rack int
    Slot int
}

type CompareReadRequest struct {
    Address      string
    Port         int
    Candidates   []RackSlot
    Area         model.Area
    DBNumber     int
    Offset       int
    Size         int
    Timeout      time.Duration
    Parallelism  int  // concurrent connections; 0 or negative = 1 (sequential); results in candidate order
}

type CompareReadCandidate struct {
    Rack   int
    Slot   int
    Result ReadResult
}

type CompareReadResult struct {
    Request             CompareReadRequest
    ByCandidate         []CompareReadCandidate
    RackSlotInsensitive bool
}

func CompareRead(ctx context.Context, req CompareReadRequest) (*CompareReadResult, error)
```

Behavior: For each candidate, creates a client, connects with that rack/slot, performs one read, closes. RackSlotInsensitive is true only when every candidate succeeded and all returned identical data.

### Discovery API

```go
type DiscoverResult struct {
    IP               string
    Port             int
    IsS7             bool
    Rack             int
    Slot             int
    PDUSize          int
    TSAP             string
    Error            string
    AbandonedReason  string // e.g. "max_attempts", "context_canceled" when host was not found
}

func Discover(ctx context.Context, cidr string, opts ...DiscoverOption) ([]DiscoverResult, error)
func WithDiscoverTimeout(ms int) DiscoverOption
func WithDiscoverParallel(n int) DiscoverOption
func WithDiscoverRackSlotRange(rackMin, rackMax, slotMin, slotMax int) DiscoverOption
func WithDiscoverRateLimit(ms int) DiscoverOption
func WithDiscoverSafetyMode(mode SafetyMode) DiscoverOption
func WithDiscoverJitter(ms int) DiscoverOption
func WithDiscoverMaxAttemptsPerHost(n int) DiscoverOption
```

Default discovery settings:

- timeout: 2000 ms
- parallel workers: 10
- rack range: 0..3
- slot range: 0..5

### Rack/Slot Probe API

A host-oriented probe that determines which rack/slot combinations are valid for a specific target IP. Intended for pre-connection topology discovery and troubleshooting.

**Strict mode** (`Strict: true`): only candidates that complete both S7 setup and a benign follow-up query are considered valid (`valid-query`). This avoids false positives from permissive gateways or simulators that accept setup but do not map to a real CPU. Without strict mode, any candidate that reaches setup success (`setup-only`, `valid-connect`, or `valid-query`) is valid.

```go
type SafetyMode string

const (
    SafetyConservative SafetyMode = "conservative"
    SafetyNormal       SafetyMode = "normal"
    SafetyAggressive   SafetyMode = "aggressive"
)

type ProbeStage string

const (
    ProbeStageTCP   ProbeStage = "tcp"
    ProbeStageCOTP  ProbeStage = "cotp"
    ProbeStageSetup ProbeStage = "setup"
    ProbeStageQuery ProbeStage = "query"
)

type ProbeStatus string

const (
    StatusUnreachable   ProbeStatus = "unreachable"
    StatusTCPOnly       ProbeStatus = "tcp-only"
    StatusCOTPOnly      ProbeStatus = "cotp-only"
    StatusSetupOnly     ProbeStatus = "setup-only"
    StatusValidConnect  ProbeStatus = "valid-connect"
    StatusValidQuery    ProbeStatus = "valid-query"
    StatusRejected      ProbeStatus = "rejected"
    StatusTimeout       ProbeStatus = "timeout"
    StatusFlaky         ProbeStatus = "flaky"
)

type ConfirmationKind string

const (
    ConfirmNone     ConfirmationKind = "none"
    ConfirmSZL      ConfirmationKind = "szl"
    ConfirmCPUState ConfirmationKind = "cpu-state"
    ConfirmAny      ConfirmationKind = "any"
)

type Confidence string

const (
    ConfidenceNone Confidence = "none"
    ConfidenceLow  Confidence = "low"
    ConfidenceHigh Confidence = "high"
)

type RackSlotProbeRequest struct {
    Address     string
    Port        int           // default 102
    RackMin     int           // default 0
    RackMax     int           // default 7
    SlotMin     int           // default 0
    SlotMax     int           // default 31
    Timeout     time.Duration // per-attempt timeout; default from SafetyMode
    Parallelism int           // concurrent probes; default from SafetyMode
    DelayMS     int           // delay between attempts in ms; default 0
    StopOnFirst bool          // stop after first valid candidate

    LocalTSAP  *uint16
    RemoteTSAP *uint16

    SafetyMode         SafetyMode      // affects default Timeout, Parallelism, DelayMS
    JitterMS            int            // random [0, JitterMS] ms before each attempt; 0 = no jitter
    MaxAttemptsPerHost  int            // cap (rack,slot) attempts per host; 0 = no limit

    Strict  bool            // if true, only valid-query counts as valid; run follow-up
    Confirm ConfirmationKind // when Strict: szl | cpu-state | any (default when Strict: any)
    Retries int             // reserved for Phase 2
    RetryDelay time.Duration
    StopOnFirstConfirmed bool
}

type RackSlotCandidate struct {
    Rack         int
    Slot         int
    LocalTSAP    uint16
    RemoteTSAP   uint16
    Stage        ProbeStage
    Status       ProbeStatus
    PDUSize      int
    ConfirmedBy  ConfirmationKind
    Confidence   Confidence
    Error        string
}

type RackSlotProbeResult struct {
    Address          string
    Candidates       []RackSlotCandidate
    Valid            []RackSlotCandidate
    SetupAccepted    int    // candidates that reached setup success
    ConfirmedByQuery int    // candidates with valid-query
    Flaky            int    // reserved for Phase 2
    TCPOnly          int
    StoppedEarly     bool   // true when stopped due to MaxAttemptsPerHost before all candidates tried
    StoppedReason    string // e.g. "max_attempts_reached"
}

func ProbeRackSlots(ctx context.Context, req RackSlotProbeRequest) (*RackSlotProbeResult, error)
func DefaultRackSlotProbeRequest(address string) RackSlotProbeRequest
```

Status values:

| Status           | Meaning                                                |
|------------------|--------------------------------------------------------|
| `valid-query`    | S7 setup and follow-up query both succeeded            |
| `valid-connect`  | S7 setup succeeded; follow-up failed or not attempted |
| `setup-only`     | S7 setup succeeded; no follow-up (non-strict only)     |
| `cotp-only`      | COTP ok, S7 setup failed                                |
| `tcp-only`       | TCP ok, COTP failed                                    |
| `unreachable`    | TCP connect failed                                     |
| `rejected`       | Target rejected (S7 error)                             |
| `timeout`        | Any stage timed out                                     |
| `flaky`          | Retries produced mixed results                          |

Confirmation strategies (when `Strict` is true):

- `ConfirmSZL`: one SZL read (module ID).
- `ConfirmCPUState`: SZL CPU state.
- `ConfirmAny`: try SZL module ID, then CPU state, then protection; first success sets `ConfirmedBy`.

Behavior notes:

- **Valid list**: without `Strict`, `Valid` contains candidates with status `setup-only`, `valid-connect`, or `valid-query`. With `Strict`, `Valid` contains only `valid-query`.
- When `Strict` is true and `Confirm` is zero, `Confirm` is set to `ConfirmAny` in `applyProbeDefaults`.
- Remote TSAP is derived from rack/slot (PG convention: `0x03RS`) unless `RemoteTSAP` is set.
- Probe is non-destructive: only connection, setup, and read-only follow-up traffic.

### Identification, diagnostics, and blocks

```go
func (c *Client) Identify(ctx context.Context) (*model.DeviceInfo, error)
func (c *Client) GetCPUState(ctx context.Context) (model.CPUState, error)
func (c *Client) GetProtectionLevel(ctx context.Context) (model.ProtectionLevel, error)
func (c *Client) ReadDiagBuffer(ctx context.Context) (*model.DiagBuffer, error)   // partial parsing; 20-byte stride
func (c *Client) ReadDiagBufferRaw(ctx context.Context) ([]byte, error)          // raw SZL 0x00A0 payload

func (c *Client) ListBlocks(ctx context.Context, bt model.BlockType) ([]model.BlockInfo, error)
func (c *Client) ListAllBlocks(ctx context.Context) ([]model.BlockInfo, error)
func (c *Client) GetBlockInfo(ctx context.Context, bt model.BlockType, num int) (*model.BlockInfo, error)
func (c *Client) UploadBlock(ctx context.Context, bt model.BlockType, num int) (*model.BlockData, error)
```

- **GetBlockInfo**: Best-effort; on transport/protocol failure returns `(nil, err)`; on parse failure after transport success returns partial `BlockInfo` (Type, Number) plus `err`. Invalid block number returns `*ValidationError`.
- **UploadBlock**: Performs a best-effort end-upload cleanup before returning (500ms timeout); cleanup errors are not returned. Invalid block number returns `*ValidationError`.

### Client options

```go
type Option func(*options)

type Logger interface {
    Debug(msg string, args ...interface{})
    Info(msg string, args ...interface{})
    Error(msg string, args ...interface{})
}

func WithPort(port int) Option
func WithRackSlot(rack, slot int) Option
func WithTSAP(local, remote uint16) Option
func WithAutoRackSlot(brute bool) Option
func WithTimeout(t time.Duration) Option
func WithRateLimit(d time.Duration) Option
func WithLogger(l Logger) Option
func WithMaxPDU(size int) Option
```

Defaults:

- port: 102
- rack/slot: 0/1
- timeout: 5s
- max PDU request: 480

## model

```go
import "github.com/otfabric/go-s7comm/model"
```

### Addressing and enums

```go
type Area uint8

const (
    AreaInputs  Area = 0x81
    AreaOutputs Area = 0x82
    AreaMerkers Area = 0x83
    AreaDB      Area = 0x84
    AreaCounter Area = 0x1C
    AreaTimer   Area = 0x1D
)

func (a Area) String() string

type Address struct {
    Area     Area
    DBNumber int
    Start    int
    Size     int
}
```

### Blocks and device metadata

```go
type BlockType uint8
func (b BlockType) String() string

type BlockLang uint8
func (l BlockLang) String() string

type BlockInfo struct { ... }
type BlockData struct { ... }

type DeviceInfo struct { ... }
type ConnectionInfo struct { ... }

type CPUState uint8
func (s CPUState) String() string

type ProtectionLevel uint8
func (p ProtectionLevel) String() string

type DiagEntry struct { ... }
type DiagBuffer struct { ... }

type Fingerprint struct { ... }
type TSAPProfile struct { ... }
```

### Value encoders/decoders

```go
func DecodeBool(data []byte, bit int) bool
func DecodeByte(data []byte) byte
func DecodeWord(data []byte) uint16
func DecodeInt(data []byte) int16
func DecodeDWord(data []byte) uint32
func DecodeDInt(data []byte) int32
func DecodeReal(data []byte) float32
func DecodeString(data []byte) string

func EncodeBool(val bool) []byte
func EncodeByte(val byte) []byte
func EncodeWord(val uint16) []byte
func EncodeInt(val int16) []byte
func EncodeDWord(val uint32) []byte
func EncodeDInt(val int32) []byte
func EncodeReal(val float32) []byte
func EncodeString(val string, maxLen int) []byte
```

Notes:

- Numeric values are big-endian.
- DecodeBool returns false for invalid/negative indexes.
- EncodeString uses S7 string layout and clamps total length to [2, 256].

## transport

Transport uses **github.com/otfabric/go-tpkt** for TPKT framing: `Send` writes the given TPDU payload as one TPKT frame; `Receive` reads the next TPKT frame and returns its payload.

**Invariant:** `Receive()` returns exactly one TPKT payload. In the S7 connection and data flow this payload is always one complete COTP TPDU (e.g. CC, DT). Callers never receive raw S7 bytes directly; S7 payload is carried inside COTP DT `UserData` and must be extracted via `cotp.Decode` and `dec.DT.UserData`.

```go
import "github.com/otfabric/go-s7comm/transport"
```

```go
var ErrConnectionNotEstablished = errors.New("connection not established")

type Conn struct { ... }

type Tracer interface {
    Trace(direction string, data []byte)
}

func New(conn net.Conn, timeout time.Duration) *Conn
func (c *Conn) SetTracer(t Tracer)
func (c *Conn) Send(data []byte) error       // payload = TPDU (e.g. COTP); TPKT framing applied internally
func (c *Conn) SendContext(ctx context.Context, data []byte) error
func (c *Conn) Receive() ([]byte, error)    // returns TPKT payload (e.g. COTP bytes)
func (c *Conn) ReceiveContext(ctx context.Context) ([]byte, error)
func (c *Conn) Close() error
func (c *Conn) LocalAddr() net.Addr
func (c *Conn) RemoteAddr() net.Addr
```

## wire

```go
import "github.com/otfabric/go-s7comm/wire"
```

### TPKT and COTP

TPKT framing is provided by **github.com/otfabric/go-tpkt**: the transport layer sends and receives TPDU payloads as TPKT frames. COTP encoding uses **github.com/otfabric/go-cotp** (import path `github.com/otfabric/go-cotp`); the wire package exposes S7-oriented helpers:

```go
func BuildTSAP(connType, rack, slot int) uint16
func EncodeCOTPCR(localTSAP, remoteTSAP uint16) ([]byte, error)
func EncodeCOTPDT(s7Payload []byte) ([]byte, error)
```

**Ownership and usage:**

- These functions return **COTP payload only** (one complete COTP TPDU). The caller must send that payload with `transport.Send(...)`; the transport layer adds TPKT framing. **Do not** wrap the returned bytes in TPKT again when using this transport—doing so would double-frame and break the protocol.
- `BuildTSAP`: S7 TSAP from connection type (1=PG, 2=OP, 3=S7Basic), rack, slot.
- `EncodeCOTPCR`: COTP Connection Request for the given TSAPs. Send the returned bytes via `transport.Send`.
- `EncodeCOTPDT`: COTP Data TPDU with EOT and the given S7 payload. Send the returned bytes via `transport.Send`.

Decoding of received payloads is done via `cotp.Decode(payload)` from go-cotp (e.g. check `dec.Type`, `dec.CC`, `dec.DT.UserData`).

### S7 headers

```go
func EncodeS7Header(rosctr ROSCTR, pduRef uint16, paramLen, dataLen int) []byte
func ParseS7Header(data []byte) (*S7Header, []byte, error)
```

`ROSCTR` is a wire package type (e.g. `ROSCTRJob`, `ROSCTRAckData`).

### PDUs

```go
func EncodeSetupCommRequest(pduRef uint16, maxAmqCalling, maxAmqCalled, pduSize int) []byte
func ParseSetupCommResponse(data []byte) (*SetupCommResponse, error)

func EncodeS7Any(addr S7AnyAddress) []byte
func EncodeReadVarRequest(pduRef uint16, addrs []S7AnyAddress) []byte
func ParseReadVarResponse(param, data []byte) ([]ReadVarItem, error)
func EncodeWriteVarRequest(pduRef uint16, addr S7AnyAddress, value []byte) []byte
func ParseWriteVarResponse(param, data []byte) error

func EncodeSZLRequest(pduRef, szlID, szlIndex uint16) []byte
func ParseSZLResponse(data []byte) (*SZLResponse, error)

func EncodeBlockListRequest(pduRef uint16, blockType byte) []byte
func ParseBlockListResponse(szlData []byte) ([]BlockListEntry, error)
func EncodeStartUploadRequest(pduRef uint16, blockType byte, blockNum int) []byte
func ParseStartUploadResponse(param []byte) (string, error)
func EncodeUploadRequest(pduRef uint16, sessionID string) []byte
func EncodeEndUploadRequest(pduRef uint16, sessionID string) []byte
func ParseUploadResponse(param, data []byte) (*UploadChunk, error)
```

### Inspection and errors

```go
func InspectFrame(frame []byte) (*FrameSummary, error)

func NewS7Error(class, code byte) *S7Error
func ReturnCodeError(code byte) error
```

Key sentinel errors include short/invalid S7 headers and payload length mismatches. TPKT and COTP errors come from go-tpkt and go-cotp when used for framing and decode.
