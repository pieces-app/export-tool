package exporter

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const auditReadBytes = 1 << 20
const auditOverlapBytes = 4096
const auditJSONReadBytes = 32 << 10

// One auditor belongs to one worker. Allocate I/O buffers lazily and reuse them
// only within that worker; JSON/PDF keep fresh decoders.
// File-local scan state (especially the overlap tail) must never live here.
type outputAuditor struct {
	run        *run
	buffer     []byte
	jsonReader *bufio.Reader
	hashBuffer []byte
	cache      *outputAuditCache
}

// Final defense against values accidentally restored by a renderer or metadata writer.
// Overlapping bounded chunks avoid loading the entire exported timeline into memory.
func (r *run) auditOutput() (result error) {
	defer func() {
		// A failed/canceled traversal never supplies reuse evidence for a retry.
		if result != nil {
			r.auditCache = nil
		}
	}()
	r.progress.Stage("Audit filtered output", 0)
	cache, err := r.beginOutputAudit()
	if err != nil {
		return err
	}
	workers := r.opts.FileWorkers
	if workers == 0 {
		workers = defaultFileWorkers
	}
	if err := auditOutputFiles(r.ctx, r.stage, workers, func(ctx context.Context) func(string) error {
		worker := &run{ctx: ctx, stage: r.stage, opts: r.opts, local: r.local}
		worker.opts.Scanner = r.opts.Scanner.forkForAudit()
		auditor := outputAuditor{run: worker, cache: cache}
		return func(path string) error {
			if err := auditor.file(path); err != nil {
				return err
			}
			r.progress.Add(1)
			return nil
		}
	}); err != nil {
		return err
	}
	for _, entry := range cache.entries {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if entry.pass != cache.pass {
			return errConfig("previously audited output file is missing")
		}
	}
	return nil
}

func (r *run) auditOutputFile(path string) error {
	auditor := outputAuditor{run: r}
	return auditor.file(path)
}

