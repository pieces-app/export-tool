package recovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
)

var ErrNotFound = errors.New("recovery record is not present")

// Get retrieves one bounded, authenticated current payload through the primary
// index. It never visits or decrypts unrelated records. Returned bytes belong
// to the caller, who should clear private payloads after use. Deletion/exclusion
// decisions remain explicit records; ErrNotFound is not an inclusion decision.
func (s *Store) Get(ctx context.Context, ref [32]byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(ctx); err != nil {
		return nil, err
	}
	if ref == ([32]byte{}) {
		return nil, ErrInvalid
	}
	var blob, digest []byte
	var generation int64
	var latest sql.NullInt64
	err := s.conn.QueryRowContext(ctx, `SELECT i.generation,
		CASE WHEN typeof(i.payload)='blob' AND length(i.payload) BETWEEN ? AND ? THEN i.payload END,
		c.generation, CASE WHEN length(c.digest)=32 THEN c.digest END
		FROM items AS i LEFT JOIN changes AS c ON c.sequence=(SELECT sequence FROM changes INDEXED BY changes_ref WHERE ref=i.ref ORDER BY sequence DESC LIMIT 1)
		WHERE i.ref=?`, envelopeOverhead, s.w.header.MaxBytes+envelopeOverhead, ref[:]).Scan(&generation, &blob, &latest, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil || blob == nil || len(digest) != 32 || generation <= 0 || uint64(generation) > s.meta.Generation || !latest.Valid || latest.Int64 != generation || sha256.Sum256(blob) != [32]byte(digest) {
		return nil, fail(ErrInvalid, err)
	}
	return s.w.Unseal(ctx, "record", ref, uint64(generation), blob)
}
