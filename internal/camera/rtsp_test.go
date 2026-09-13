package camera

import "testing"

// redactURL is what keeps camera passwords out of the logs, so it is worth
// pinning down: it must hide the password, keep everything else intact, and
// never mangle a URL that has no credentials in it.
func TestRedactURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"password hidden", "rtsp://admin:hunter2@192.0.2.10:554/stream1", "rtsp://admin:***@192.0.2.10:554/stream1"},
		{"user and path preserved", "rtsp://viewer:s3cr3t@cam.example:554/cam/realmonitor?channel=1", "rtsp://viewer:***@cam.example:554/cam/realmonitor?channel=1"},
		{"symbols in password", "rtsp://u:p@ss:w0rd@host/live", "rtsp://u:***@ss:w0rd@host/live"},
		{"no credentials", "rtsp://192.0.2.10:554/stream1", "rtsp://192.0.2.10:554/stream1"},
		{"user only, no password", "rtsp://admin@192.0.2.10:554/stream1", "rtsp://admin@192.0.2.10:554/stream1"},
		{"at sign in path only", "rtsp://host/path@segment", "rtsp://host/path@segment"},
		{"http url", "http://user:pw@example/api", "http://user:***@example/api"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redactURL(c.in)
			if got != c.want {
				t.Errorf("redactURL(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}
}

// A redacted URL must never still contain the secret.
func TestRedactURLLeavesNoPassword(t *testing.T) {
	const password = "SuperSecret123"
	got := redactURL("rtsp://admin:" + password + "@192.0.2.10:554/stream")
	if contains(got, password) {
		t.Fatalf("password survived redaction: %q", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
