package exporter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicitly opt-in, read-only performance comparison. No OS client, source
// cache, renderer, metadata writer or finalization is constructed here. This
// scanner cannot reconstruct credentials learned by the original exporter;
// therefore passing is not a fresh privacy/completeness certification.
func TestLiveAuditUnchangedArchive(t *testing.T) {
	source := os.Getenv("PIECES_EXPORT_LIVE_AUDIT_ARCHIVE")
	if source == "" {
		t.Skip("set PIECES_EXPORT_LIVE_AUDIT_ARCHIVE to a finalized default-filter archive for a read-only comparison")
	}
	manifest, err := InspectArchive(source)
	if err != nil || manifest.Mode != "filtered" {
		t.Fatal("audit comparison requires a finalized filtered archive")
	}
	s, err := NewScanner(DefaultPolicy(), "")
	if err != nil || manifest.PolicyHash != s.Hash || len(manifest.CategoryHashes) != 0 {
		t.Fatal("audit comparison requires the default privacy policy")
	}
	// Resolve the explicitly selected finalized directory once; the traversal
	// itself rejects symlinks and does not follow a .partial directory.
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal("finalized archive cannot be resolved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	r := &run{ctx: ctx, stage: source, opts: Options{Mode: "filtered", Scanner: s}, local: newLocalMeasurements()}
	for _, phase := range []string{"full_scan", "checksum_reuse"} {
		before := r.local.snapshot()
		started := time.Now()
		if err := r.auditOutput(); err != nil {
			// Do not expose filesystem paths or record-derived error text.
			t.Fatal("read-only audit comparison failed during", phase)
		}
		delta := map[string]OperationMeasurement{}
		addMeasurements(delta, r.local.snapshot(), before)
		report := struct {
			Phase        string                          `json:"phase"`
			Milliseconds int64                           `json:"milliseconds"`
			Operations   map[string]OperationMeasurement `json:"operations"`
			CachedFiles  int                             `json:"cached_files"`
			PathBytes    int                             `json:"cached_path_bytes"`
		}{phase, time.Since(started).Milliseconds(), delta, len(r.auditCache.entries), r.auditCache.keyBytes}
		b, _ := json.Marshal(report)
		t.Log(string(b))
	}
	t.Log("Read-only phase comparison; original learned credentials are unavailable, so this is not privacy recertification or complete-source acceptance.")
}