func (a *outputAuditor) file(path string) (result error) {
	r := a.run
	if err := r.ctx.Err(); err != nil {
		return err
	}
	relative, _ := filepath.Rel(r.stage, path)
	defer func() {
		var review *outputReviewError
		if errors.As(result, &review) && review.path == "" {
			review.path = r.reviewPath(relative)
		}
	}()
	if err := r.auditText(relative); err != nil {
		return err
	}
	if filepath.Ext(path) == ".pdf" {
		return r.auditPDF(path)
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errConfig("output audit requires a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if a.cache != nil {
		if previous, ok := a.cache.lookup(relative); ok {
			digest, size, err := a.hashFile(f)
			if err != nil {
				return err
			}
			if previous.digest == digest {
				a.cache.remember(relative, digest)
				r.local.record("audit_reused", 0, size, nil)
				return nil
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
		}
	}
	// Hash exactly the same bytes consumed by the successful semantic scan.
	// A post-scan reopen/hash could accidentally approve different file bytes.
	h := sha256.New()
	counter := &countedReader{Reader: io.TeeReader(f, h)}
	started := time.Now()
	defer func() {
		r.local.record("audit_content_scan", time.Since(started), counter.bytes, result)
		if result == nil {
			var digest [32]byte
			copy(digest[:], h.Sum(nil))
			a.cache.remember(relative, digest)
		}
	}()
	// Scan decoded JSON tokens so escaped ampersands/unicode are interpreted
	// as their actual content, including already-redacted URL query values.
	if ext := filepath.Ext(path); ext == ".json" || ext == ".jsonl" {
		// Token decoders can request many small reads. Refill from the file
		// in bounded blocks, but create a fresh decoder and reset all unread
		// buffered bytes for every file, including after a prior audit error.
		if a.jsonReader == nil {
			a.jsonReader = bufio.NewReaderSize(counter, auditJSONReadBytes)
		} else {
			a.jsonReader.Reset(counter)
		}
		decoder := json.NewDecoder(a.jsonReader)
		decoder.UseNumber()
		// Scanner inputs are immutable during a file audit. Every input byte
		// still reaches the decoder and digest; only identical approved token
		// scans repeat less often. Never retain approvals in the worker itself.
		tokens := auditTokenCache{}
		depth := 0
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				// Token can return EOF while an object or array is still open.
				// A stream may contain several complete JSONL values, but no
				// value may end with an unfinished container.
				if depth != 0 {
					return errConfig("final output contains invalid JSON")
				}
				return nil
			}
			if err != nil {
				return errConfig("final output contains invalid JSON")
			}
			switch value := token.(type) {
			case json.Delim:
				switch value {
				case '{', '[':
					depth++
				case '}', ']':
					depth--
				}
			case string:
				if err := tokens.check(r.ctx, value, r.auditText); err != nil {
					return err
				}
			case json.Number:
				if err := tokens.check(r.ctx, string(value), r.auditText); err != nil {
					return err
				}
			}
		}
	}
	if a.buffer == nil {
		a.buffer = make([]byte, auditReadBytes)
	}
	scan := r.auditText
	if filepath.Ext(path) == ".md" {
		scan = r.auditMarkdown
	}
	tail := ""
	for {
		n, readErr := counter.Read(a.buffer)
		if n > 0 {
			// Only the newly read bytes belong to this file. The rest of the
			// buffer can retain arbitrary bytes from a prior, longer document.
			chunk := tail + string(a.buffer[:n])
			if err := scan(chunk); err != nil {
				return err
			}
			start := max(0, len(chunk)-auditOverlapBytes)
			tail = chunk[start:]
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (a *outputAuditor) hashFile(f *os.File) (digest [32]byte, bytes int64, result error) {
	started := time.Now()
	defer func() { a.run.local.record("audit_hash_read", time.Since(started), bytes, result) }()
	if a.hashBuffer == nil {
		a.hashBuffer = make([]byte, 128<<10)
	}
	h := sha256.New()
	for {
		if err := a.run.ctx.Err(); err != nil {
			return digest, bytes, err
		}
		n, err := f.Read(a.hashBuffer)
		bytes += int64(n)
		h.Write(a.hashBuffer[:n])
		if err == io.EOF {
			copy(digest[:], h.Sum(nil))
			return digest, bytes, nil
		}
		if err != nil {
			return digest, bytes, err
		}
	}
}

// auditMarkdown scans generated Markdown as a reader sees it. Generated titles
// escape "_" as "\_". The underscore is the only word character that Markdown
// escaping changes, and the added backslash would create word boundaries the
// record scan never saw, such as one that ends a 16-digit run in "4111…_final"
// for the payment card pattern.
func (r *run) auditMarkdown(text string) error {
	return r.auditText(strings.ReplaceAll(text, `\_`, "_"))
}

func (r *run) auditText(text string) error {
	stats := ScanResult{}
	_, err := r.opts.Scanner.cleanString(r.ctx, "", text, &stats)
	for i := 0; i < stats.TimeoutRetries; i++ {
		r.local.record("secret_scan_timeout_retry", 0, 0, err)
	}
	if err != nil {
		return err
	}
	if stats.Redactions > 0 || stats.Denied {
		return &outputReviewError{kinds: stats.Findings}
	}
	return nil
}

// outputReviewError reports a final-audit finding by output file and finding
// kind so that a failed export can be diagnosed from its error alone. It never
// carries the matched value.
type outputReviewError struct {
	path  string
	kinds []string
}

func (e *outputReviewError) Error() string {
	var b strings.Builder
	b.WriteString("final output scan found content requiring review")
	if e.path != "" {
		b.WriteString(" in ")
		b.WriteString(e.path)
	}
	if len(e.kinds) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(e.kinds, ", "))
		b.WriteString(")")
	}
	b.WriteString("; partial directory was not finalized")
	return b.String()
}

// reviewPath names an output file in an audit error. The name itself can be
// the finding, so report the scanner's cleaned form, never the raw name.
func (r *run) reviewPath(relative string) string {
	stats := ScanResult{}
	clean, err := r.opts.Scanner.cleanString(r.ctx, "", filepath.ToSlash(relative), &stats)
	if err != nil {
		return ""
	}
	return clean
}
