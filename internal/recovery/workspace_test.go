package recovery

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixtureOptions(root string) Options {
	return Options{Directory: filepath.Join(root, "work"), KeyDirectory: filepath.Join(root, "keys"), Binding: sha256.Sum256([]byte("synthetic immutable configuration")), MaxPayloadBytes: 1024}
}

func createFixture(t *testing.T) (*Workspace, Options) {
	t.Helper()
	o := fixtureOptions(t.TempDir())
	w, err := Create(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, o
}

func snapshot(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = sha256.Sum256(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSnapshot(t *testing.T, root string, expected map[string][32]byte) {
	t.Helper()
	got := snapshot(t, root)
	if len(got) != len(expected) {
		t.Fatal("failed open changed the workspace/key file set")
	}
	for name, hash := range expected {
		if got[name] != hash {
			t.Fatal("failed open changed existing workspace/key bytes")
		}
	}
}

func TestWorkspaceRoundTripAndBinding(t *testing.T) {
	w, o := createFixture(t)
	ctx := context.Background()
	ref := sha256.Sum256([]byte("synthetic-record"))
	plain := []byte(`{"known":["synthetic-private-credential"],"projection":"absent"}`)
	blob, err := w.Seal(ctx, "scanner", ref, 17, plain)
	if err != nil || bytes.Contains(blob, plain) {
		t.Fatal("payload was not encrypted")
	}
	again, err := w.Seal(ctx, "scanner", ref, 17, plain)
	if err != nil || bytes.Equal(blob, again) {
		t.Fatal("envelopes did not receive independent randomness")
	}
	if err := writeNew(ctx, w.root, "test-state.bin", blob); err != nil {
		t.Fatal(err)
	}
	id := w.ID()
	key := bytes.Clone(w.key[:])
	entries, err := os.ReadDir(o.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(o.Directory, entry.Name()))
		if err != nil || bytes.Contains(b, key) || bytes.Contains(b, []byte("synthetic-private-credential")) {
			t.Fatal("shareable workspace copy contained the key or plaintext payload")
		}
	}
	if err := w.Close(); err != nil || w.key != ([32]byte{}) {
		t.Fatal("close did not release/clear workspace state")
	}
	if _, err := w.Seal(ctx, "scanner", ref, 17, plain); !errors.Is(err, ErrClosed) {
		t.Fatal("closed workspace accepted new plaintext")
	}
	if err := w.Close(); err != nil {
		t.Fatal("close is not idempotent")
	}
	w, err = Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.ID() != id {
		t.Fatal("workspace identity changed on open")
	}
	decoded, err := w.Unseal(ctx, "scanner", ref, 17, blob)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatal("record did not recover after closing/reopening")
	}
	for _, tc := range []struct {
		kind string
		ref  [32]byte
		gen  uint64
	}{
		{"record", ref, 17}, {"scanner", sha256.Sum256([]byte("another")), 17}, {"scanner", ref, 18},
	} {
		if _, err := w.Unseal(ctx, tc.kind, tc.ref, tc.gen, blob); !errors.Is(err, ErrInvalid) {
			t.Fatal("envelope accepted incorrect kind, record, or generation")
		}
	}
	for _, offset := range []int{0, 31, 32, len(blob) - 1} {
		bad := bytes.Clone(blob)
		bad[offset] ^= 1
		if _, err := w.Unseal(ctx, "scanner", ref, 17, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("modified envelope passed authentication")
		}
	}
	other, _ := createFixture(t)
	if _, err := other.Unseal(ctx, "scanner", ref, 17, blob); !errors.Is(err, ErrInvalid) {
		t.Fatal("different workspace accepted this envelope")
	}
}

func TestWorkspaceRejectsIncompleteOrChangedInputs(t *testing.T) {
	for _, scenario := range []string{"binding", "limit", "missing_key", "wrong_key", "long_key", "proof", "header_version", "header_duplicate", "missing_lease"} {
		t.Run(scenario, func(t *testing.T) {
			w, o := createFixture(t)
			keyPath := filepath.Join(o.KeyDirectory, w.ID()+".key")
			leasePath := filepath.Join(o.KeyDirectory, w.ID()+".lock")
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "binding":
				o.Binding[0] ^= 1
			case "limit":
				o.MaxPayloadBytes++
			case "missing_key":
				if err := os.Remove(keyPath); err != nil {
					t.Fatal(err)
				}
			case "wrong_key", "long_key":
				n := 32
				if scenario == "long_key" {
					n++
				}
				if err := os.WriteFile(keyPath, bytes.Repeat([]byte{7}, n), 0600); err != nil {
					t.Fatal(err)
				}
			case "proof":
				if err := os.WriteFile(filepath.Join(o.Directory, "proof.bin"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "header_version", "header_duplicate":
				h := w.header
				if scenario == "header_version" {
					h.Version++
				}
				data, _ := json.Marshal(h)
				if scenario == "header_duplicate" {
					data = append([]byte(`{"version":1,`), data[1:]...)
				}
				if err := os.WriteFile(filepath.Join(o.Directory, "header.json"), append(data, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing_lease":
				if err := os.Remove(leasePath); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshot(t, filepath.Dir(o.Directory))
			opened, err := Open(context.Background(), o)
			if err == nil {
				opened.Close()
				t.Fatal("changed or incomplete state was accepted")
			}
			if strings.Contains(err.Error(), o.Directory) || strings.Contains(err.Error(), keyPath) {
				t.Fatal("storage error exposed a path")
			}
			assertSnapshot(t, filepath.Dir(o.Directory), before)
		})
	}
}

func TestWorkspaceBoundsCancellationAndExistingPaths(t *testing.T) {
	w, o := createFixture(t)
	ctx := context.Background()
	for _, kind := range []string{"", "../secret", "records/name", strings.Repeat("a", 65)} {
		if _, err := w.Seal(ctx, kind, [32]byte{}, 0, nil); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid namespace accepted")
		}
	}
	if _, err := w.Seal(ctx, "record", [32]byte{}, 0, make([]byte, 1025)); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized plaintext accepted")
	}
	for _, length := range []int{0, envelopeOverhead - 1, 1025 + envelopeOverhead} {
		if _, err := w.Unseal(ctx, "record", [32]byte{}, 0, make([]byte, length)); !errors.Is(err, ErrInvalid) {
			t.Fatal("unbounded or truncated envelope accepted")
		}
	}
	empty, err := w.Seal(ctx, "record", [32]byte{}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := w.Unseal(ctx, "record", [32]byte{}, 0, empty); err != nil || len(got) != 0 {
		t.Fatal("empty payload failed round trip")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, operation := range []func() error{
		func() error { _, err := w.Seal(canceled, "record", [32]byte{}, 0, nil); return err },
		func() error { _, err := w.Unseal(canceled, "record", [32]byte{}, 0, empty); return err },
		func() error { _, err := Open(canceled, o); return err },
	} {
		if !errors.Is(operation(), context.Canceled) {
			t.Fatal("canceled operation did not stop")
		}
	}
	next := fixtureOptions(t.TempDir())
	if _, err := Create(canceled, next); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled creation did not stop")
	}
	if _, err := os.Stat(next.Directory); !os.IsNotExist(err) {
		t.Fatal("canceled creation modified storage")
	}
	before := snapshot(t, filepath.Dir(o.Directory))
	if _, err := Create(ctx, o); err == nil {
		t.Fatal("existing workspace was overwritten")
	}
	assertSnapshot(t, filepath.Dir(o.Directory), before)
	next.Binding = [32]byte{}
	if _, err := Create(ctx, next); !errors.Is(err, ErrCompatibility) {
		t.Fatal("missing configuration binding accepted")
	}
}

func TestWorkspaceCloseDuringEncryption(t *testing.T) {
	w, o := createFixture(t)
	ctx := context.Background()
	var workers sync.WaitGroup
	results := make(chan []byte, 16)
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			blob, err := w.Seal(ctx, "record", [32]byte{}, 1, []byte("synthetic"))
			if err == nil {
				results <- blob
			} else if !errors.Is(err, ErrClosed) {
				failures <- err
			}
		}()
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatalf("encryption raced with key clearing: %v", err)
	}
	w, err := Open(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for blob := range results {
		plain, err := w.Unseal(ctx, "record", [32]byte{}, 1, blob)
		if err != nil || string(plain) != "synthetic" {
			t.Fatal("completed encryption used a cleared or partially cleared key")
		}
	}
	var zero Workspace
	if _, err := zero.Seal(ctx, "record", [32]byte{}, 0, nil); !errors.Is(err, ErrClosed) {
		t.Fatal("uninitialized workspace accepted plaintext")
	}
}

func TestWorkspaceMovedAndNestedPaths(t *testing.T) {
	w, o := createFixture(t)
	id := w.ID()
	w.Close()
	next := filepath.Join(filepath.Dir(o.Directory), "moved")
	if err := os.Rename(o.Directory, next); err != nil {
		t.Fatal(err)
	}
	o.Directory = next
	w, err := Open(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if w.ID() != id {
		t.Fatal("move changed the workspace identity")
	}
	bad := o
	bad.Directory = filepath.Join(o.KeyDirectory, "nested")
	if _, err := Create(context.Background(), bad); !errors.Is(err, ErrPermissions) {
		t.Fatal("workspace inside key directory was accepted")
	}
	bad = o
	bad.KeyDirectory = filepath.Join(o.Directory, "keys")
	if _, err := Open(context.Background(), bad); !errors.Is(err, ErrPermissions) {
		t.Fatal("key directory inside workspace was accepted")
	}
}

func copyWorkspace(t *testing.T, from, to string) {
	t.Helper()
	root, err := openPrivateRoot(context.Background(), to, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(from, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := writeNew(context.Background(), root, entry.Name(), body); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkspaceLockChild(t *testing.T) {
	if base := os.Getenv("PIECES_RECOVERY_WORKSPACE_CHILD"); base != "" {
		w, err := Open(context.Background(), fixtureOptions(base))
		if err != nil {
			t.Fatal(err)
		}
		// Keep the live owner reachable while the parent attempts takeover.
		defer w.Close()
		fmt.Fprintln(os.Stdout, "workspace locked")
		one := make([]byte, 1)
		if _, err := io.ReadFull(os.Stdin, one); err != nil {
			t.Fatal(err)
		}
		os.Exit(23)
	}
	w, o := createFixture(t)
	w.Close()
	clone := o
	clone.Directory = filepath.Join(filepath.Dir(o.Directory), "copy")
	copyWorkspace(t, o.Directory, clone.Directory)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWorkspaceLockChild$")
	cmd.Env = append(os.Environ(), "PIECES_RECOVERY_WORKSPACE_CHILD="+filepath.Dir(o.Directory))
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill() // Only this test's synthetic child.
			_ = cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "workspace locked\n" {
		t.Fatalf("child failed before acquiring ownership: %v", err)
	}
	for _, attempt := range []Options{o, clone} {
		opened, err := Open(context.Background(), attempt)
		if !errors.Is(err, ErrBusy) {
			if opened != nil {
				opened.Close()
			}
			t.Fatalf("active original/copy ownership was not refused: %v", err)
		}
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	input.Close()
	err = cmd.Wait()
	waited = true
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 23 {
		t.Fatalf("child did not reach its intended abrupt exit: %v", err)
	}
	w, err = Open(context.Background(), clone)
	if err != nil {
		t.Fatalf("kernel ownership survived process exit: %v", err)
	}
	defer w.Close()
	if opened, err := Open(context.Background(), o); !errors.Is(err, ErrBusy) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("recovered clone did not own the original key identity")
	}
}
