package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrConflict = errors.New("recovery checkpoint generation changed")
	ErrReopen   = errors.New("recovery storage failed; close and reopen before continuing")
)

const (
	storeFile      = "state.sqlite"
	checkpointSize = 4 + 8 + 8 + 8 + 32
	maxBatchItems  = 50
	maxBatchBytes  = 8 << 20
	applicationID  = 0x50455850 // PEXP; private application format identifier.
)

// These exact schema definitions are validated before application queries.
// No private identity, body, credential, path, cursor or policy enters a SQL
// column unencrypted. The change log contains opaque references and hashes.
var storeSchema = []string{
	`CREATE TABLE items (ref BLOB PRIMARY KEY CHECK(length(ref)=32), generation INTEGER NOT NULL CHECK(generation>0), payload BLOB NOT NULL) STRICT, WITHOUT ROWID`,
	`CREATE TABLE changes (sequence INTEGER PRIMARY KEY CHECK(sequence>0), ref BLOB NOT NULL CHECK(length(ref)=32), generation INTEGER NOT NULL CHECK(generation>0), digest BLOB NOT NULL CHECK(length(digest)=32)) STRICT`,
	`CREATE INDEX changes_ref ON changes (ref, sequence DESC)`,
	`CREATE TABLE checkpoint (singleton INTEGER PRIMARY KEY CHECK(singleton=1), generation INTEGER NOT NULL CHECK(generation>=0), payload BLOB NOT NULL) STRICT`,
}

// Record includes the complete private evidence and inclusion decision for an
// identity, encoded by the exporter. Exclusions are payloads, not row deletion.
// Ref must be an opaque, stable 32-byte reference, never a raw private ID.
type Record struct {
	Ref     [32]byte
	Payload []byte
}

// Checkpoint.State is an exporter-defined encoding of complete accumulated
// scanner state, exact source progress, counters and phase prerequisites.
// This layer atomically binds those bytes to Records; it cannot validate the
// exporter-specific meaning. A new store starts at generation zero.
type Checkpoint struct {
	Generation uint64
	Records    uint64
	Changes    uint64
	State      []byte
}

type checkpointMeta struct {
	Generation, Records, Changes uint64
	Head                         [32]byte
}

// Store owns its workspace and one SQLite connection. Close releases both.
// It is not safe to copy a Store. No source OS calls are made by this package.
type Store struct {
	mu     sync.Mutex
	w      *Workspace
	db     *sql.DB
	conn   *sql.Conn
	guard  *os.File
	meta   checkpointMeta
	closed bool
	failed bool
}

func CreateStore(ctx context.Context, o Options, initialState []byte) (*Store, error) {
	return acquireStore(ctx, o, initialState, true)
}

// OpenStore verifies the complete change history and current ciphertext set
// before returning. SQLite may first roll back an interrupted transaction.
// Restoring an entire older, internally consistent workspace cannot be
// detected without a separate trusted anti-rollback anchor.
func OpenStore(ctx context.Context, o Options) (*Store, error) {
	return acquireStore(ctx, o, nil, false)
}

