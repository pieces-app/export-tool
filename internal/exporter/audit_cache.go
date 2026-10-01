package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"hash"
	"sort"
	"sync"
)

// Private, process-local acceleration, never reconstruction/recovery evidence.
// Hitting either limit simply leaves later files on the ordinary audit path.
const maxAuditCacheEntries = 500000
const maxAuditCacheKeyBytes = 64 << 20

type auditedContent struct {
	digest [32]byte
	pass   uint64
}

type outputAuditCache struct {
	mu       sync.Mutex // entries/keyBytes; traversal identity changes only after workers join
	scanner  *Scanner
	policy   [32]byte
	entries  map[string]auditedContent
	keyBytes int
	pass     uint64
}

// Fingerprint actual in-memory policy/domain/credential values, not just the
// original configuration hash or filenames. Known secrets can contain invalid
// UTF-8, so they must be hashed as length-prefixed bytes, not JSON strings.
// The digest and cached paths never leave this private in-memory structure.
func auditPolicyFingerprint(ctx context.Context, s *Scanner) ([32]byte, error) {
	var result [32]byte
	if err := ctx.Err(); err != nil {
		return result, err
	}
	h := sha256.New()
	write := func(value string) { hashAuditPart(h, value) }
	write("pieces-export/output-audit-cache/v1")
	b, err := json.Marshal(s.Policy)
	if err != nil {
		return result, err
	}
	write(string(b))
	hashAuditCount(h, uint64(len(s.domains)))
	for _, group := range s.domains {
		hashAuditCount(h, uint64(len(group)))
		for _, domain := range group {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			write(domain)
		}
	}
	write("known-credentials")
	keys := make([]string, 0, len(s.known))
	for key := range s.known {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hashAuditCount(h, uint64(len(keys)))
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		write(key)
	}
	copy(result[:], h.Sum(nil))
	return result, nil
}

func hashAuditPart(h hash.Hash, value string) {
	hashAuditCount(h, uint64(len(value)))
	h.Write([]byte(value))
}

func hashAuditCount(h hash.Hash, value uint64) {
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], value)
	h.Write(length[:])
}

func (r *run) beginOutputAudit() (*outputAuditCache, error) {
	fingerprint, err := auditPolicyFingerprint(r.ctx, r.opts.Scanner)
	if err != nil {
		return nil, err
	}
	c := r.auditCache
	if c == nil || c.scanner != r.opts.Scanner || c.policy != fingerprint || c.pass == ^uint64(0) {
		if c != nil {
			r.local.record("audit_cache_reset", 0, 0, nil)
		}
		c = &outputAuditCache{scanner: r.opts.Scanner, policy: fingerprint, entries: map[string]auditedContent{}}
		r.auditCache = c
	}
	c.pass++
	return c, nil
}

func (c *outputAuditCache) remember(path string, digest [32]byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[path]; !exists {
		if len(c.entries) >= maxAuditCacheEntries || len(path) > maxAuditCacheKeyBytes-c.keyBytes {
			return
		}
		c.keyBytes += len(path)
	}
	c.entries[path] = auditedContent{digest: digest, pass: c.pass}
}

func (c *outputAuditCache) lookup(path string) (auditedContent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	previous, ok := c.entries[path]
	return previous, ok
}
