// Command sentinel is the main entry point for Sentinel NVR.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bughatti/sentinel/internal/app"
	"github.com/bughatti/sentinel/internal/config"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var cfgPath string
	var logLevel string

	cmd := &cobra.Command{
		Use:   "sentinel",
		Short: "Sentinel NVR — GPU-accelerated network video recorder",
		Long: `Sentinel NVR is a high-performance, GPU-accelerated Network Video Recorder
built in Go. It provides a integration-friendly API for Home Assistant integration.

Documentation: https://github.com/bughatti/sentinel`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cfgPath, logLevel)
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&cfgPath, "config", "", "Path to config.yaml (default: /etc/sentinel/config.yaml or ./config.yaml)")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "Override log level (debug|info|warn|error)")

	// Sub-commands.
	cmd.AddCommand(versionCmd())
	cmd.AddCommand(validateCmd())

	return cmd
}

func run(cfgPath, logLevelOverride string) error {
	// Resolve config path.
	cfgPath = resolveConfigPath(cfgPath)

	// Load config first (log level comes from config).
	cfg, err := config.Load(cfgPath)
	if err != nil {
		// Can't use structured logger before config is loaded.
		fmt.Fprintf(os.Stderr, "fatal: load config %s: %v\n", cfgPath, err)
		return err
	}

	// Apply log level override.
	level := cfg.LogLevel
	if logLevelOverride != "" {
		level = logLevelOverride
	}
	setupLogger(level)

	slog.Info("config loaded", "path", cfgPath)

	// Start config hot-reloader. Only log level is applied live; all other
	// changes require a restart (the running subsystems capture their config at
	// startup), so we say so plainly rather than pretending a full reload happened.
	watcher, err := config.NewWatcher(cfgPath, func(newCfg *config.Config) {
		setupLogger(newCfg.LogLevel)
		slog.Warn("config file changed — applied log_level live; all other changes require a restart to take effect",
			"log_level", newCfg.LogLevel)
	})
	if err != nil {
		slog.Warn("config watcher init failed — hot-reload disabled", "err", err)
	} else {
		defer watcher.Close()
	}

	// Build and start application.
	a, err := app.New(cfg)
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := a.Start(ctx); err != nil {
		return fmt.Errorf("start app: %w", err)
	}

	// Block until SIGTERM or SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	slog.Info("received signal", "signal", sig)
	cancel()

	// Graceful shutdown with timeout.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := a.Stop(shutdownCtx); err != nil {
		slog.Error("graceful shutdown error", "err", err)
		return err
	}
	return nil
}

// versionCmd prints the build version and exits.
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and exit",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("sentinel %s\n", app.Version)
		},
	}
}

// validateCmd validates a config file without starting the server.
func validateCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a configuration file and exit",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath = resolveConfigPath(cfgPath)
			_, err := config.Load(cfgPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "config invalid: %v\n", err)
				return err
			}
			fmt.Printf("config %s is valid\n", cfgPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "Path to config.yaml")
	return cmd
}

// resolveConfigPath returns the effective config file path, trying several
// defaults in order if path is empty.
func resolveConfigPath(path string) string {
	if path != "" {
		return path
	}
	candidates := []string{
		"/etc/sentinel/config.yaml",
		"./config.yaml",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[len(candidates)-1] // will fail with a clear error
}

// setupLogger configures the global slog logger with the requested level.
func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     l,
		AddSource: l == slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))
}