func acquireStore(ctx context.Context, o Options, initial []byte, create bool) (_ *Store, result error) {
	var err error
	if o, err = normalize(o); err != nil {
		return nil, err
	}
	if o.MaxPayloadBytes < checkpointSize || len(initial) > o.MaxPayloadBytes-checkpointSize {
		return nil, ErrInvalid
	}
	s := &Store{}
	defer func() {
		if result != nil {
			_ = s.Close()
		}
	}()
	if create {
		s.w, err = Create(ctx, o)
	} else {
		s.w, err = Open(ctx, o)
	}
	if err != nil {
		return nil, err
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	s.guard, err = openPrivateFile(ctx, s.w.root, storeFile, flags)
	if err != nil {
		return nil, err
	}
	if err := s.checkFiles(ctx); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(s.w.root.Name(), storeFile))}
	q := u.Query()
	q.Set("mode", "rw") // Never silently create an absent/replaced database.
	u.RawQuery = q.Encode()
	s.db, err = sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fail(ErrStorage, err)
	}
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
	s.conn, err = s.db.Conn(ctx)
	if err != nil {
		return nil, fail(ErrStorage, err)
	}
	if err := s.configure(ctx); err != nil {
		return nil, err
	}
	if create {
		if err := s.initialize(ctx, initial); err != nil {
			return nil, err
		}
	}
	if err := s.verify(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) checkFiles(ctx context.Context) error {
	// SQLite opens journals by pathname. A moved or replaced directory must
	// not redirect those writes away from the root this operation owns.
	named, err := os.Lstat(s.w.root.Name())
	if err != nil || !named.IsDir() || named.Mode()&os.ModeSymlink != 0 {
		return fail(ErrPermissions, err)
	}
	held, err := s.w.root.Stat(".")
	if err != nil || !os.SameFile(named, held) {
		return fail(ErrPermissions, err)
	}
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		name := storeFile + suffix
		info, err := s.w.root.Lstat(name)
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil {
			return fail(ErrStorage, err)
		}
		if suffix == "-wal" || suffix == "-shm" || !info.Mode().IsRegular() {
			return ErrInvalid
		}
		if suffix == "" {
			held, err := s.guard.Stat()
			if err != nil || !os.SameFile(info, held) {
				return fail(ErrPermissions, err)
			}
			// Never open/close another descriptor for the database while
			// SQLite owns it: POSIX close releases this process's fcntl locks
			// on that inode, including locks held by SQLite's descriptor.
			if err := checkPrivate(ctx, s.guard, false); err != nil {
				return fail(ErrPermissions, err)
			}
			continue
		}
		f, err := openPrivateFile(ctx, s.w.root, name, os.O_RDONLY)
		if err != nil {
			return err
		}
		f.Close()
	}
	return nil
}

func (s *Store) configure(ctx context.Context) error {
	for kind, value := range map[int]int{
		sqlite3.SQLITE_LIMIT_LENGTH:     max(64<<10, s.w.header.MaxBytes+envelopeOverhead+1024),
		sqlite3.SQLITE_LIMIT_SQL_LENGTH: 64 << 10,
		sqlite3.SQLITE_LIMIT_ATTACHED:   0,
	} {
		if _, err := sqlite.Limit(s.conn, kind, value); err != nil {
			return fail(ErrStorage, err)
		}
		if got, err := sqlite.Limit(s.conn, kind, -1); err != nil || got != value {
			return fail(ErrStorage, err)
		}
	}
	for _, query := range []string{
		"PRAGMA trusted_schema=OFF", "PRAGMA synchronous=EXTRA", "PRAGMA fullfsync=ON",
		"PRAGMA temp_store=MEMORY", "PRAGMA cache_size=-4096", "PRAGMA mmap_size=0",
		"PRAGMA busy_timeout=0", "PRAGMA cell_size_check=ON",
	} {
		if _, err := s.conn.ExecContext(ctx, query); err != nil {
			return fail(ErrStorage, err)
		}
	}
	for query, want := range map[string]string{
		"PRAGMA journal_mode": "delete", "PRAGMA synchronous": "3", "PRAGMA fullfsync": "1",
		"PRAGMA temp_store": "2", "PRAGMA cache_size": "-4096", "PRAGMA trusted_schema": "0",
		"PRAGMA mmap_size": "0", "PRAGMA busy_timeout": "0", "PRAGMA cell_size_check": "1",
	} {
		var got string
		if err := s.conn.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
			return fail(ErrStorage, err)
		}
	}
	return s.checkFiles(ctx)
}

func (s *Store) initialize(ctx context.Context, state []byte) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fail(ErrStorage, err)
	}
	defer tx.Rollback()
	for _, query := range append([]string{"PRAGMA application_id=1346721872", "PRAGMA user_version=1"}, storeSchema...) {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return fail(ErrStorage, err)
		}
	}
	s.meta.Head = sha256.Sum256(append([]byte("pieces-export/recovery/changes/v1/"), s.w.headerHash[:]...))
	if err := s.putCheckpoint(ctx, tx, s.meta, state); err != nil {
		return err
	}
	if err := s.checkFiles(ctx); err != nil {
		return err
	}
	return storageError(tx.Commit())
}

