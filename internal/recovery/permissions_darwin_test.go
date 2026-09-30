package recovery

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorkspaceDarwinACL(t *testing.T) {
	parent := t.TempDir()
	// Public synthetic fixture only. A newly created private child must strip
	// this inherited grant before writing any key or workspace content.
	cmd := exec.Command("/bin/chmod", "+a", "everyone allow read,search,file_inherit,directory_inherit", parent)
	if err := cmd.Run(); err != nil {
		t.Fatal("could not establish the synthetic inherited-ACL fixture")
	}
	o := fixtureOptions(parent)
	w, err := Create(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	key := filepath.Join(o.KeyDirectory, w.ID()+".key")
	if err := exec.Command("/bin/chmod", "+a", "everyone allow read", key).Run(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(key)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("fixture did not keep private mode bits alongside its ACL")
	}
	opened, err := Open(context.Background(), o)
	if !errors.Is(err, ErrPermissions) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("Darwin ACL grant bypassed private mode bits")
	}
}
