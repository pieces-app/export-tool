package exporter

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const auditReadBytes = 1 << 20
const auditOverlapBytes = 4096
const auditJSONReadBytes = 32 << 10

// One auditor belongs to one sequential traversal. Allocate I/O buffers lazily
// and reuse them only within that traversal; JSON/PDF keep fresh decoders.
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
	auditor := outputAuditor{run: r, cache: cache}
	if err := filepath.WalkDir(r.stage, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errConfig("unexpected symlink in export output")
		}
		if err := auditor.file(path); err != nil {
			return err
		}
		r.progress.Add(1)
		return nil
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
		if previous, ok := a.cache.entries[relative]; ok {
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
				if err := r.auditText(value); err != nil {
					return err
				}
			case json.Number:
				if err := r.auditText(string(value)); err != nil {
					return err
				}
			}
		}
	}
	if a.buffer == nil {
		a.buffer = make([]byte, auditReadBytes)
	}
	tail := ""
	for {
		n, readErr := counter.Read(a.buffer)
		if n > 0 {
			// Only the newly read bytes belong to this file. The rest of the
			// buffer can retain arbitrary bytes from a prior, longer document.
			chunk := tail + string(a.buffer[:n])
			if err := r.auditText(chunk); err != nil {
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

func (r *run) auditText(text string) error {
	stats := ScanResult{}
	_, err := r.opts.Scanner.cleanString(r.ctx, "", text, &stats)
	if err != nil {
		return err
	}
	if stats.Redactions > 0 || stats.Denied {
		return errConfig("final output scan found content requiring review; partial directory was not finalized")
	}
	return nil
}
