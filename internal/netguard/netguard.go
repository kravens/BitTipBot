// Package netguard keeps outgoing connections for user-supplied URLs away from
// the bot's own host and network.
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Ranges that are not public but pass netip's own checks.
var reserved = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, overlay networks
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64, embeds an IPv4 address
	netip.MustParsePrefix("2002::/16"),     // 6to4, embeds an IPv4 address
}

// IsPublic reports whether ip is an address on the public internet.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, prefix := range reserved {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// The check runs at connect time on the resolved address, so it also covers
// redirects and hostnames that resolve to an internal address.
var dialer = &net.Dialer{
	Timeout: 10 * time.Second,
	Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return err
		}
		if !IsPublic(ap.Addr()) {
			return fmt.Errorf("connection to non-public address %s refused", ap.Addr())
		}
		return nil
	},
}

// trusted holds hostnames that may resolve to a non-public address: the
// bot's own LNURL and LNbits hosts often point at loopback through the hosts
// file or split DNS. Filled once at startup by Trust.
var trusted = map[string]bool{}

// Trust exempts hosts from the public address check.
func Trust(hosts ...string) {
	for _, host := range hosts {
		if host != "" {
			trusted[normalizeHost(host)] = true
		}
	}
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

// isTrusted reports whether addr ("host:port") names a trusted host.
func isTrusted(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return trusted[normalizeHost(host)]
}

var plainDialer = &net.Dialer{Timeout: 10 * time.Second}

// DialContext dials like net.Dialer, but only connects to public addresses,
// unless addr names a trusted host.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if isTrusted(addr) {
		return plainDialer.DialContext(ctx, network, addr)
	}
	return dialer.DialContext(ctx, network, addr)
}

// CheckHost rejects addr ("host:port") when the host is a literal non-public
// IP or a localhost name. It does not resolve other hostnames, so use it only
// where the connection is made by a proxy and DialContext cannot be used.
func CheckHost(addr string) error {
	if isTrusted(addr) {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = normalizeHost(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("connection to %s refused", host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !IsPublic(ip) {
			return fmt.Errorf("connection to non-public address %s refused", ip)
		}
		return nil
	}
	// Resolvers also accept IPv4 addresses written as 2130706433, 127.1 or
	// 0x7f.1. No real hostname ends in a number.
	if _, err := strconv.ParseUint(host[strings.LastIndex(host, ".")+1:], 0, 64); err == nil {
		return fmt.Errorf("connection to numeric host %s refused", host)
	}
	return nil
}
