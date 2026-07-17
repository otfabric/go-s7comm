package client

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/wire"
)

// jitterRNG returns a private RNG for jitter in this call (avoids package-global rand for reproducibility).
func jitterRNG() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// SafetyMode tunes discovery/probe for OT network safety (conservative) vs speed (aggressive).
type SafetyMode string

const (
	SafetyConservative SafetyMode = "conservative" // longer timeouts, lower parallelism, optional delay/jitter
	SafetyNormal       SafetyMode = "normal"       // default
	SafetyAggressive   SafetyMode = "aggressive"   // shorter timeouts, higher parallelism
)

// isProbeTimeout reports whether err is a timeout (context deadline or net timeout).
// Used to set StatusTimeout instead of generic transport/unreachable in probe flows.
func isProbeTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// ProbeStage indicates the last stage reached during a rack/slot probe.
type ProbeStage string

const (
	ProbeStageTCP   ProbeStage = "tcp"
	ProbeStageCOTP  ProbeStage = "cotp"
	ProbeStageSetup ProbeStage = "setup"
	ProbeStageQuery ProbeStage = "query"
)

// ProbeStatus is the classification of a single rack/slot probe attempt.
type ProbeStatus string

const (
	StatusUnreachable  ProbeStatus = "unreachable"   // TCP connect failed
	StatusTCPOnly      ProbeStatus = "tcp-only"      // TCP ok, COTP failed
	StatusCOTPOnly     ProbeStatus = "cotp-only"     // COTP ok, S7 setup failed
	StatusSetupOnly    ProbeStatus = "setup-only"    // Setup ok, no follow-up (non-strict)
	StatusValidConnect ProbeStatus = "valid-connect" // Setup ok, follow-up failed or not attempted
	StatusValidQuery   ProbeStatus = "valid-query"   // Setup ok and follow-up query succeeded
	StatusRejected     ProbeStatus = "rejected"      // Target explicitly rejected (S7 error)
	StatusTimeout      ProbeStatus = "timeout"       // Any stage timed out
	StatusFlaky        ProbeStatus = "flaky"         // Retries produced mixed results
)

// ConfirmationKind specifies how to confirm a rack/slot after S7 setup (strict mode).
type ConfirmationKind string

const (
	ConfirmNone     ConfirmationKind = "none"      // No follow-up (non-strict)
	ConfirmSZL      ConfirmationKind = "szl"       // SZL module/component ID
	ConfirmCPUState ConfirmationKind = "cpu-state" // SZL CPU state
	ConfirmAny      ConfirmationKind = "any"       // Try SZL, then CPU state, then protection
)

// Confidence indicates how strongly a result is confirmed.
type Confidence string

const (
	ConfidenceNone Confidence = "none"
	ConfidenceLow  Confidence = "low"
	ConfidenceHigh Confidence = "high"
)

// RackSlotProbeRequest configures a host-oriented rack/slot probe.
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
	StopOnFirst bool          // stop after first valid candidate (any valid in non-strict)

	// Optional manual TSAP override (bypasses rack/slot-derived TSAP).
	LocalTSAP  *uint16
	RemoteTSAP *uint16

	// SafetyMode affects default Timeout, Parallelism, DelayMS when not explicitly set.
	SafetyMode SafetyMode
	// JitterMS adds random [0, JitterMS] ms before each attempt to spread load. 0 = no jitter.
	JitterMS int
	// MaxAttemptsPerHost caps total (rack,slot) attempts for this host; 0 = no limit.
	MaxAttemptsPerHost int

	// Strict mode: only count valid-query as valid; run follow-up confirmation.
	Strict bool
	// Confirm selects the follow-up strategy when Strict is true. Default when Strict is true: ConfirmAny.
	Confirm ConfirmationKind
	// Retries: number of attempts per candidate (Phase 2; reserved).
	Retries int
	// RetryDelay: delay between retries (Phase 2; reserved).
	RetryDelay time.Duration
	// StopOnFirstConfirmed: in strict mode, stop after first valid-query (Phase 2; reserved).
	StopOnFirstConfirmed bool
}

