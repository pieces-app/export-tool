package recovery

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

// A hidden cause cost a week on Windows (issue #10). Failures must name the
// underlying operating-system error while never leaking a filesystem path.
func TestFailureMessagesNameTheCauseWithoutPaths(t *testing.T) {
	secret := "/private/workspace/with secret name"
	cases := []struct {
		name  string
		cause error
		want  []string
		avoid []string
	}{
		{"path error", &os.PathError{Op: "open", Path: secret + "/state.sqlite", Err: syscall.EACCES}, []string{"open", "permission denied"}, []string{secret, "state.sqlite"}},
		{"link error", &os.LinkError{Op: "rename", Old: secret + "/a", New: secret + "/b", Err: syscall.EEXIST}, []string{"rename", "file exists"}, []string{secret}},
		{"syscall error", os.NewSyscallError("LockFileEx", syscall.Errno(33)), []string{"LockFileEx", "33"}, nil},
		{"errno", syscall.ENOSPC, []string{"no space left on device"}, nil},
		{"driver message without a path", errors.New("SQL logic error: invalid uri authority: C: (1)"), []string{"invalid uri authority: C:"}, nil},
		{"message containing a path", errors.New("cannot read " + secret + "/keys"), []string{"*errors.errorString"}, []string{secret, "keys"}},
		{"message containing a drive path", errors.New(`stat C:\Users\someone\AppData: denied`), []string{"*errors.errorString"}, []string{"someone", `C:\`}},
	}
	for _, c := range cases {
		err := fail(ErrStorage, c.cause)
		msg := err.Error()
		if !strings.HasPrefix(msg, ErrStorage.Error()+": ") {
			t.Errorf("%s: message %q does not start with the kind", c.name, msg)
		}
		for _, w := range c.want {
			if !strings.Contains(msg, w) {
				t.Errorf("%s: message %q lacks %q", c.name, msg, w)
			}
		}
		for _, a := range c.avoid {
			if strings.Contains(msg, a) {
				t.Errorf("%s: message %q leaks %q", c.name, msg, a)
			}
		}
		if !errors.Is(err, ErrStorage) || !errors.Is(err, c.cause) {
			t.Errorf("%s: errors.Is semantics changed", c.name)
		}
	}
	if msg := fail(ErrPermissions, nil).Error(); msg != ErrPermissions.Error() {
		t.Errorf("nil cause should print the bare kind, got %q", msg)
	}
}
