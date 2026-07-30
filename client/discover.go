// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"
)

// DiscoverResult holds a single discovery result.
type DiscoverResult struct {
	IP      string
	Port    int
	IsS7    bool
	Rack    int
	Slot    int
	PDUSize int
	TSAP    string
	Error   string
	// AbandonedReason is set when the host was not found and we stopped for a known reason (e.g. "max_attempts", "context_canceled").
	AbandonedReason string
}

// DiscoverOption configures discovery
type DiscoverOption func(*discoverOpts)

type discoverOpts struct {
	timeout            int
	parallel           int
	rackMin            int
	rackMax            int
	slotMin            int
	slotMax            int
	rateMS             int
	safetyMode         SafetyMode
	jitterMS           int
	maxAttemptsPerHost int
}

// WithDiscoverTimeout sets per-host timeout in ms
func WithDiscoverTimeout(ms int) DiscoverOption {
	return func(o *discoverOpts) { o.timeout = ms }
}

// WithDiscoverParallel sets parallel probe count
func WithDiscoverParallel(n int) DiscoverOption {
	return func(o *discoverOpts) { o.parallel = n }
}

// WithDiscoverRackSlotRange sets rack and slot probe ranges (inclusive).
func WithDiscoverRackSlotRange(rackMin, rackMax, slotMin, slotMax int) DiscoverOption {
	return func(o *discoverOpts) {
		o.rackMin = rackMin
		o.rackMax = rackMax
		o.slotMin = slotMin
		o.slotMax = slotMax
	}
}

// WithDiscoverRateLimit sets delay between rack/slot probes in milliseconds.
func WithDiscoverRateLimit(ms int) DiscoverOption {
	return func(o *discoverOpts) { o.rateMS = ms }
}

// WithDiscoverSafetyMode sets conservative/normal/aggressive; affects default timeout and parallel when not overridden.
func WithDiscoverSafetyMode(mode SafetyMode) DiscoverOption {
	return func(o *discoverOpts) { o.safetyMode = mode }
}

// WithDiscoverJitter sets random [0, ms] ms delay before each host probe to spread load. 0 = no jitter.
func WithDiscoverJitter(ms int) DiscoverOption {
	return func(o *discoverOpts) { o.jitterMS = ms }
}

// WithDiscoverMaxAttemptsPerHost caps connection attempts per IP (rack/slot tries). 0 = no limit.
func WithDiscoverMaxAttemptsPerHost(n int) DiscoverOption {
	return func(o *discoverOpts) { o.maxAttemptsPerHost = n }
}

// Discover scans an IPv4 CIDR range for S7 devices. Only IPv4 CIDRs are supported;
// IPv6 is rejected. Results are returned in the same order as the CIDR addresses.
func Discover(ctx context.Context, cidr string, opts ...DiscoverOption) ([]DiscoverResult, error) {
	dOpts := &discoverOpts{
		timeout:  2000,
		parallel: 10,
		rackMin:  0,
		rackMax:  3,
		slotMin:  0,
		slotMax:  5,
		rateMS:   0,
	}
	for _, opt := range opts {
		opt(dOpts)
	}
	// Apply safety presets when defaults are still in use
	if dOpts.safetyMode == "" {
		dOpts.safetyMode = SafetyNormal
	}
	switch dOpts.safetyMode {
	case SafetyConservative:
		if dOpts.timeout == 2000 {
			dOpts.timeout = 5000
		}
		if dOpts.parallel == 10 {
			dOpts.parallel = 3
		}
		if dOpts.rateMS == 0 {
			dOpts.rateMS = 50
		}
	case SafetyAggressive:
		if dOpts.timeout == 2000 {
			dOpts.timeout = 1000
		}
		if dOpts.parallel == 10 {
			dOpts.parallel = 15
		}
	}
	if dOpts.rackMin > dOpts.rackMax {
		return nil, &ValidationError{Message: fmt.Sprintf("discover: rack min (%d) must be <= rack max (%d)", dOpts.rackMin, dOpts.rackMax)}
	}
	if dOpts.slotMin > dOpts.slotMax {
		return nil, &ValidationError{Message: fmt.Sprintf("discover: slot min (%d) must be <= slot max (%d)", dOpts.slotMin, dOpts.slotMax)}
	}
	if dOpts.jitterMS < 0 {
		return nil, &ValidationError{Message: fmt.Sprintf("discover: jitter ms must be >= 0, got %d", dOpts.jitterMS)}
	}
	if dOpts.maxAttemptsPerHost < 0 {
		return nil, &ValidationError{Message: fmt.Sprintf("discover: max attempts per host must be >= 0, got %d", dOpts.maxAttemptsPerHost)}
	}

	// Two-pass: first pass counts IPs for deterministic result order without materializing the full list.
	var count int
	err := streamCIDR(ctx, cidr, func(ip string) bool {
		select {
		case <-ctx.Done():
			return false
		default:
			count++
			return true
		}
	})
	if err != nil {
		return nil, err
	}

	results := make([]DiscoverResult, count)
	if count == 0 {
		return results, nil
	}

	if dOpts.parallel < 1 {
		dOpts.parallel = 1
	}

	type job struct {
		i  int
		ip string
	}
	type indexed struct {
		i int
		r DiscoverResult
	}
	jobs := make(chan job, dOpts.parallel*2)
	out := make(chan indexed, count)
	var wg sync.WaitGroup

	for i := 0; i < dOpts.parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				r := probeS7(ctx, j.ip, 102, dOpts)
				select {
				case <-ctx.Done():
					return
				case out <- indexed{j.i, r}:
				}
			}
		}()
	}

	go func() {
		idx := 0
		_ = streamCIDR(ctx, cidr, func(ip string) bool {
			select {
			case <-ctx.Done():
				return false
			case jobs <- job{idx, ip}:
				idx++
				return true
			}
		})
		close(jobs)
	}()

	go func() {
		wg.Wait()
		close(out)
	}()

	for res := range out {
		results[res.i] = res.r
	}

	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}

