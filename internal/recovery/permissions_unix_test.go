//go:build darwin || linux

package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRejectsLoosePermissions(t *testing.T) {
	for _, target := range []string{"work", "keys", "key_file", "owner.lock", "header.json"} {
		t.Run(target, func(t *testing.T) {
			w, o := createFixture(t)
			paths := map[string]string{"work": o.Directory, "keys": o.KeyDirectory, "key_file": filepath.Join(o.KeyDirectory, w.ID()+".key"), "owner.lock": filepath.Join(o.Directory, "owner.lock"), "header.json": filepath.Join(o.Directory, "header.json")}
			w.Close()
			mode := os.FileMode(0644)
			if target == "work" || target == "keys" {
				mode = 0755
			}
			if err := os.Chmod(paths[target], mode); err != nil {
				t.Fatal(err)
			}
			opened, err := Open(context.Background(), o)
			if !errors.Is(err, ErrPermissions) {
				if opened != nil {
					opened.Close()
				}
				t.Fatalf("unsafe permissions were accepted: %v", err)
			}
		})
	}
}

func TestWorkspaceRejectsSymlinksAndHardlinks(t *testing.T) {
	for _, target := range []string{"work", "keys", "key_file", "owner.lock", "header.json"} {
		t.Run(target, func(t *testing.T) {
			w, o := createFixture(t)
			paths := map[string]string{"work": o.Directory, "keys": o.KeyDirectory, "key_file": filepath.Join(o.KeyDirectory, w.ID()+".key"), "owner.lock": filepath.Join(o.Directory, "owner.lock"), "header.json": filepath.Join(o.Directory, "header.json")}
			w.Close()
			path := paths[target]
			original := path + ".held"
			if err := os.Rename(path, original); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(original, path); err != nil {
				t.Fatal(err)
			}
			opened, err := Open(context.Background(), o)
			if err == nil {
				opened.Close()
				t.Fatal("symlink accepted")
			}
		})
	}
	w, o := createFixture(t)
	w.Close()
	if err := os.Link(filepath.Join(o.KeyDirectory, w.ID()+".key"), filepath.Join(filepath.Dir(o.Directory), "extra-key-link")); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(context.Background(), o)
	if !errors.Is(err, ErrPermissions) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("multiply linked key accepted")
	}
}
