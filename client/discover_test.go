// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"strings"
	"testing"
)

func TestExpandCIDR(t *testing.T) {
	ips, err := expandCIDR("192.168.1.0/30")
	if err != nil {
		t.Fatalf("expandCIDR error: %v", err)
	}
	if len(ips) != 2 {
		t.Fatalf("expected 2 host addresses, got %d", len(ips))
	}
}

func TestExpandCIDR32(t *testing.T) {
	ips, err := expandCIDR("127.0.0.1/32")
	if err != nil {
		t.Fatalf("expandCIDR error: %v", err)
	}
	if len(ips) != 1 || ips[0] != "127.0.0.1" {
		t.Fatalf("expected single host 127.0.0.1, got %v", ips)
	}
}

func TestExpandCIDRInvalid(t *testing.T) {
	if _, err := expandCIDR("bad-cidr"); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
}

func TestExpandCIDR31(t *testing.T) {
	ips, err := expandCIDR("192.168.1.0/31")
	if err != nil {
		t.Fatalf("expandCIDR: %v", err)
	}
	if len(ips) != 2 {
		t.Fatalf("expected 2 host addresses for /31, got %d", len(ips))
	}
}

func TestExpandCIDRIPv6Rejected(t *testing.T) {
	_, err := expandCIDR("2001:db8::/32")
	if err == nil {
		t.Fatal("expected IPv6 CIDR to be rejected")
	}
	if !strings.Contains(err.Error(), "IPv4-only") {
		t.Errorf("expected IPv4-only message, got %q", err.Error())
	}
}

func TestStreamCIDRRejectsTooLargeCIDR(t *testing.T) {
	ctx := context.Background()
	// /11 has 21 host bits > maxDiscoveryHostBits (20)
	err := streamCIDR(ctx, "10.0.0.0/11", func(string) bool { return true })
	if err == nil {
		t.Fatal("expected streamCIDR to reject CIDR with >20 host bits")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got %q", err.Error())
	}
}

func TestDiscoverCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Discover(ctx, "192.168.1.0/30")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestDiscoverMaxAttemptsAndAbandonedReason(t *testing.T) {
	ctx := context.Background()
	// Port 1 is closed; 2 rack/slot pairs, max 1 attempt → abandon after 1 try with AbandonedReason.
	results, err := Discover(ctx, "127.0.0.1/32",
		WithDiscoverRackSlotRange(0, 0, 0, 1),
		WithDiscoverMaxAttemptsPerHost(1),
		WithDiscoverTimeout(50),
	)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.AbandonedReason != "max_attempts" {
		t.Errorf("AbandonedReason = %q, want max_attempts", r.AbandonedReason)
	}
	if r.IsS7 {
		t.Error("expected IsS7 false for closed port")
	}
}

func TestDiscoverValidationRackSlotRange(t *testing.T) {
	ctx := context.Background()
	_, err := Discover(ctx, "127.0.0.1/32", WithDiscoverRackSlotRange(2, 0, 0, 1))
	if err == nil {
		t.Fatal("expected error when rack min > rack max")
	}
	if !strings.Contains(err.Error(), "rack min") {
		t.Errorf("expected rack min/max message, got %q", err.Error())
	}
	_, err = Discover(ctx, "127.0.0.1/32", WithDiscoverRackSlotRange(0, 1, 3, 1))
	if err == nil {
		t.Fatal("expected error when slot min > slot max")
	}
	if !strings.Contains(err.Error(), "slot min") {
		t.Errorf("expected slot min/max message, got %q", err.Error())
	}
}

func TestStreamCIDRMatchesExpandCIDR(t *testing.T) {
	ctx := context.Background()
	for _, cidr := range []string{"192.168.1.0/30", "127.0.0.1/32", "192.168.1.0/31"} {
		var streamed []string
		err := streamCIDR(ctx, cidr, func(ip string) bool {
			streamed = append(streamed, ip)
			return true
		})
		if err != nil {
			t.Fatalf("streamCIDR %s: %v", cidr, err)
		}
		expanded, err := expandCIDR(cidr)
		if err != nil {
			t.Fatalf("expandCIDR %s: %v", cidr, err)
		}
		if len(streamed) != len(expanded) {
			t.Errorf("cidr %s: streamed %d, expanded %d", cidr, len(streamed), len(expanded))
		}
		for i := range streamed {
			if i >= len(expanded) || streamed[i] != expanded[i] {
				t.Errorf("cidr %s: at %d streamed %q, expanded %q", cidr, i, streamed[i], expanded[i])
				break
			}
		}
	}
}

func TestDiscoverOptions(t *testing.T) {
	// Ensure option constructors don't panic and Discover accepts them
	opts := []DiscoverOption{
		WithDiscoverTimeout(1000),
		WithDiscoverParallel(5),
		WithDiscoverRackSlotRange(0, 2, 0, 3),
		WithDiscoverRateLimit(50),
	}
	ctx := context.Background()
	// Use /32 to minimize work; may still try to connect
	results, err := Discover(ctx, "127.0.0.1/32", opts...)
	if err != nil {
		t.Fatalf("Discover with options: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for /32, got %d", len(results))
	}
}

func BenchmarkDiscover(b *testing.B) {
	ctx := context.Background()
	opts := []DiscoverOption{
		WithDiscoverTimeout(5),
		WithDiscoverParallel(4),
		WithDiscoverRackSlotRange(0, 0, 0, 0),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Discover(ctx, "127.0.0.1/32", opts...)
	}
}
