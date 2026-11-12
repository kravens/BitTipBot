// Package netguard keeps outgoing connections for user-supplied URLs away from
// the bot's own host and network.
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// 100.64.0.0/10 is carrier-grade NAT space, also used by overlay networks.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// IsPublic reports whether ip is an address on the public internet.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
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

// DialContext dials like net.Dialer, but only connects to public addresses.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return dialer.DialContext(ctx, network, addr)
}

// CheckHost rejects addr ("host:port") when the host is a literal non-public
// IP or a localhost name. It does not resolve other hostnames, so use it only
// where the connection is made by a proxy and DialContext cannot be used.
func CheckHost(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("connection to %s refused", host)
	}
	if ip, err := netip.ParseAddr(host); err == nil && !IsPublic(ip) {
		return fmt.Errorf("connection to non-public address %s refused", ip)
	}
	return nil
}
