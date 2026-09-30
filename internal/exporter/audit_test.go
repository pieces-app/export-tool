package exporter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func auditFixtureFile(t *testing.T, r *run, name, text string) string {
	t.Helper()
	path := filepath.Join(r.stage, name)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAuditTextChunkBoundaries(t *testing.T) {
	for name, sensitive := range map[string]string{
		"credential":  fakeSecret(),
		"denied_host": "https://private.bank.example/account",
		"email":       "synthetic.person@example.test",
	} {
		t.Run(name, func(t *testing.T) {
			p := DefaultPolicy()
			p.Emails = true
			p.Deny = []DomainRule{{Domain: "bank.example", Subdomains: true}}
			r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, p)}}
			// The complete value is visible only after the next read's overlap.
			prefix := strings.Repeat(" ", auditReadBytes-len(sensitive)/2)
			path := auditFixtureFile(t, r, "boundary.md", prefix+sensitive+"\nApproved suffix.\n")
			if err := r.auditOutputFile(path); err == nil || !strings.Contains(err.Error(), "content requiring review") {
				t.Fatalf("chunk-boundary %s was not detected: %v", name, err)
			}
		})
	}
}

func TestAuditScratchDoesNotCombineFilesOrScanStaleBytes(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Scanner: scanner(t, DefaultPolicy())}}
	a := outputAuditor{run: r}
	secret := fakeSecret()
	first := auditFixtureFile(t, r, "a.md", strings.Repeat("Approved.\n", 1000)+secret[:len(secret)/2])
	second := auditFixtureFile(t, r, "b.md", secret[len(secret)/2:])
	for _, file := range []string{first, second} {
		if err := a.file(file); err != nil {
			t.Fatal("separate harmless fragments were combined across file boundaries")
		}
	}
	leaked := auditFixtureFile(t, r, "c.md", strings.Repeat(" ", 5000)+secret)
	if err := a.file(leaked); err == nil {
		t.Fatal("complete credential was not detected")
	}
	// Reuse scratch after a rejected long file: bytes beyond a shorter read,
	// and all bytes after an empty read, belong only to that earlier file.
	for _, text := range []string{"Approved short document.", ""} {
		if err := a.file(auditFixtureFile(t, r, "d.md", text)); err != nil {
			t.Fatalf("stale scratch bytes contaminated the next document: %v", err)
		}
	}
}

func TestAuditDecodedJSONAndPDFDispatch(t *testing.T) {
	var escaped strings.Builder
	for _, char := range fakeSecret() {
		fmt.Fprintf(&escaped, "\\u%04x", char)
	}
	for _, tc := range []struct{ name, content, reason string }{
		{"escaped.json", `{"value":"` + escaped.String() + `"}`, "content requiring review"},
		{"numeric.json", `{"value":4111111111111111}`, "content requiring review"},
		{"later.jsonl", "{\"value\":\"Approved\"}\n{\"value\":\"" + escaped.String() + "\"}\n", "content requiring review"},
		{"invalid.json", "[}", "invalid JSON"},
		{"invalid.pdf", "This is not a PDF document.", "PDF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
			auditFixtureFile(t, r, "a.md", "Approved document, processed before structured files.")
			auditFixtureFile(t, r, tc.name, tc.content)
			if err := r.auditOutput(); err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("structured output bypassed decoding or failed for the wrong reason: %v", err)
			}
		})
	}
	// A valid PDF with sensitive text also must take the semantic PDF path.
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "preserve", Scanner: scanner(t, DefaultPolicy())}}
	if _, err := r.writePDF("source.md", "leaked.pdf", []byte("# Synthetic fixture\n\n"+fakeSecret()), nil); err != nil {
		t.Fatal(err)
	}
	r.opts.Mode = "filtered"
	if err := r.auditOutput(); err == nil || !strings.Contains(err.Error(), "content requiring review") {
		t.Fatalf("semantic PDF audit did not detect leaked text: %v", err)
	}
}

