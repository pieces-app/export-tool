// Package recovery provides private, exclusively owned recovery workspaces.
// Export-state persistence and CLI resume integration are separate layers.
package recovery

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var (
	ErrBusy          = errors.New("recovery workspace is owned by another operation")
	ErrInvalid       = errors.New("recovery workspace is invalid or incomplete")
	ErrPermissions   = errors.New("recovery storage permissions, ownership or hard links are unsafe; keep recovery folders out of synced folders such as iCloud Drive")
	ErrKey           = errors.New("recovery key is missing, invalid, or does not match")
	ErrCompatibility = errors.New("recovery configuration does not match this workspace")
	ErrStorage       = errors.New("recovery storage operation failed")
	ErrClosed        = errors.New("recovery workspace is closed")
)

// Preserve underlying filesystem causes for errors.Is without printing paths.
type failure struct{ kind, cause error }

func (e *failure) Error() string        { return e.kind.Error() }
func (e *failure) Unwrap() error        { return e.cause }
func (e *failure) Is(target error) bool { return target == e.kind }
func fail(kind, cause error) error      { return &failure{kind, cause} }

const maxPayloadBytes = 128 << 20
const envelopeOverhead = 32 + 12 + 16 // HKDF salt, GCM nonce, authentication tag
const proofText = "pieces-export workspace ownership proof v1"

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type Options struct {
	Directory    string
	KeyDirectory string
	// Binding is a digest of the immutable export configuration/compatibility
	// contract. The export adapter, not this storage layer, defines that schema.
	Binding [32]byte
	// Zero selects 128 MiB; a smaller bound is useful for a restricted store.
	MaxPayloadBytes int
}

type header struct {
	Version  int    `json:"version"`
	ID       string `json:"id"`
	Binding  string `json:"binding"`
	MaxBytes int    `json:"max_payload_bytes"`
}

type Workspace struct {
	mu         sync.Mutex
	root, keys *os.Root
	lock       *os.File
	keyLease   *os.File
	key        [32]byte
	header     header
	headerHash [32]byte
	closed     bool
}