func storageError(err error) error {
	if err == nil {
		return nil
	}
	return fail(ErrStorage, err)
}

func encodeCheckpoint(m checkpointMeta, state []byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, 1)
	b = binary.BigEndian.AppendUint64(b, m.Generation)
	b = binary.BigEndian.AppendUint64(b, m.Records)
	b = binary.BigEndian.AppendUint64(b, m.Changes)
	b = append(b, m.Head[:]...)
	return append(b, state...)
}

func (s *Store) putCheckpoint(ctx context.Context, tx *sql.Tx, m checkpointMeta, state []byte) error {
	plain := encodeCheckpoint(m, state)
	defer clear(plain)
	blob, err := s.w.Seal(ctx, "checkpoint", [32]byte{}, m.Generation, plain)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO checkpoint VALUES(1,?,?) ON CONFLICT(singleton) DO UPDATE SET generation=excluded.generation,payload=excluded.payload", m.Generation, blob)
	return storageError(err)
}

func (s *Store) readCheckpoint(ctx context.Context) (checkpointMeta, []byte, error) {
	var m checkpointMeta
	var generation int64
	var blob []byte
	err := s.conn.QueryRowContext(ctx, `SELECT generation, CASE WHEN typeof(payload)='blob' AND length(payload) BETWEEN ? AND ? THEN payload END FROM checkpoint WHERE singleton=1`, checkpointSize+envelopeOverhead, s.w.header.MaxBytes+envelopeOverhead).Scan(&generation, &blob)
	if err != nil || generation < 0 || blob == nil {
		return m, nil, fail(ErrInvalid, err)
	}
	plain, err := s.w.Unseal(ctx, "checkpoint", [32]byte{}, uint64(generation), blob)
	if err != nil {
		return m, nil, err
	}
	defer clear(plain)
	if len(plain) < checkpointSize || binary.BigEndian.Uint32(plain) != 1 {
		return m, nil, ErrInvalid
	}
	m.Generation = binary.BigEndian.Uint64(plain[4:12])
	m.Records = binary.BigEndian.Uint64(plain[12:20])
	m.Changes = binary.BigEndian.Uint64(plain[20:28])
	copy(m.Head[:], plain[28:60])
	if m.Generation != uint64(generation) || m.Changes > math.MaxInt64 || m.Records > m.Changes {
		return m, nil, ErrInvalid
	}
	return m, bytes.Clone(plain[checkpointSize:]), nil
}

func extendHead(previous [32]byte, sequence, generation uint64, ref, digest [32]byte) [32]byte {
	b := append([]byte("pieces-export/recovery/change/v1/"), previous[:]...)
	b = binary.BigEndian.AppendUint64(b, sequence)
	b = binary.BigEndian.AppendUint64(b, generation)
	b = append(b, ref[:]...)
	return sha256.Sum256(append(b, digest[:]...))
}