// RackSlotCandidate holds the probe result for a single rack/slot pair.
type RackSlotCandidate struct {
	Rack       int
	Slot       int
	LocalTSAP  uint16
	RemoteTSAP uint16

	Stage       ProbeStage
	Status      ProbeStatus
	PDUSize     int
	ConfirmedBy ConfirmationKind
	Confidence  Confidence
	Error       string
}

// RackSlotProbeResult holds all candidates, the subset that are valid, and a summary.
type RackSlotProbeResult struct {
	Address    string
	Candidates []RackSlotCandidate
	Valid      []RackSlotCandidate

	// Summary counts (honest reporting).
	SetupAccepted    int // candidates that reached setup success (setup-only, valid-connect, valid-query)
	ConfirmedByQuery int // candidates with valid-query (strict validity)
	Flaky            int // reserved for Phase 2
	TCPOnly          int // tcp-only count

	// StoppedEarly is true when probing stopped due to MaxAttemptsPerHost before all candidates were tried.
	StoppedEarly  bool
	StoppedReason string // e.g. "max_attempts_reached"
}

// ProbeRackSlots probes a single target IP for valid rack/slot combinations.
// It is non-destructive: only connection/setup and optionally a benign SZL read (strict mode) are used.
// Without Strict, "valid" means setup-only or valid-connect or valid-query.
// With Strict, "valid" means only valid-query (setup accepted and confirmed by follow-up).
func ProbeRackSlots(ctx context.Context, req RackSlotProbeRequest) (*RackSlotProbeResult, error) {
	applyProbeDefaults(&req)
	if err := validateRackSlotProbeRequest(req); err != nil {
		return nil, err
	}

	type job struct {
		rack int
		slot int
	}

	var jobs []job
	for rack := req.RackMin; rack <= req.RackMax; rack++ {
		for slot := req.SlotMin; slot <= req.SlotMax; slot++ {
			jobs = append(jobs, job{rack, slot})
		}
	}

	maxJobs := len(jobs)
	if req.MaxAttemptsPerHost > 0 && maxJobs > req.MaxAttemptsPerHost {
		maxJobs = req.MaxAttemptsPerHost
	}

	result := &RackSlotProbeResult{Address: req.Address}
	if maxJobs < len(jobs) {
		result.StoppedEarly = true
		result.StoppedReason = "max_attempts_reached"
	}
	candidates := make([]RackSlotCandidate, len(jobs))

	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var jitterRng *rand.Rand
	if req.JitterMS > 0 {
		jitterRng = jitterRNG()
	}

	sem := make(chan struct{}, req.Parallelism)
	var wg sync.WaitGroup

outer:
	for i := 0; i < maxJobs; i++ {
		j := jobs[i]
		select {
		case <-probeCtx.Done():
			break outer
		default:
		}

		if req.JitterMS > 0 && jitterRng != nil {
			jitter := time.Duration(jitterRng.Intn(req.JitterMS+1)) * time.Millisecond
			if jitter > 0 {
				select {
				case <-probeCtx.Done():
					break outer
				case <-time.After(jitter):
				}
			}
		}
		if req.DelayMS > 0 && i > 0 {
			select {
			case <-probeCtx.Done():
				break outer
			case <-time.After(time.Duration(req.DelayMS) * time.Millisecond):
			}
		}

		idx := i
		rack := j.rack
		slot := j.slot

		select {
		case <-probeCtx.Done():
			break outer
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			c := probeOne(probeCtx, req, rack, slot)
			candidates[idx] = c

			// Stop on first valid: non-strict = any setup success; strict = first valid-query
			validForStop := (c.Status == StatusSetupOnly || c.Status == StatusValidConnect || c.Status == StatusValidQuery) && !req.Strict
			if req.Strict {
				validForStop = c.Status == StatusValidQuery
			}
			if validForStop && (req.StopOnFirst || req.StopOnFirstConfirmed) {
				cancel()
			}
		}()
	}

	wg.Wait()

	result.Candidates = candidates

	// Valid list: non-strict = setup-only | valid-connect | valid-query; strict = only valid-query
	for _, c := range candidates {
		if req.Strict {
			if c.Status == StatusValidQuery {
				result.Valid = append(result.Valid, c)
			}
		} else {
			if c.Status == StatusSetupOnly || c.Status == StatusValidConnect || c.Status == StatusValidQuery {
				result.Valid = append(result.Valid, c)
			}
		}
	}

	// Summary counts
	for _, c := range candidates {
		if c.Status == StatusSetupOnly || c.Status == StatusValidConnect || c.Status == StatusValidQuery {
			result.SetupAccepted++
		}
		if c.Status == StatusValidQuery {
			result.ConfirmedByQuery++
		}
		if c.Status == StatusTCPOnly {
			result.TCPOnly++
		}
		if c.Status == StatusFlaky {
			result.Flaky++
		}
	}

	// Only surface outer context cancellation; stop-on-first uses cancel() and we return result, nil
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

// DefaultRackSlotProbeRequest returns a RackSlotProbeRequest populated with
// the recommended defaults for a full rack/slot scan of a single target.
func DefaultRackSlotProbeRequest(address string) RackSlotProbeRequest {
	return RackSlotProbeRequest{
		Address:     address,
		Port:        102,
		RackMin:     0,
		RackMax:     7,
		SlotMin:     0,
		SlotMax:     31,
		Timeout:     2 * time.Second,
		Parallelism: 4,
	}
}

func applyProbeDefaults(req *RackSlotProbeRequest) {
	if req.Port == 0 {
		req.Port = 102
	}
	mode := req.SafetyMode
	if mode == "" {
		mode = SafetyNormal
	}
	if req.Timeout == 0 {
		switch mode {
		case SafetyConservative:
			req.Timeout = 5 * time.Second
		case SafetyAggressive:
			req.Timeout = time.Second
		default:
			req.Timeout = 2 * time.Second
		}
	}
	if req.Parallelism < 1 {
		switch mode {
		case SafetyConservative:
			req.Parallelism = 2
		case SafetyAggressive:
			req.Parallelism = 8
		default:
			req.Parallelism = 4
		}
	}
	if req.DelayMS == 0 && mode == SafetyConservative {
		req.DelayMS = 50
	}
	if req.Strict && req.Confirm == ConfirmNone {
		req.Confirm = ConfirmAny
	}
}

// runFollowUp sends a benign S7 request (SZL) on the existing conn and returns whether it succeeded.
// pduRef is used for the request header. On success, confirmedBy is set to the strategy that worked.
func runFollowUp(ctx context.Context, conn *cotp.Conn, pduRef uint16, strategy ConfirmationKind) (ok bool, confirmedBy ConfirmationKind, errMsg string) {
	trySZL := func(szlID uint16) (bool, string) {
		req := wire.EncodeSZLRequest(pduRef, szlID, 0)
		if err := conn.WriteTSDU(ctx, req); err != nil {
			return false, err.Error()
		}
		s7Data, err := conn.ReadTSDU(ctx)
		if err != nil {
			return false, err.Error()
		}
		hdr, rest, err := wire.ParseS7Header(s7Data)
		if err != nil {
			return false, err.Error()
		}
		if hdr.PDURef != pduRef {
			return false, fmt.Sprintf("PDU ref mismatch: expected %d got %d", pduRef, hdr.PDURef)
		}
		if hdr.ErrorClass != 0 || hdr.ErrorCode != 0 {
			return false, fmt.Sprintf("S7 error 0x%02X/0x%02X", hdr.ErrorClass, hdr.ErrorCode)
		}
		need := int(hdr.ParamLength) + int(hdr.DataLength)
		if len(rest) < need {
			return false, "short S7 payload"
		}
		dataSlice := rest[hdr.ParamLength : hdr.ParamLength+hdr.DataLength]
		if _, err := wire.ParseSZLResponse(dataSlice); err != nil {
			return false, err.Error()
		}
		return true, ""
	}

	switch strategy {
	case ConfirmSZL:
		ok, errMsg = trySZL(wire.SZLModuleID)
		if ok {
			return true, ConfirmSZL, ""
		}
		return false, ConfirmNone, errMsg
	case ConfirmCPUState:
		ok, errMsg = trySZL(wire.SZLCPUState)
		if ok {
			return true, ConfirmCPUState, ""
		}
		return false, ConfirmNone, errMsg
	case ConfirmAny:
		if ok, _ := trySZL(wire.SZLModuleID); ok {
			return true, ConfirmSZL, ""
		}
		if ok, _ := trySZL(wire.SZLCPUState); ok {
			return true, ConfirmCPUState, ""
		}
		if ok, errMsg = trySZL(wire.SZLProtectionInfo); ok {
			return true, ConfirmAny, ""
		}
		return false, ConfirmNone, errMsg
	default:
		return false, ConfirmNone, "no confirmation strategy"
	}
}

func probeOne(ctx context.Context, req RackSlotProbeRequest, rack, slot int) RackSlotCandidate {
	c := RackSlotCandidate{Rack: rack, Slot: slot, Confidence: ConfidenceNone}

	addr := net.JoinHostPort(req.Address, fmt.Sprint(req.Port))

	var localTSAP, remoteTSAP uint16
	if req.LocalTSAP != nil {
		localTSAP = *req.LocalTSAP
	} else {
		var err error
		localTSAP, err = wire.BuildTSAP(1, 0, 0)
		if err != nil {
			c.Error = err.Error()
			c.Status = StatusRejected
			return c
		}
	}
	if req.RemoteTSAP != nil {
		remoteTSAP = *req.RemoteTSAP
	} else {
		var err error
		remoteTSAP, err = wire.BuildTSAP(3, rack, slot)
		if err != nil {
			c.Error = err.Error()
			c.Status = StatusRejected
			return c
		}
	}
	c.LocalTSAP = localTSAP
	c.RemoteTSAP = remoteTSAP

	raw, err := dialTCP(ctx, addr, req.Timeout)
	if err != nil {
		c.Stage = ProbeStageTCP
		if isProbeTimeout(err) {
			c.Status = StatusTimeout
		} else {
			c.Status = StatusUnreachable
		}
		c.Error = err.Error()
		return c
	}
	conn, err := connectCOTP(ctx, raw, req.Timeout, localTSAP, remoteTSAP)
	if err != nil {
		c.Stage = ProbeStageCOTP
		if isProbeTimeout(err) {
			c.Status = StatusTimeout
		} else {
			c.Status = StatusTCPOnly
		}
		c.Error = err.Error()
		return c
	}
	defer func() { _ = closeCOTP(conn) }()
	setupRef := uint16(1)
	setup, err := performS7Setup(ctx, conn, setupRef, 1, 1, 480)
	if err != nil {
		c.Stage = ProbeStageSetup
		if isProbeTimeout(err) {
			c.Status = StatusTimeout
		} else {
			var s7Err *wire.S7Error
			if errors.As(err, &s7Err) {
				c.Status = StatusRejected
			} else {
				c.Status = StatusCOTPOnly
			}
		}
		c.Error = err.Error()
		return c
	}
	c.PDUSize = setup.PDUSize

	if !req.Strict {
		c.Stage = ProbeStageSetup
		c.Status = StatusSetupOnly
		c.Confidence = ConfidenceLow
		return c
	}

	c.Stage = ProbeStageQuery
	followUpRef := setupRef + 1
	ok, confirmedBy, errStr := runFollowUp(ctx, conn, followUpRef, req.Confirm)
	if ok {
		c.Status = StatusValidQuery
		c.ConfirmedBy = confirmedBy
		c.Confidence = ConfidenceHigh
	} else {
		c.Status = StatusValidConnect
		c.Confidence = ConfidenceLow
		if errStr != "" {
			c.Error = "follow-up failed: " + errStr
		}
	}
	return c
}
