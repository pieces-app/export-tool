package exporter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newAuditTestRun(t *testing.T) *run {
	t.Helper()
	return &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}, local: newLocalMeasurements()}
}

func TestAuditReuseRequiresIdenticalBytes(t *testing.T) {
	r := newAuditTestRun(t)
	secret := fakeSecret()
	path := auditFixtureFile(t, r, "document.md", strings.Repeat("a", len(secret)))
	auditFixtureFile(t, r, "record.json", `{"text":"Approved structured content"}`)
	for i := 0; i < 2; i++ {
		if err := r.auditOutput(); err != nil {
			t.Fatal(err)
		}
	}
	s := r.local.snapshot()
	if s["audit_content_scan"].Calls != 2 || s["audit_reused"].Calls != 2 || s["audit_hash_read"].Calls != 2 {
		t.Fatal("unchanged files were not checksum-verified and reused", s)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err == nil || !strings.Contains(err.Error(), "content requiring review") {
		t.Fatal("same-size, same-time modification bypassed privacy scanning", err)
	}
	if r.auditCache != nil {
		t.Fatal("failed traversal retained reuse evidence")
	}
}

func TestAuditReuseInvalidatesChangedScannerInputs(t *testing.T) {
	for _, kind := range []string{"known", "email_policy", "domain_policy", "domain_list", "new_scanner"} {
		t.Run(kind, func(t *testing.T) {
			r := newAuditTestRun(t)
			auditFixtureFile(t, r, "content.md", "ordinary-late-label synthetic.person@example.test https://private.example/page")
			if err := r.auditOutput(); err != nil {
				t.Fatal(err)
			}
			s := r.opts.Scanner
			switch kind {
			case "known":
				s.remember("ordinary-late-label")
			case "email_policy":
				s.Policy.Emails = true
			case "domain_policy":
				s.Policy.Deny = []DomainRule{{"private.example", true}}
			case "domain_list":
				s.domains = [][]string{{"private.example"}}
			case "new_scanner":
				r.opts.Scanner = scanner(t, DefaultPolicy())
			}
			err := r.auditOutput()
			if kind == "new_scanner" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "content requiring review") {
				t.Fatal("changed scanner input reused a prior result", kind, err)
			}
			stats := r.local.snapshot()
			if stats["audit_cache_reset"].Calls != 1 || stats["audit_reused"].Calls != 0 || stats["audit_content_scan"].Calls != 2 {
				t.Fatal("scanner invalidation did not perform a fresh scan", stats)
			}
		})
	}
}

func TestAuditFingerprintPreservesRawCredentialBytes(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	s.remember("raw-credential-\xff")
	a, err := auditPolicyFingerprint(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	s.known = map[string]bool{"raw-credential-\xfe": true}
	b, err := auditPolicyFingerprint(context.Background(), s)
	if err != nil || a == b {
		t.Fatal("fingerprint replaced distinct invalid UTF-8 credential bytes", err)
	}
	s.known = map[string]bool{"first": true, "second": true}
	a, _ = auditPolicyFingerprint(context.Background(), s)
	s.known = map[string]bool{"second": true, "first": true}
	b, _ = auditPolicyFingerprint(context.Background(), s)
	if a != b {
		t.Fatal("map iteration order changed the fingerprint")
	}
}

func TestAuditReuseNewAndMalformedFiles(t *testing.T) {
	r := newAuditTestRun(t)
	auditFixtureFile(t, r, "first.md", "Approved.")
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	path := auditFixtureFile(t, r, "second.json", `{"value":"Approved"}`)
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	stats := r.local.snapshot()
	if stats["audit_reused"].Calls != 1 || stats["audit_content_scan"].Calls != 2 {
		t.Fatal("new file escaped scanning", stats)
	}
	if err := os.WriteFile(path, []byte(`{"unfinished":[`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatal("changed JSON bypassed parsing", err)
	}
	if r.auditCache != nil {
		t.Fatal("invalid JSON left a reusable traversal")
	}
}

func TestAuditReuseStillChecksPathsPresenceAndCancellation(t *testing.T) {
	for _, action := range []string{"rename_private", "remove", "symlink", "cancel"} {
		t.Run(action, func(t *testing.T) {
			r := newAuditTestRun(t)
			path := auditFixtureFile(t, r, "approved.md", "Approved document.")
			if err := r.auditOutput(); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "rename_private":
				if err := os.Rename(path, filepath.Join(r.stage, fakeSecret()+".md")); err != nil {
					t.Fatal(err)
				}
			case "remove":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside.md")
				if err := os.Rename(path, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Skip("symlink creation unavailable")
				}
			case "cancel":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				r.ctx = ctx
			}
			err := r.auditOutput()
			if err == nil || action == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("invalid second traversal was accepted", action, err)
			}
			if r.auditCache != nil {
				t.Fatal("invalid traversal kept cached approvals")
			}
		})
	}
}

func TestAuditCacheLimitFallsBackToScanning(t *testing.T) {
	r := newAuditTestRun(t)
	auditFixtureFile(t, r, "first.md", "Approved.")
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	r.auditCache.keyBytes = maxAuditCacheKeyBytes
	auditFixtureFile(t, r, "second.json", `{"text":"Approved"}`)
	for i := 0; i < 2; i++ {
		if err := r.auditOutput(); err != nil {
			t.Fatal(err)
		}
	}
	s := r.local.snapshot()
	if len(r.auditCache.entries) != 1 || s["audit_reused"].Calls != 2 || s["audit_content_scan"].Calls != 3 {
		t.Fatal("cache budget disabled auditing instead of reuse", s)
	}
}

func TestAuditReuseKeepsPDFSemanticLinkChecks(t *testing.T) {
	r := newAuditTestRun(t)
	r.opts.Mode = "preserve"
	auditFixtureFile(t, r, "index.md", "[Target](target.md)")
	auditFixtureFile(t, r, "target.md", "# Target")
	if err := r.renderPDFs(); err != nil {
		t.Fatal(err)
	}
	r.opts.Mode = "filtered"
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	for name := range r.auditCache.entries {
		if strings.HasSuffix(name, ".pdf") {
			t.Fatal("PDF semantic results must not be cached")
		}
	}
	if err := os.Remove(filepath.Join(r.stage, "pdf", "target.pdf")); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err == nil || !strings.Contains(err.Error(), "PDF link target is missing") {
		t.Fatal("unchanged PDF skipped target validation", err)
	}
}
