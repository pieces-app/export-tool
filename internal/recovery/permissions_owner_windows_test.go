package recovery

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Files that other code creates, such as SQLite's rollback journal, take the
// token's default owner. In an elevated window that is the Administrators
// group unless the process adopts the user, and checkPrivate then rejects
// the tool's own journal (issue #20).
func TestProcessCreatesFilesOwnedByTheCurrentUser(t *testing.T) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	var size uint32
	_ = windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &size)
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], size, &size); err != nil {
		t.Fatal(err)
	}
	owner := (*struct{ Owner *windows.SID })(unsafe.Pointer(&buffer[0]))
	if !owner.Owner.Equals(user.User.Sid) {
		t.Fatalf("token default owner %s is not the user %s (elevated: %v)", owner.Owner, user.User.Sid, token.IsElevated())
	}
	path := filepath.Join(t.TempDir(), "journal-like")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	fileOwner, _, err := sd.Owner()
	if err != nil || !fileOwner.Equals(user.User.Sid) {
		t.Fatalf("a plainly created file is owned by %v, not the user (elevated: %v)", fileOwner, token.IsElevated())
	}
}
