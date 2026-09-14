package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// trustedNets holds api.trusted_clients: addresses whose requests skip the API
// key and the WebSocket origin check. It exists for a reverse proxy that
// enforces its own login in front of Sentinel, such as a home dashboard that
// embeds the NVR, so authentication can be turned on without breaking it.
//
// Trust is decided from the TCP peer address only. X-Forwarded-For and
// similar headers are ignored, because any client can set them.
type trustedNets []*net.IPNet

// parseTrusted accepts single IPs ("10.0.0.5", "fd00::1") and CIDR ranges.
func parseTrusted(entries []string) (trustedNets, error) {
	var out trustedNets
	for _, raw := range entries {
		e := strings.TrimSpace(raw)
		if e == "" {
			continue
		}
		if !strings.Contains(e, "/") {
			ip := net.ParseIP(e)
			if ip == nil {
				return nil, fmt.Errorf("trusted_clients: %q is not an IP address or CIDR range", raw)
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			return nil, fmt.Errorf("trusted_clients: %q is not an IP address or CIDR range", raw)
		}
		out = append(out, n)
	}
	return out, nil
}

// trusts reports whether the request's TCP peer is a trusted client.
func (t trustedNets) trusts(r *http.Request) bool {
	if len(t) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i] // drop an IPv6 zone
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range t {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