func TestAuditCancellationPathsAndSymlinks(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		r := &run{ctx: ctx, stage: t.TempDir(), opts: Options{Scanner: scanner(t, DefaultPolicy())}}
		cancel()
		if !errors.Is(r.auditOutput(), context.Canceled) {
			t.Fatal("empty canceled traversal did not stop")
		}
		if !errors.Is(r.auditOutputFile(filepath.Join(r.stage, "missing.md")), context.Canceled) {
			t.Fatal("canceled audit accessed a file before stopping")
		}
	})
	t.Run("pathname", func(t *testing.T) {
		r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Scanner: scanner(t, DefaultPolicy())}}
		auditFixtureFile(t, r, fakeSecret()+".md", "Approved body.")
		if err := r.auditOutput(); err == nil {
			t.Fatal("path privacy checks were skipped")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Scanner: scanner(t, DefaultPolicy())}}
		outside := filepath.Join(t.TempDir(), "outside.md")
		if err := os.WriteFile(outside, []byte("Approved body."), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(r.stage, "link.md")); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		if err := r.auditOutput(); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatal("output traversal followed or ignored a symlink")
		}
	})
}

func TestAuditJSONBufferedReaderResetsAfterFailures(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	a := outputAuditor{run: r}
	secret := fakeSecret()
	for _, tc := range []struct{ text, reason string }{
		{`{"early":"` + secret + `","buffered":"` + secret + `"}`, "content requiring review"},
		{`{"unfinished":["Approved"`, "invalid JSON"},
		{`{"duplicate":"` + secret + `","duplicate":"Approved"}`, "content requiring review"},
	} {
		bad := auditFixtureFile(t, r, "rejected.json", tc.text)
		if err := a.file(bad); err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Fatalf("expected rejected token stream (%s): %v", tc.reason, err)
		}
		good := auditFixtureFile(t, r, "approved.json", `{"value":"Approved"}`)
		if err := a.file(good); err != nil {
			t.Fatalf("prior buffered bytes or decoder state contaminated the next file: %v", err)
		}
		if err := a.file(auditFixtureFile(t, r, "empty.jsonl", "")); err != nil {
			t.Fatalf("empty JSONL stream inherited buffered values: %v", err)
		}
	}
	// This string crosses an underlying buffered-reader refill, independently
	// of the smaller internal buffers used by the JSON token decoder.
	head := `{"value":"`
	padding := strings.Repeat(" ", auditJSONReadBytes-len(head)-len(secret)/2)
	path := auditFixtureFile(t, r, "boundary.json", head+padding+secret+`"}`)
	if err := a.file(path); err == nil || !strings.Contains(err.Error(), "content requiring review") {
		t.Fatalf("buffered-reader boundary concealed a decoded credential: %v", err)
	}
}

func TestAuditJSONRequiresCompleteValues(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Scanner: scanner(t, DefaultPolicy())}}
	a := outputAuditor{run: r}
	for _, malformed := range []string{
		`{`, `[`, `{"key"`, `{"key":`, `{"key":1`, `{"key":1,`, `[1`, `[1,`,
		`{"nested":[1]`, `[{"key":1}`, `"unterminated`, `tru`, `1e`,
		"{}\n{\"unfinished\":[", `{"key":[}`, `{} unexpected`,
	} {
		if err := a.file(auditFixtureFile(t, r, "invalid.jsonl", malformed)); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
			t.Fatalf("incomplete/invalid value %q was accepted or failed for the wrong reason: %v", malformed, err)
		}
	}
	for _, complete := range []string{
		"", " \n\t", `{}`, `[]`, `{"key":[1,{"nested":true},null]}`,
		"{}\n[1]\n\"Approved\"\ntrue\nnull\n1\n",
	} {
		if err := a.file(auditFixtureFile(t, r, "valid.jsonl", complete)); err != nil {
			t.Fatalf("complete JSONL stream %q rejected: %v", complete, err)
		}
	}
}
