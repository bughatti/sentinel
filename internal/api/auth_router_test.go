package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bughatti/sentinel/internal/config"
	"github.com/bughatti/sentinel/internal/events"
	"github.com/bughatti/sentinel/internal/storage"
	"github.com/gorilla/websocket"
)

const testKey = "test-key-123"

// newAuthServer builds the real router with auth on or off, over a temp media
// tree holding one recorded segment.
func newAuthServer(t *testing.T, authOn bool, origins []string) http.Handler {
	t.Helper()
	return buildTestServer(t, config.APIConfig{AuthEnabled: authOn, APIKey: testKey, CORSOrigins: origins})
}

// newAuthServerTrusted is newAuthServer with auth on and a trusted_clients list.
func newAuthServerTrusted(t *testing.T, trusted []string) http.Handler {
	t.Helper()
	return buildTestServer(t, config.APIConfig{AuthEnabled: true, APIKey: testKey, TrustedClients: trusted})
}

func buildTestServer(t *testing.T, apiCfg config.APIConfig) http.Handler {
	t.Helper()
	root := t.TempDir()
	rec := filepath.Join(root, "recordings")
	seg := filepath.Join(rec, "drive", "2026-09-13", "14")
	if err := os.MkdirAll(seg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seg, "0000.mp4"), []byte("VIDEO"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg:     apiCfg,
		bus:     events.NewEventBus(),
		storage: storage.NewLocalStorage(rec, root, root, root, root),
		version: "test",
	}
	return s.buildRouter()
}

func do(h http.Handler, method, target, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// With auth on, the dashboard shell must still load (the browser has to show
// the page before it can ask for a key), and every data route must refuse a
// request without the key.
func TestAuthProtectsDataButNotTheDashboardShell(t *testing.T) {
	h := newAuthServer(t, true, nil)

	public := []string{"/", "/healthz"}
	for _, p := range public {
		if rec := do(h, http.MethodGet, p, ""); rec.Code != http.StatusOK {
			t.Errorf("%s without key: got %d, want 200 (must stay public)", p, rec.Code)
		}
	}

	protected := []string{
		"/api/version",
		"/vod/2026-09-13/14/drive/index.m3u8",
		"/vod/2026-09-13/14/drive/0000.mp4",
		"/clips/x.mp4",
		"/ws",
	}
	for _, p := range protected {
		if rec := do(h, http.MethodGet, p, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without key: got %d, want 401", p, rec.Code)
		}
	}

	if rec := do(h, http.MethodGet, "/api/version", testKey); rec.Code != http.StatusOK {
		t.Errorf("bearer key: got %d, want 200", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/version?api_key="+testKey, ""); rec.Code != http.StatusOK {
		t.Errorf("query key: got %d, want 200", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/version", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong key: got %d, want 401", rec.Code)
	}
}

// A browser <video> element cannot send headers, so a playlist fetched with
// ?api_key= must hand the same key to every segment URL it lists, and those
// segment URLs must actually work.
func TestPlaylistCarriesTheKeyToSegments(t *testing.T) {
	h := newAuthServer(t, true, nil)
	rec := do(h, http.MethodGet, "/vod/2026-09-13/14/drive/index.m3u8?api_key="+testKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("playlist: got %d, want 200", rec.Code)
	}
	var segURL string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, "/vod/") {
			segURL = line
		}
	}
	if !strings.HasSuffix(segURL, "?api_key="+testKey) {
		t.Fatalf("segment URL %q does not carry the key", segURL)
	}
	seg := do(h, http.MethodGet, segURL, "")
	if seg.Code != http.StatusOK || seg.Body.String() != "VIDEO" {
		t.Fatalf("segment via playlist URL: got %d %q, want 200 VIDEO", seg.Code, seg.Body.String())
	}
}

func TestAuthOffLeavesEverythingOpen(t *testing.T) {
	h := newAuthServer(t, false, nil)
	if rec := do(h, http.MethodGet, "/api/version", ""); rec.Code != http.StatusOK {
		t.Errorf("auth off: got %d, want 200", rec.Code)
	}
	rec := do(h, http.MethodGet, "/vod/2026-09-13/14/drive/index.m3u8", "")
	if strings.Contains(rec.Body.String(), "api_key") {
		t.Error("a playlist fetched without a key must not mention one")
	}
}

// A real WebSocket handshake: a page on a foreign website must be refused even
// with a valid key, while the dashboard's own origin and non-browser clients
// connect.
func TestWebSocketOriginCheck(t *testing.T) {
	srv := httptest.NewServer(newAuthServer(t, true, []string{"http://dashboard.example"}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?api_key=" + testKey

	dial := func(origin string) (int, error) {
		hdr := http.Header{}
		if origin != "" {
			hdr.Set("Origin", origin)
		}
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
		if conn != nil {
			conn.Close()
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			return resp.StatusCode, err
		}
		return 0, err
	}

	if code, err := dial("http://evil.example"); err == nil || code != http.StatusForbidden {
		t.Errorf("foreign origin: got code %d err %v, want refused with 403", code, err)
	}
	if _, err := dial(srv.URL); err != nil {
		t.Errorf("same origin: %v, want connected", err)
	}
	if _, err := dial("http://dashboard.example"); err != nil {
		t.Errorf("listed origin: %v, want connected", err)
	}
	if _, err := dial(""); err != nil {
		t.Errorf("no Origin (non-browser client): %v, want connected", err)
	}
}

func TestAllowedOrigin(t *testing.T) {
	req := func(host, origin string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/ws", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	check := allowedOrigin([]string{"https://home.example/"}, nil)
	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"same origin", req("nas:5000", "http://nas:5000"), true},
		{"same origin, different case", req("NAS:5000", "http://nas:5000"), true},
		{"listed origin, trailing slash normalised", req("nas:5000", "https://home.example"), true},
		{"foreign origin", req("nas:5000", "https://evil.example"), false},
		{"same host, different port", req("nas:5000", "http://nas:6000"), false},
		{"no origin header", req("nas:5000", ""), true},
	}
	for _, c := range cases {
		if got := check(c.r); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if !allowedOrigin([]string{"*"}, nil)(req("nas:5000", "https://anything.example")) {
		t.Error(`"*" must allow any origin`)
	}
}