// maxDiscoveryHostBits is the maximum CIDR host bits allowed for discovery (e.g. /12 = 2^20 hosts).
// Larger ranges are rejected to avoid overflow and operational risk.
const maxDiscoveryHostBits = 20

// streamCIDR yields IPv4 host addresses from the CIDR one at a time via send. It does not
// materialize the full list, so memory use is constant in CIDR size. send is called for each
// address; return false to stop. For /24 and larger, network and broadcast addresses are
// skipped (same as expandCIDR). Returns an error for invalid or IPv6 CIDRs. CIDRs with
// more than maxDiscoveryHostBits host bits (e.g. /0–/11) are rejected.
func streamCIDR(ctx context.Context, cidr string, send func(ip string) bool) error {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return &ValidationError{Message: err.Error()}
	}
	if ipnet.IP.To4() == nil {
		return &ValidationError{Message: fmt.Sprintf("discovery is IPv4-only; got IPv6 CIDR %s", cidr)}
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return &ValidationError{Message: fmt.Sprintf("expected IPv4 mask, got %d bits", bits)}
	}
	hostBits := bits - ones
	if hostBits > maxDiscoveryHostBits {
		return &ValidationError{Message: fmt.Sprintf("CIDR too large for discovery (max /%d): %s", 32-maxDiscoveryHostBits, cidr)}
	}
	total := 1 << hostBits
	skipEnds := total > 2
	ip := make(net.IP, 4)
	copy(ip, ipnet.IP.Mask(ipnet.Mask))
	for idx := 0; idx < total; idx++ {
		if skipEnds && (idx == 0 || idx == total-1) {
			if idx == 0 {
				incIP(ip)
			}
			continue
		}
		if !send(ip.String()) {
			return ctx.Err()
		}
		incIP(ip)
	}
	return nil
}

// expandCIDR expands an IPv4 CIDR to a list of host addresses. Returns an error for IPv6 CIDRs.
// Internal/legacy helper for tests; production code should use streamCIDR to avoid materializing
// the full IP list. Prefer streamCIDR for large CIDRs.
func expandCIDR(cidr string) ([]string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}
	if ipnet.IP.To4() == nil {
		return nil, &ValidationError{Message: fmt.Sprintf("discovery is IPv4-only; got IPv6 CIDR %s", cidr)}
	}

	var ips []string
	for ip := ipnet.IP.Mask(ipnet.Mask); ipnet.Contains(ip); incIP(ip) {
		ips = append(ips, ip.String())
	}

	// Remove network and broadcast for /24 and larger
	if len(ips) > 2 {
		ips = ips[1 : len(ips)-1]
	}
	return ips, nil
}

func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func probeS7(ctx context.Context, ip string, port int, opts *discoverOpts) DiscoverResult {
	result := DiscoverResult{IP: ip, Port: port}
	attempts := 0

	var jitterRng *rand.Rand
	if opts.jitterMS > 0 {
		jitterRng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	for rack := opts.rackMin; rack <= opts.rackMax; rack++ {
		for slot := opts.slotMin; slot <= opts.slotMax; slot++ {
			if opts.maxAttemptsPerHost > 0 && attempts >= opts.maxAttemptsPerHost {
				result.AbandonedReason = "max_attempts"
				result.Error = "max attempts per host reached"
				return result
			}
			if err := ctx.Err(); err != nil {
				result.Error = err.Error()
				result.AbandonedReason = "context_canceled"
				return result
			}
			if opts.jitterMS > 0 && jitterRng != nil {
				jitter := time.Duration(jitterRng.Intn(opts.jitterMS+1)) * time.Millisecond
				if jitter > 0 {
					select {
					case <-ctx.Done():
						result.Error = ctx.Err().Error()
						result.AbandonedReason = "context_canceled"
						return result
					case <-time.After(jitter):
					}
				}
			}
			if opts.rateMS > 0 {
				select {
				case <-ctx.Done():
					result.Error = ctx.Err().Error()
					result.AbandonedReason = "context_canceled"
					return result
				case <-time.After(time.Duration(opts.rateMS) * time.Millisecond):
				}
			}

			attempts++

			c := New(ip,
				WithPort(port),
				WithRackSlot(rack, slot),
				WithTimeout(time.Duration(opts.timeout)*time.Millisecond),
			)

			if err := c.Connect(ctx); err != nil {
				continue
			}

			info := c.ConnectionInfo()
			_ = c.Close()

			result.IsS7 = true
			result.Rack = info.Rack
			result.Slot = info.Slot
			result.PDUSize = info.PDUSize
			result.TSAP = fmt.Sprintf("0x%04X", info.RemoteTSAP)
			return result
		}
	}

	result.Error = "no valid COTP+S7 setup for tested rack/slot range"
	return result
}

// isPortOpen attempts a TCP dial; returns nil if the port is open, otherwise the real error
// (e.g. context deadline, connection refused). Used by tests; discovery uses Connect directly.
func isPortOpen(ctx context.Context, ip string, port int, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(ip, fmt.Sprint(port)))
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}
