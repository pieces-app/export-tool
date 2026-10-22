package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A permission failure names the condition that failed, so a report can be
// diagnosed from the message alone, and never repeats the kind text.
func TestPermissionFailuresNameTheCondition(t *testing.T) {
	cases := []struct {
		name   string
		unix   bool
		break_ func(t *testing.T, w *Workspace, o Options)
		want   string
	}{
		{"hard link", false, func(t *testing.T, w *Workspace, o Options) {
			if err := os.Link(filepath.Join(o.KeyDirectory, w.ID()+".key"), filepath.Join(filepath.Dir(o.Directory), "extra-key-link")); err != nil {
				t.Skip("hard links unavailable:", err)
			}
		}, "hard link"},
		{"loose permissions", true, func(t *testing.T, w *Workspace, o Options) {
			if err := os.Chmod(filepath.Join(o.Directory, "header.json"), 0644); err != nil {
				t.Fatal(err)
			}
		}, "readable or writable by other users"},
		{"loose directory permissions", true, func(t *testing.T, w *Workspace, o Options) {
			if err := os.Chmod(o.KeyDirectory, 0755); err != nil {
				t.Fatal(err)
			}
		}, "readable or writable by other users"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.unix && runtime.GOOS == "windows" {
				t.Skip("Unix file modes")
			}
			w, o := createFixture(t)
			w.Close()
			c.break_(t, w, o)
			opened, err := Open(context.Background(), o)
			if opened != nil {
				opened.Close()
			}
			if !errors.Is(err, ErrPermissions) {
				t.Fatalf("expected a permission failure, got %v", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, c.want) {
				t.Fatalf("message %q does not name the condition %q", msg, c.want)
			}
			if strings.Count(msg, ErrPermissions.Error()) != 1 {
				t.Fatalf("message repeats the kind: %q", msg)
			}
		})
	}
}

// Wrapping a kind around itself must not print it twice.
func TestFailureDoesNotRepeatItsKind(t *testing.T) {
	if msg := fail(ErrPermissions, ErrPermissions).Error(); msg != ErrPermissions.Error() {
		t.Fatalf("got %q", msg)
	}
}