// Resolve parents to handle ordinary platform aliases (such as macOS /var),
// but never follow a symlink supplied as the workspace/key directory itself.
func directoryPath(name string) (string, error) {
	if name == "" {
		return "", ErrInvalid
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", fail(ErrInvalid, err)
	}
	if info, err := os.Lstat(abs); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", ErrInvalid
		}
	} else if !os.IsNotExist(err) {
		return "", fail(ErrStorage, err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fail(ErrStorage, err)
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func contained(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func normalize(o Options) (Options, error) {
	if o.Binding == ([32]byte{}) {
		return o, ErrCompatibility
	}
	if o.MaxPayloadBytes == 0 {
		o.MaxPayloadBytes = maxPayloadBytes
	}
	if o.MaxPayloadBytes < 1 || o.MaxPayloadBytes > maxPayloadBytes {
		return o, ErrInvalid
	}
	var err error
	if o.Directory, err = directoryPath(o.Directory); err != nil {
		return o, err
	}
	if o.KeyDirectory, err = directoryPath(o.KeyDirectory); err != nil {
		return o, err
	}
	if contained(o.Directory, o.KeyDirectory) || contained(o.KeyDirectory, o.Directory) {
		return o, ErrPermissions
	}
	return o, nil
}

// openPrivateRoot never repairs permissions on an existing directory. The
// caller must supply existing parents; default platform locations are wired by
// the future export adapter, not silently created in arbitrary user folders.
func openPrivateRoot(ctx context.Context, path string, create, allowExisting bool) (*os.Root, error) {
	created := false
	if create {
		if err := makePrivateDirectory(path); err != nil {
			if !allowExisting || !os.IsExist(err) {
				return nil, fail(ErrStorage, err)
			}
		} else {
			created = true
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fail(ErrInvalid, err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fail(ErrStorage, err)
	}
	f, err := root.Open(".")
	if err == nil {
		defer f.Close()
		var held os.FileInfo
		held, err = f.Stat()
		if err == nil && !os.SameFile(info, held) {
			err = ErrPermissions
		}
		if err == nil && created {
			err = finishPrivateDirectory(ctx, f)
		}
		if err == nil {
			err = checkPrivate(ctx, f, true)
		}
	}
	if err != nil {
		root.Close()
		return nil, fail(ErrPermissions, err)
	}
	return root, nil
}

func openPrivateFile(ctx context.Context, root *os.Root, name string, flags int) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if info, err := root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return nil, ErrPermissions
		}
	} else if !os.IsNotExist(err) || flags&os.O_CREATE == 0 {
		return nil, fail(ErrStorage, err)
	}
	f, err := root.OpenFile(name, flags, 0600)
	if err != nil {
		return nil, fail(ErrStorage, err)
	}
	if flags&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL {
		if err := finishPrivateFile(ctx, f); err != nil {
			f.Close()
			return nil, fail(ErrPermissions, err)
		}
	}
	held, err := f.Stat()
	if err == nil {
		var current os.FileInfo
		current, err = root.Lstat(name)
		if err == nil && (!current.Mode().IsRegular() || !os.SameFile(held, current)) {
			err = ErrPermissions
		}
	}
	if err == nil {
		err = checkPrivate(ctx, f, false)
	}
	if err != nil {
		f.Close()
		return nil, fail(ErrPermissions, err)
	}
	return f, nil
}

func writeNew(ctx context.Context, root *os.Root, name string, data []byte) error {
	f, err := openPrivateFile(ctx, root, name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err != nil {
		return fail(ErrStorage, err)
	}
	return nil
}

func readPrivate(ctx context.Context, root *os.Root, name string, limit int) ([]byte, error) {
	f, err := openPrivateFile(ctx, root, name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() < 0 || info.Size() > int64(limit) {
		return nil, fail(ErrInvalid, err)
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(b) > limit {
		return nil, fail(ErrInvalid, err)
	}
	return b, nil
}

func acquire(ctx context.Context, o Options, create bool) (_ *Workspace, result error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o, err := normalize(o)
	if err != nil {
		return nil, err
	}
	w := &Workspace{}
	defer func() {
		if result != nil {
			_ = w.Close()
		}
	}()
	w.root, err = openPrivateRoot(ctx, o.Directory, create, false)
	if err != nil {
		return nil, err
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	w.lock, err = openPrivateFile(ctx, w.root, "owner.lock", flags)
	if err != nil {
		return nil, err
	}
	if err := lockFile(w.lock); err != nil {
		return nil, err
	}
	w.keys, err = openPrivateRoot(ctx, o.KeyDirectory, create, true)
	if err != nil {
		return nil, fail(ErrKey, err)
	}
	// Also compare directory identities: lexical paths alone do not detect
	// case aliases on a case-insensitive filesystem.
	if err := separateRoots(w.root, w.keys); err != nil {
		return nil, err
	}
	if create {
		id := make([]byte, 32)
		if _, err := rand.Read(id); err != nil {
			return nil, fail(ErrStorage, err)
		}
		w.header = header{1, hex.EncodeToString(id), hex.EncodeToString(o.Binding[:]), o.MaxPayloadBytes}
		if err := w.leaseKey(ctx, true); err != nil {
			return nil, err
		}
		if _, err := rand.Read(w.key[:]); err != nil {
			return nil, fail(ErrStorage, err)
		}
		if err := writeNew(ctx, w.keys, w.header.ID+".key", w.key[:]); err != nil {
			return nil, fail(ErrKey, err)
		}
		body, _ := json.Marshal(w.header)
		body = append(body, '\n')
		w.headerHash = sha256.Sum256(body)
		proof, err := w.seal("workspace-proof", [32]byte{}, 0, []byte(proofText), 1024)
		if err != nil {
			return nil, err
		}
		if err := writeNew(ctx, w.root, "header.json", body); err != nil {
			return nil, err
		}
		if err := writeNew(ctx, w.root, "proof.bin", proof); err != nil {
			return nil, err
		}
	} else {
		body, err := readPrivate(ctx, w.root, "header.json", 2048)
		if err != nil || json.Unmarshal(body, &w.header) != nil {
			return nil, fail(ErrInvalid, err)
		}
		canonical, _ := json.Marshal(w.header)
		if !bytes.Equal(body, append(canonical, '\n')) || w.header.Version != 1 || !validID(w.header.ID) || !validID(w.header.Binding) || w.header.MaxBytes < 1 || w.header.MaxBytes > maxPayloadBytes {
			return nil, ErrInvalid
		}
		if w.header.Binding != hex.EncodeToString(o.Binding[:]) || w.header.MaxBytes != o.MaxPayloadBytes {
			return nil, ErrCompatibility
		}
		if err := w.leaseKey(ctx, false); err != nil {
			return nil, err
		}
		key, err := readPrivate(ctx, w.keys, w.header.ID+".key", 32)
		if err != nil || len(key) != 32 {
			return nil, fail(ErrKey, err)
		}
		copy(w.key[:], key)
		clear(key)
		w.headerHash = sha256.Sum256(body)
		proof, err := readPrivate(ctx, w.root, "proof.bin", 1024+envelopeOverhead)
		if err != nil {
			return nil, fail(ErrInvalid, err)
		}
		plain, err := w.unseal("workspace-proof", [32]byte{}, 0, proof, 1024)
		if err != nil || string(plain) != proofText {
			return nil, ErrKey
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return w, nil
}

func separateRoots(a, b *os.Root) error {
	for _, pair := range [][2]*os.Root{{a, b}, {b, a}} {
		held, err := pair[0].Stat(".")
		if err != nil {
			return fail(ErrStorage, err)
		}
		for path := pair[1].Name(); ; path = filepath.Dir(path) {
			info, err := os.Stat(path)
			if err != nil {
				return fail(ErrStorage, err)
			}
			if os.SameFile(held, info) {
				return ErrPermissions
			}
			if path == filepath.Dir(path) {
				break
			}
		}
	}
	return nil
}

func (w *Workspace) leaseKey(ctx context.Context, create bool) error {
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	var err error
	w.keyLease, err = openPrivateFile(ctx, w.keys, w.header.ID+".lock", flags)
	if err != nil {
		return fail(ErrKey, err)
	}
	// Copies of a workspace still share its key identity. A second lock at
	// that identity prevents different copied owner.lock files authorizing
	// two writers within the same key directory.
	return lockFile(w.keyLease)
}

func validID(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == value
}

func Create(ctx context.Context, o Options) (*Workspace, error) { return acquire(ctx, o, true) }
func Open(ctx context.Context, o Options) (*Workspace, error)   { return acquire(ctx, o, false) }

// ID contains only the random workspace identifier, never a source identity.
func (w *Workspace) ID() string { return w.header.ID }

func (w *Workspace) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	clear(w.key[:])
	var result error
	if w.lock != nil {
		// Closing releases the kernel lock, including on abrupt process exit.
		result = errors.Join(result, w.lock.Close())
	}
	if w.keyLease != nil {
		result = errors.Join(result, w.keyLease.Close())
	}
	for _, root := range []*os.Root{w.root, w.keys} {
		if root != nil {
			result = errors.Join(result, root.Close())
		}
	}
	if result != nil {
		return fail(ErrStorage, result)
	}
	return nil
}

func (w *Workspace) associated(kind string, ref [32]byte, generation uint64) []byte {
	b := append([]byte("pieces-export/recovery/v1/"), w.headerHash[:]...)
	b = append(b, byte(len(kind)))
	b = append(b, kind...)
	b = append(b, ref[:]...)
	return binary.BigEndian.AppendUint64(b, generation)
}

// A fresh 256-bit salt derives a separate AES key for every envelope. Each GCM
// key seals one message, rather than sharing GCM's per-key random-nonce budget
// across millions of records, retries and restored processes.
func (w *Workspace) cipher(salt []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, w.key[:], salt, "pieces-export/recovery/envelope/v1", 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (w *Workspace) seal(kind string, ref [32]byte, generation uint64, plain []byte, limit int) ([]byte, error) {
	if !kindPattern.MatchString(kind) || len(plain) > limit {
		return nil, ErrInvalid
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, fail(ErrStorage, err)
	}
	a, err := w.cipher(salt)
	if err != nil {
		return nil, fail(ErrKey, err)
	}
	return a.Seal(salt, nil, plain, w.associated(kind, ref, generation)), nil
}

func (w *Workspace) unseal(kind string, ref [32]byte, generation uint64, blob []byte, limit int) ([]byte, error) {
	if !kindPattern.MatchString(kind) || len(blob) < envelopeOverhead || len(blob) > limit+envelopeOverhead {
		return nil, ErrInvalid
	}
	a, err := w.cipher(blob[:32])
	if err != nil {
		return nil, fail(ErrKey, err)
	}
	plain, err := a.Open(nil, nil, blob[32:], w.associated(kind, ref, generation))
	if err != nil {
		return nil, ErrInvalid
	}
	return plain, nil
}

func (w *Workspace) Seal(ctx context.Context, kind string, ref [32]byte, generation uint64, plain []byte) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.closed || w.root == nil || w.lock == nil || w.keyLease == nil {
		return nil, ErrClosed
	}
	return w.seal(kind, ref, generation, plain, w.header.MaxBytes)
}

func (w *Workspace) Unseal(ctx context.Context, kind string, ref [32]byte, generation uint64, blob []byte) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.closed || w.root == nil || w.lock == nil || w.keyLease == nil {
		return nil, ErrClosed
	}
	return w.unseal(kind, ref, generation, blob, w.header.MaxBytes)
}
