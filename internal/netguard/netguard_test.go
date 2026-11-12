package netguard

import (
	"context"
	"net"
	"net/netip"
	"testing"
)

func TestIsPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"1.1.1.1":              true,
		"2606:4700:4700::1111": true,
		"127.0.0.1":            false,
		"::1":                  false,
		"0.0.0.0":              false,
		"10.1.2.3":             false,
		"172.16.0.1":           false,
		"192.168.1.1":          false,
		"169.254.169.254":      false,
		"100.64.0.1":           false,
		"fe80::1":              false,
		"fd00::1":              false,
		"::ffff:127.0.0.1":     false,
		"::ffff:192.168.1.1":   false,
		"224.0.0.1":            false,
		"255.255.255.255":      false,
	} {
		if got := IsPublic(netip.MustParseAddr(addr)); got != want {
			t.Errorf("IsPublic(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestDialContextRefusesLoopback(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	conn, err := DialContext(context.Background(), "tcp", l.Addr().String())
	if err == nil {
		conn.Close()
		t.Fatal("dial to loopback succeeded")
	}
	// a hostname that resolves to loopback is refused as well
	_, port, _ := net.SplitHostPort(l.Addr().String())
	if conn, err := DialContext(context.Background(), "tcp", "localhost:"+port); err == nil {
		conn.Close()
		t.Fatal("dial to localhost succeeded")
	}
}

func TestCheckHost(t *testing.T) {
	for addr, ok := range map[string]bool{
		"example.com:443":   true,
		"abc.onion:80":      true,
		"1.1.1.1:443":       true,
		"localhost:6060":    false,
		"LOCALHOST.:6060":   false,
		"foo.localhost:80":  false,
		"127.0.0.1:6060":    false,
		"[::1]:6060":        false,
		"192.168.0.10:5000": false,
		"0.0.0.0:80":        false,
	} {
		if err := CheckHost(addr); (err == nil) != ok {
			t.Errorf("CheckHost(%s) = %v, want ok=%v", addr, err, ok)
		}
	}
}
