package lifecycle

import (
	"context"
	"errors"
	"github.com/pieces-app/export-tool/internal/exporter"
	"testing"
	"time"
)

func TestDesktopQuitOnlyWhenRunning(t *testing.T) {
	calls := 0
	checks := 0
	probe := func(context.Context) (bool, error) { checks++; return checks == 1, nil }
	open := func(_ context.Context, url string) error {
		calls++
		if url != "pieces-for-developers://quit" {
			t.Fatal("wrong quit URL")
		}
		return nil
	}
	if err := closeDesktop(context.Background(), probe, open); err != nil || calls != 1 {
		t.Fatal("graceful closure failed")
	}
	calls = 0
	if err := closeDesktop(context.Background(), func(context.Context) (bool, error) { return false, nil }, open); err != nil || calls != 0 {
		t.Fatal("launched absent desktop")
	}
}

func TestDesktopClosureHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := closeDesktop(ctx, func(context.Context) (bool, error) { return true, nil }, func(context.Context, string) error { return nil })
	if err == nil {
		t.Fatal("canceled closure claimed success")
	}
}

func TestConnectLaunchEnvironmentAndMigration(t *testing.T) {
	for _, state := range []string{"absent", "migrating", "wrong_environment", "launch_failure", "read_failure"} {
		t.Run(state, func(t *testing.T) {
			calls, launches := 0, 0
			discover := func(_ context.Context, _ time.Duration, _ int64, env string) (*exporter.Client, exporter.ServerInfo, error) {
				calls++
				if calls == 1 {
					switch state {
					case "migrating":
						return nil, exporter.ServerInfo{}, exporter.ErrMigrating
					case "read_failure":
						return nil, exporter.ServerInfo{}, errors.New("ambiguous or unreadable")
					default:
						return nil, exporter.ServerInfo{}, exporter.ErrNotFound
					}
				}
				environment := "production"
				if state == "wrong_environment" {
					environment = "staging"
				}
				return nil, exporter.ServerInfo{Ready: true, Environment: environment}, nil
			}
			launch := func(_ context.Context, env, path string) error {
				launches++
				if env != "production" {
					t.Fatal("auto launch did not pin production")
				}
				if state == "launch_failure" {
					return errors.New("fixture failure")
				}
				return nil
			}
			_, _, err := connect(context.Background(), Options{Environment: "auto", Launch: true, StartupTimeout: time.Second}, discover, launch)
			wantsError := state == "wrong_environment" || state == "launch_failure" || state == "read_failure"
			if (err != nil) != wantsError {
				t.Fatalf("unexpected result: %v", err)
			}
			expectedLaunches := 1
			if state == "migrating" || state == "read_failure" {
				expectedLaunches = 0
			}
			if launches != expectedLaunches {
				t.Fatal("duplicate/unwanted launch")
			}
		})
	}
}

func TestConnectDeadlineAndMissingInstallation(t *testing.T) {
	calls := 0
	_, _, err := connect(context.Background(), Options{Environment: "staging", Launch: true, StartupTimeout: 10 * time.Millisecond}, func(context.Context, time.Duration, int64, string) (*exporter.Client, exporter.ServerInfo, error) {
		return nil, exporter.ServerInfo{}, exporter.ErrMigrating
	}, func(context.Context, string, string) error { calls++; return nil })
	if err == nil || calls != 0 {
		t.Fatal("migration wait ignored deadline or launched duplicate")
	}
	if err := LaunchOS(context.Background(), "staging", t.TempDir()+"/missing"); err == nil {
		t.Fatal("missing installation accepted")
	}
}
