// Package lifecycle controls installed applications without terminating Pieces OS.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pieces-app/export-tool/internal/exporter"
)

type Options struct {
	BaseURL, Environment, OSPath string
	Launch                       bool
	Timeout, StartupTimeout      time.Duration
	MaxBytes                     int64
}

func Connect(ctx context.Context, o Options) (*exporter.Client, exporter.ServerInfo, error) {
	return connect(ctx, o, exporter.DiscoverEnvironment, LaunchOS)
}

func connect(ctx context.Context, o Options, discover func(context.Context, time.Duration, int64, string) (*exporter.Client, exporter.ServerInfo, error), launch func(context.Context, string, string) error) (*exporter.Client, exporter.ServerInfo, error) {
	if o.Environment != "auto" && o.Environment != "production" && o.Environment != "staging" {
		return nil, exporter.ServerInfo{}, errors.New("environment must be auto, production, or staging")
	}
	startup, cancel := context.WithTimeout(ctx, o.StartupTimeout)
	defer cancel()
	launched := false
	for {
		var c *exporter.Client
		var info exporter.ServerInfo
		var err error
		if o.BaseURL != "" {
			c, err = exporter.NewClient(o.BaseURL, o.Timeout, o.MaxBytes)
			if err == nil {
				info, err = c.Inspect(startup)
			}
			if err == nil && o.Environment != "auto" && info.Environment != o.Environment {
				return nil, info, errors.New("connected OS environment does not match --environment")
			}
			if err == nil && !info.Ready {
				err = exporter.ErrMigrating
			}
		} else {
			c, info, err = discover(startup, o.Timeout, o.MaxBytes, o.Environment)
		}
		if err == nil {
			if o.Environment != "auto" && info.Environment != o.Environment {
				return nil, info, errors.New("connected OS environment does not match --environment")
			}
			return c, info, nil
		}
		if startup.Err() != nil {
			return nil, info, errors.New("OS startup/readiness deadline reached or canceled")
		}
		if errors.Is(err, exporter.ErrNotFound) && o.Launch && !launched {
			if o.Environment == "auto" {
				o.Environment = "production"
			}
			if err = launch(startup, o.Environment, o.OSPath); err != nil {
				return nil, info, err
			}
			launched = true
		} else if !errors.Is(err, exporter.ErrMigrating) && !(launched && errors.Is(err, exporter.ErrNotFound)) {
			return nil, info, err
		}
		select {
		case <-startup.Done():
			return nil, info, errors.New("OS startup/readiness deadline reached or canceled")
		case <-time.After(400 * time.Millisecond):
		}
	}
}

// Paths come only from the explicit CLI option or a fixed known installation,
// never from an HTTP response. All arguments are passed without a shell.
func LaunchOS(ctx context.Context, environment, path string) error {
	if path == "" && environment == "staging" && runtime.GOOS == "darwin" {
		path = "/Applications/Pieces OS (Local Staging).app"
	}
	if path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		st, err := os.Stat(absolute)
		if err != nil {
			return errors.New("requested OS installation does not exist; provide --os-path")
		}
		if runtime.GOOS == "darwin" && st.IsDir() && strings.HasSuffix(absolute, ".app") {
			return run(ctx, "/usr/bin/open", "-g", absolute)
		}
		if st.IsDir() {
			return errors.New("--os-path must identify an executable or macOS app bundle")
		}
		cmd := exec.Command(absolute)
		if err := cmd.Start(); err != nil {
			return errors.New("could not launch requested OS executable")
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	if environment == "staging" {
		return errors.New("staging launch requires --os-path; staging shares the production URL scheme")
	}
	return openURL(ctx, "pieces://")
}
func run(ctx context.Context, name string, args ...string) error {
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return fmt.Errorf("application action failed using %s", filepath.Base(name))
	}
	return nil
}
func openURL(ctx context.Context, url string) error {
	switch runtime.GOOS {
	case "darwin":
		return run(ctx, "/usr/bin/open", "-g", url)
	case "windows":
		return run(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", url)
	case "linux":
		return run(ctx, "xdg-open", url)
	default:
		return errors.New("application activation is unsupported on this OS")
	}
}
func desktopRunning(ctx context.Context) (bool, error) {
	switch runtime.GOOS {
	case "darwin", "linux":
		name := "Pieces"
		if runtime.GOOS == "linux" {
			name = "pieces_for_x"
		}
		err := exec.CommandContext(ctx, "pgrep", "-x", name).Run()
		if err == nil {
			return true, nil
		}
		var status *exec.ExitError
		if errors.As(err, &status) && status.ExitCode() == 1 {
			return false, nil
		}
		return false, errors.New("cannot determine whether Pieces Desktop is running")
	case "windows":
		b, err := exec.CommandContext(ctx, "tasklist.exe", "/FI", "IMAGENAME eq pieces_for_x.exe", "/FO", "CSV", "/NH").Output()
		if err != nil {
			return false, errors.New("cannot inspect Pieces Desktop process")
		}
		return strings.Contains(strings.ToLower(string(b)), "\"pieces_for_x.exe\""), nil
	default:
		return false, errors.New("desktop detection is unsupported on this OS")
	}
}
func CloseDesktop(ctx context.Context) error { return closeDesktop(ctx, desktopRunning, openURL) }
func closeDesktop(ctx context.Context, running func(context.Context) (bool, error), open func(context.Context, string) error) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	active, err := running(ctx)
	if err != nil || !active {
		return err
	}
	if err = open(ctx, "pieces-for-developers://quit"); err != nil {
		return err
	}
	for {
		active, err = running(ctx)
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("Pieces Desktop did not confirm graceful closure; close it manually or use --close-desktop=false")
		case <-time.After(250 * time.Millisecond):
		}
	}
}
