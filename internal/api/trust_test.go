package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestParseTrusted(t *testing.T) {
	n, err := parseTrusted([]string{"10.0.0.5", " 192.168.50.0/24 ", "fd00::1", "", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	if len(n) != 4 {
		t.Fatalf("got %d networks, want 4", len(n))
	}
	for _, bad := range []string{"not-an-ip", "10.0.0.300", "10.0.0.0/33"} {
		if _, err := parseTrusted([]string{bad}); err == nil {
			t.Errorf("parseTrusted(%q) accepted a malformed entry", bad)
		}
	}
}

func TestTrustsUsesThePeerAddressOnly(t *testing.T) {
	trusted, _ := parseTrusted([]string{"10.0.0.5", "192.168.50.0/24", "fd00::/8"})
	req := func(remote, xff string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/version", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
			r.Header.Set("X-Real-IP", xff)
		}
		return r
	}
	cases := []struct {
		name   string
		r      *http.Request
		wanted bool
	}{
		{"exact IPv4", req("10.0.0.5:51234", ""), true},
		{"inside CIDR", req("192.168.50.77:443", ""), true},
		{"IPv6 in range", req("[fd00::42]:8080", ""), true},
		{"IPv6 with zone", req("[fd00::42%eth0]:8080", ""), true},
		{"untrusted peer", req("10.0.0.6:51234", ""), false},
		{"forwarded header cannot claim trust", req("203.0.113.9:4000", "10.0.0.5"), false},
		{"garbage remote address", req("nonsense", ""), false},
	}
	for _, c := range cases {
		if got := trusted.trusts(c.r); got != c.wanted {
			t.Errorf("%s: trusts = %v, want %v", c.name, got, c.wanted)
		}
	}
	if (trustedNets)(nil).trusts(req("10.0.0.5:1", "")) {
		t.Error("an empty trust list must trust nobody")
	}
}

// With auth on, a trusted proxy reaches data routes without a key while every
// other peer still needs one.
func TestTrustedClientSkipsTheKeyOthersDoNot(t *testing.T) {
	trusted, _ := parseTrusted([]string{"10.0.0.5"})
	h := authMiddleware("sekrit", trusted)(http.HandlerFunc(ok))
	call := func(remote, bearer string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/events", nil)
		r.RemoteAddr = remote
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if code := call("10.0.0.5:5000", ""); code != http.StatusOK {
		t.Errorf("trusted proxy without key: %d, want 200", code)
	}
	if code := call("10.0.0.9:5000", ""); code != http.StatusUnauthorized {
		t.Errorf("untrusted peer without key: %d, want 401", code)
	}
	if code := call("10.0.0.9:5000", "sekrit"); code != http.StatusOK {
		t.Errorf("untrusted peer with key: %d, want 200", code)
	}
}

// A reverse proxy forwards its users' Origin header, which never matches
// Sentinel's own host. From a trusted peer that must still connect; from any
// other peer the same Origin is refused.
func TestTrustedProxyWebSocketWithForeignOrigin(t *testing.T) {
	dial := func(trustedList []string) int {
		srv := httptest.NewServer(newAuthServerTrusted(t, trustedList))
		defer srv.Close()
		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
		hdr := http.Header{"Origin": []string{"https://homeforge.example"}}
		conn, resp, _ := websocket.DefaultDialer.Dial(wsURL, hdr)
		if conn != nil {
			conn.Close()
			return http.StatusSwitchingProtocols
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			return resp.StatusCode
		}
		return 0
	}
	// httptest connections arrive from 127.0.0.1.
	if code := dial([]string{"127.0.0.1"}); code != http.StatusSwitchingProtocols {
		t.Errorf("trusted proxy, no key, foreign Origin: got %d, want 101", code)
	}
	if code := dial(nil); code == http.StatusSwitchingProtocols {
		t.Error("untrusted peer, no key, foreign Origin: connected, want refused")
	}
}
