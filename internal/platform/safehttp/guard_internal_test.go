package safehttp

import (
	"errors"
	"net/netip"
	"testing"
)

func TestCheckAddr(t *testing.T) {
	t.Parallel()
	g := guard{blocked: blockedPrefixes(), ports: []int{443}}
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
		{"127.0.0.1", true},
		{"127.255.255.254", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // cloud metadata
		{"100.100.100.200", true}, // Alibaba metadata (CGNAT range)
		{"0.0.0.0", true},
		{"0.1.2.3", true},
		{"224.0.0.1", true},
		{"255.255.255.255", true},
		{"198.18.0.1", true},
		{"::1", true},
		{"::", true},
		{"::ffff:127.0.0.1", true}, // IPv4-mapped loopback
		{"::ffff:10.0.0.1", true},
		{"::127.0.0.1", true},    // IPv4-compatible
		{"64:ff9b::a00:1", true}, // NAT64 of 10.0.0.1
		{"2002:a00:1::", true},   // 6to4 of 10.0.0.1
		{"2001:0:a00:1::", true}, // Teredo
		{"fc00::1", true},
		{"fd00:ec2::254", true},
		{"fe80::1", true},
		{"fe80::1%eth0", true},
		{"ff02::1", true},
		{"2001:db8::1", true},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			t.Parallel()
			err := g.checkAddr(netip.MustParseAddr(tt.ip))
			if (err != nil) != tt.blocked {
				t.Fatalf("checkAddr(%s) = %v, blocked want %v", tt.ip, err, tt.blocked)
			}
			if err != nil && !errors.Is(err, ErrBlockedAddress) {
				t.Fatalf("wrong error %v", err)
			}
		})
	}
}

func TestControl(t *testing.T) {
	t.Parallel()
	g := guard{blocked: blockedPrefixes(), ports: []int{443}}
	for addr, want := range map[string]error{
		"8.8.8.8:443":   nil,
		"8.8.8.8:22":    ErrBlockedPort,
		"10.0.0.1:443":  ErrBlockedAddress,
		"[::1]:443":     ErrBlockedAddress,
		"not-an-ip:443": ErrBlockedAddress,
	} {
		if err := g.control("tcp", addr, nil); !errors.Is(err, want) {
			t.Errorf("control(%s) = %v, want %v", addr, err, want)
		}
	}
}
