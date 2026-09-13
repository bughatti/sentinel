package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `
database:
  url: "postgresql://sentinel:sentinel@localhost:5432/sentinel?sslmode=disable"
log_level: "info"
cameras:
  front_door:
    ffmpeg:
      inputs:
        - path: "rtsp://user:pass@192.0.2.10:554/stream"
          roles: [detect, record]
    detect:
      width: 1280
      height: 720
      fps: 5
`

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(write(t, validConfig))
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("log level = %q", cfg.LogLevel)
	}
	if cfg.Database.MaxConns != 10 {
		t.Errorf("defaults not applied: max_conns = %d, want 10", cfg.Database.MaxConns)
	}
	cam, ok := cfg.Cameras["front_door"]
	if !ok {
		t.Fatal("camera missing after load")
	}
	if cam.Name != "front_door" {
		t.Errorf("camera name should default to its key, got %q", cam.Name)
	}
	if cam.Detect.Width != 1280 || cam.Detect.Height != 720 || cam.Detect.FPS != 5 {
		t.Errorf("detect settings lost: %+v", cam.Detect)
	}
	// A camera that specifies none of these must inherit the globals, or the
	// pipeline dereferences a nil pointer at runtime.
	if cam.Motion == nil || cam.Record == nil || cam.Snapshots == nil || cam.Objects == nil {
		t.Error("camera did not inherit global motion/record/snapshots/objects")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("a missing config file must be an error")
	}
}

func TestValidationRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no database url", strings.Replace(validConfig, `  url: "postgresql://sentinel:sentinel@localhost:5432/sentinel?sslmode=disable"`, "", 1), "database.url"},
		{"no cameras", "database:\n  url: \"postgres://x\"\nlog_level: \"info\"\n", "at least one camera"},
		{"camera without detect role", strings.Replace(validConfig, "roles: [detect, record]", "roles: [record]", 1), "role 'detect'"},
		{"empty input path", strings.Replace(validConfig, `path: "rtsp://user:pass@192.0.2.10:554/stream"`, `path: ""`, 1), "path must not be empty"},
		{"zero detect width", strings.Replace(validConfig, "width: 1280", "width: 0", 1), "detect.width"},
		{"zero detect fps", strings.Replace(validConfig, "fps: 5", "fps: 0", 1), "detect.fps"},
		{"bad log level", strings.Replace(validConfig, `log_level: "info"`, `log_level: "chatty"`, 1), "log_level"},
		{"auth without key", validConfig + "api:\n  auth_enabled: true\n", "api.api_key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(write(t, c.body))
			if err == nil {
				t.Fatalf("expected rejection mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error should mention %q, got: %v", c.want, err)
			}
		})
	}
}

func TestLogLevelAccepted(t *testing.T) {
	for _, lvl := range []string{"debug", "info", "warn", "error", "INFO"} {
		body := strings.Replace(validConfig, `log_level: "info"`, `log_level: "`+lvl+`"`, 1)
		if _, err := Load(write(t, body)); err != nil {
			t.Errorf("log level %q should be accepted: %v", lvl, err)
		}
	}
}