func (s *Store) verify(ctx context.Context) error {
	for query, want := range map[string]int{"PRAGMA application_id": applicationID, "PRAGMA user_version": 1} {
		var got int
		if err := s.conn.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
			return fail(ErrInvalid, err)
		}
	}
	rows, err := s.conn.QueryContext(ctx, "SELECT sql FROM sqlite_schema WHERE sql IS NOT NULL")
	if err != nil {
		return fail(ErrInvalid, err)
	}
	expected := map[string]bool{}
	for _, stmt := range storeSchema {
		expected[stmt] = true
	}
	for rows.Next() {
		var stmt string
		if err := rows.Scan(&stmt); err != nil || !expected[stmt] {
			rows.Close()
			return fail(ErrInvalid, err)
		}
		delete(expected, stmt)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(expected) != 0 {
		return fail(ErrInvalid, err)
	}
	var integrity string
	if err := s.conn.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return fail(ErrInvalid, err)
	}
	var count int64
	if err := s.conn.QueryRowContext(ctx, "SELECT count(*) FROM checkpoint").Scan(&count); err != nil || count != 1 {
		return fail(ErrInvalid, err)
	}
	m, state, err := s.readCheckpoint(ctx)
	clear(state)
	if err != nil {
		return err
	}
	head := sha256.Sum256(append([]byte("pieces-export/recovery/changes/v1/"), s.w.headerHash[:]...))
	rows, err = s.conn.QueryContext(ctx, `SELECT sequence, CASE WHEN length(ref)=32 THEN ref END, generation, CASE WHEN length(digest)=32 THEN digest END FROM changes ORDER BY sequence`)
	if err != nil {
		return fail(ErrInvalid, err)
	}
	var sequence, generation uint64
	for rows.Next() {
		var seq, gen int64
		var ref, digest []byte
		if err := rows.Scan(&seq, &ref, &gen, &digest); err != nil || seq <= 0 || uint64(seq) != sequence+1 || uint64(seq) > m.Changes || gen <= 0 || uint64(gen) < generation || uint64(gen) > m.Generation || len(ref) != 32 || len(digest) != 32 || [32]byte(ref) == ([32]byte{}) {
			rows.Close()
			return fail(ErrInvalid, err)
		}
		sequence, generation = uint64(seq), uint64(gen)
		head = extendHead(head, sequence, generation, [32]byte(ref), [32]byte(digest))
	}
	err = rows.Err()
	rows.Close()
	if err != nil || sequence != m.Changes || head != m.Head {
		return fail(ErrInvalid, err)
	}
	if err := s.conn.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT ref FROM changes GROUP BY ref)").Scan(&count); err != nil || count < 0 || uint64(count) != m.Records {
		return fail(ErrInvalid, err)
	}
	s.meta = m
	count, err = s.visit(ctx, false, nil)
	if err != nil || uint64(count) != m.Records {
		return fail(ErrInvalid, err)
	}
	return s.checkFiles(ctx)
}

func (s *Store) usable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed || s.w == nil || s.conn == nil {
		return ErrClosed
	}
	if s.failed {
		return ErrReopen
	}
	return nil
}

func (s *Store) Snapshot(ctx context.Context) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(ctx); err != nil {
		return Checkpoint{}, err
	}
	m, state, err := s.readCheckpoint(ctx)
	if err != nil || m != s.meta {
		clear(state)
		return Checkpoint{}, fail(ErrInvalid, err)
	}
	return Checkpoint{m.Generation, m.Records, m.Changes, state}, nil
}

func (s *Store) validateBatch(expected uint64, records []Record, state []byte) error {
	if expected != s.meta.Generation {
		return ErrConflict
	}
	if expected >= math.MaxInt64 || s.meta.Changes > math.MaxInt64-uint64(len(records)) || len(records) > maxBatchItems || len(state) > s.w.header.MaxBytes-checkpointSize {
		return ErrInvalid
	}
	total := uint64(len(state)) + checkpointSize
	seen := map[[32]byte]bool{}
	for _, r := range records {
		if r.Ref == ([32]byte{}) || seen[r.Ref] || len(r.Payload) > s.w.header.MaxBytes {
			return ErrInvalid
		}
		seen[r.Ref] = true
		total += uint64(len(r.Payload))
	}
	if total > maxPayloadBytes || total > maxBatchBytes && len(records) > 1 {
		return ErrInvalid
	}
	return nil
}

// Commit atomically replaces the specified records and complete State, and
// increments the generation. At most 50 records / 8 MiB are accepted, with
// one larger record allowed alone up to the 128 MiB combined plaintext bound.
// Inputs remain caller-owned and must not change while this method runs.
// Any storage error requires Close/OpenStore to resolve commit uncertainty.
func (s *Store) Commit(ctx context.Context, expected uint64, records []Record, state []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(ctx); err != nil {
		return err
	}
	if err := s.validateBatch(expected, records, state); err != nil {
		return err
	}
	tx, next, err := s.stageBatch(ctx, records, state)
	if err == nil {
		err = storageError(tx.Commit())
	}
	if err != nil {
		if tx != nil {
			_ = tx.Rollback()
		}
		s.failed = true
		return err
	}
	s.meta = next
	return nil
}

