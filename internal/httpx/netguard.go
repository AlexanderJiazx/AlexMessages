package httpx

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// nonPublicPrefixes lists every address range that must never be dialed on a
// user's behalf: the entries of the IANA IPv4 and IPv6 Special-Purpose Address
// Registries that are not globally reachable, the documentation and
// benchmarking ranges, multicast, the reserved 240.0.0.0/4, and the IPv6
// ranges that carry an embedded IPv4 address (NAT64, 6to4, Teredo, SIIT), which
// could lead to an internal IPv4 host through a translator. IPv4-mapped IPv6
// addresses (::ffff:a.b.c.d) are unmapped and checked as IPv4 first.
var nonPublicPrefixes = func() []netip.Prefix {
	ranges := []string{
		// IPv4 (RFC 6890 and its updates)
		"0.0.0.0/8",       // "this network"
		"10.0.0.0/8",      // private
		"100.64.0.0/10",   // shared address space (carrier-grade NAT)
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local
		"172.16.0.0/12",   // private
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation (TEST-NET-1)
		"192.88.99.0/24",  // 6to4 relay anycast (deprecated)
		"192.168.0.0/16",  // private
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation (TEST-NET-2)
		"203.0.113.0/24",  // documentation (TEST-NET-3)
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved, including 255.255.255.255

		// IPv6
		"::/96",           // unspecified, loopback, IPv4-compatible (deprecated)
		"::ffff:0:0/96",   // IPv4-mapped (unmapped before the check; kept as a backstop)
		"::ffff:0:0:0/96", // IPv4-translated (SIIT)
		"64:ff9b::/96",    // NAT64 well-known prefix
		"64:ff9b:1::/48",  // NAT64 local-use prefix
		"100::/64",        // discard-only
		"100:0:0:1::/64",  // dummy prefix
		"2001::/23",       // IETF protocol assignments (Teredo, benchmarking, ORCHID…)
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4
		"3fff::/20",       // documentation
		"5f00::/16",       // SRv6 SIDs
		"fc00::/7",        // unique local
		"fe80::/10",       // link-local
		"fec0::/10",       // site-local (deprecated)
		"ff00::/8",        // multicast
	}
	out := make([]netip.Prefix, 0, len(ranges))
	for _, s := range ranges {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// IsPublicAddr reports whether a is a globally reachable unicast address,
// that is, outside every range in nonPublicPrefixes.
func IsPublicAddr(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	a = a.Unmap().WithZone("")
	for _, p := range nonPublicPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// IsPublicIP is IsPublicAddr for a net.IP.
func IsPublicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	return ok && IsPublicAddr(a)
}

// GuardedDialContext is an http.Transport DialContext that resolves the host
// itself and dials only public addresses (IsPublicAddr). Because the check
// runs at dial time, a DNS name that resolves to an internal address, or a
// redirect to one, is refused too.
func GuardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	var lastErr error
	for _, a := range addrs {
		if !IsPublicAddr(a) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no public address for %s", host)
}
