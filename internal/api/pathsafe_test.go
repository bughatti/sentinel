package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bughatti/sentinel/internal/storage"
	"github.com/go-chi/chi/v5"
)

// newMediaServer lays out real media directories under a temp root, plus a
// "secret" file one level above the recordings directory, which no request
// must ever be able to read.
func newMediaServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	rec := filepath.Join(root, "recordings")
	for _, d := range []string{"recordings", "snapshots", "clips", "exports", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "secret.mp4"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	seg := filepath.Join(rec, "drive", "2026-09-13", "14")
	if err := os.MkdirAll(seg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seg, "00.mp4"), []byte("VIDEO"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{storage: storage.NewLocalStorage(rec,
		filepath.Join(root, "snapshots"), filepath.Join(root, "clips"),
		filepath.Join(root, "exports"), filepath.Join(root, "tmp"))}
	r := chi.NewRouter()
	r.Get("/vod/{year}-{month}-{day}/{hour}/{camera}/index.m3u8", s.handleHLSPlaylist)
	r.Get("/vod/{year}-{month}-{day}/{hour}/{camera}/{segment}", s.handleHLSSegment)
	r.Get("/clips/{file}", s.handleServeClip)
	r.Get("/api/events/{id}/snapshot.jpg", s.handleEventSnapshot)
	r.Get("/api/events/{id}/clip.mp4", s.handleEventClip)
	return r, root
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	// httptest.NewRequest does not clean "..", which is exactly how a client
	// sending a raw path (curl --path-as-is) reaches the router.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestMediaRoutesServeLegitimateFiles(t *testing.T) {
	h, _ := newMediaServer(t)
	if rec := get(h, "/vod/2026-09-13/14/drive/00.mp4"); rec.Code != http.StatusOK || rec.Body.String() != "VIDEO" {
		t.Fatalf("real segment: got %d %q, want 200 VIDEO", rec.Code, rec.Body.String())
	}
	if rec := get(h, "/vod/2026-09-13/14/drive/index.m3u8"); rec.Code != http.StatusOK {
		t.Fatalf("real playlist: got %d, want 200", rec.Code)
	}
}

// A request built from ".." path elements must never read outside the media
// directories. Before the fix, the segment handler checked only the segment
// name, so hour=".." and camera=".." walked out of the recordings directory.
func TestMediaRoutesRejectTraversal(t *testing.T) {
	h, _ := newMediaServer(t)
	attacks := []string{
		"/vod/2026-09-13/../../secret.mp4",
		"/vod/2026-09-13/14/../secret.mp4",
		"/vod/2026-09-13/../drive/00.mp4",
		"/vod/..-..-../14/drive/00.mp4",
		"/vod/2026-09-13/../../index.m3u8",
		"/clips/..",
		"/api/events/../snapshot.jpg",
		"/api/events/../clip.mp4",
	}
	for _, p := range attacks {
		rec := get(h, p)
		if rec.Body.String() == "SECRET" || rec.Code == http.StatusOK {
			t.Errorf("%s: got %d %q, must be rejected", p, rec.Code, rec.Body.String())
		}
	}
}

func TestSafeElement(t *testing.T) {
	good := []string{"drive", "Back_Drive", "eufy-kitchen", "1789314900.235710.8fb3b114",
		"1789314900.235710-8fb3b114", "00.mp4"}
	bad := []string{"", ".", "..", "../x", "a/b", `a\b`, "a..b", "/etc", "x\x00y"}
	for _, s := range good {
		if !safeElement(s) {
			t.Errorf("safeElement(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if safeElement(s) {
			t.Errorf("safeElement(%q) = true, want false", s)
		}
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		root, p string
		want    bool
	}{
		{"/media/rec", "/media/rec/cam/a.mp4", true},
		{"/media/rec", "/media/rec", false},
		{"/media/rec", "/media/secret.mp4", false},
		{"/media/rec", "/media/recx/a.mp4", false},
		{"/media/rec", "/media/rec/../secret", false},
	}
	for _, c := range cases {
		if got := within(c.root, c.p); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.root, c.p, got, c.want)
		}
	}
}