// stageBatch separates the transaction boundary for process-exit acceptance.
// Caller holds mu and validates all inputs before entering this method.
func (s *Store) stageBatch(ctx context.Context, records []Record, state []byte) (_ *sql.Tx, next checkpointMeta, result error) {
	if err := s.checkFiles(ctx); err != nil {
		return nil, next, err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, next, fail(ErrStorage, err)
	}
	defer func() {
		if result != nil {
			_ = tx.Rollback()
		}
	}()
	next = s.meta
	next.Generation++
	for _, r := range records {
		var present int
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM items WHERE ref=?)", r.Ref[:]).Scan(&present); err != nil {
			return nil, next, fail(ErrStorage, err)
		}
		blob, err := s.w.Seal(ctx, "record", r.Ref, next.Generation, r.Payload)
		if err != nil {
			return nil, next, err
		}
		digest := sha256.Sum256(blob)
		if _, err := tx.ExecContext(ctx, "INSERT INTO items VALUES(?,?,?) ON CONFLICT(ref) DO UPDATE SET generation=excluded.generation,payload=excluded.payload", r.Ref[:], next.Generation, blob); err != nil {
			return nil, next, fail(ErrStorage, err)
		}
		next.Changes++
		if present == 0 {
			next.Records++
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO changes VALUES(?,?,?,?)", next.Changes, r.Ref[:], next.Generation, digest[:]); err != nil {
			return nil, next, fail(ErrStorage, err)
		}
		next.Head = extendHead(next.Head, next.Changes, next.Generation, r.Ref, digest)
	}
	if err := s.putCheckpoint(ctx, tx, next, state); err != nil {
		return nil, next, err
	}
	if err := s.checkFiles(ctx); err != nil {
		return nil, next, err
	}
	return tx, next, nil
}

// Visit reads current records in opaque-reference order, using bounded rows.
// Payload is valid only during fn; copy it if retaining it. fn must not call
// this Store's methods. The store mutex prevents writes during the traversal.
func (s *Store) Visit(ctx context.Context, fn func(Record) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(ctx); err != nil {
		return err
	}
	if fn == nil {
		return ErrInvalid
	}
	count, err := s.visit(ctx, true, fn)
	if err == nil && uint64(count) != s.meta.Records {
		err = ErrInvalid
	}
	return err
}

func (s *Store) visit(ctx context.Context, decrypt bool, fn func(Record) error) (int64, error) {
	rows, err := s.conn.QueryContext(ctx, `SELECT CASE WHEN length(i.ref)=32 THEN i.ref END, i.generation,
		CASE WHEN typeof(i.payload)='blob' AND length(i.payload) BETWEEN ? AND ? THEN i.payload END,
		c.generation, CASE WHEN length(c.digest)=32 THEN c.digest END
		FROM items AS i LEFT JOIN changes AS c ON c.sequence=(SELECT sequence FROM changes INDEXED BY changes_ref WHERE ref=i.ref ORDER BY sequence DESC LIMIT 1) ORDER BY i.ref`, envelopeOverhead, s.w.header.MaxBytes+envelopeOverhead)
	if err != nil {
		return 0, fail(ErrInvalid, err)
	}
	defer rows.Close()
	var count int64
	for rows.Next() {
		var ref, blob, digest []byte
		var gen int64
		var latest sql.NullInt64
		if err := rows.Scan(&ref, &gen, &blob, &latest, &digest); err != nil || len(ref) != 32 || blob == nil || len(digest) != 32 || gen <= 0 || uint64(gen) > s.meta.Generation || !latest.Valid || latest.Int64 != gen || sha256.Sum256(blob) != [32]byte(digest) {
			return count, fail(ErrInvalid, err)
		}
		if decrypt {
			plain, err := s.w.Unseal(ctx, "record", [32]byte(ref), uint64(gen), blob)
			if err != nil {
				return count, err
			}
			err = fn(Record{[32]byte(ref), plain})
			clear(plain)
			if err != nil {
				return count, err
			}
		}
		count++
	}
	return count, storageError(rows.Err())
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.conn != nil {
		err = errors.Join(err, s.conn.Close())
	}
	if s.db != nil {
		err = errors.Join(err, s.db.Close())
	}
	if s.guard != nil {
		err = errors.Join(err, s.guard.Close())
	}
	if s.w != nil {
		err = errors.Join(err, s.w.Close())
	}
	return storageError(err)
}
