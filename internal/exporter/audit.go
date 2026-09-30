package exporter

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
}

// Final defense against values accidentally restored by a renderer or metadata writer.
// Overlapping bounded chunks avoid loading the entire exported timeline into memory.
func (r *run) auditOutput() error {
	r.progress.Stage("Audit filtered output", 0)
	auditor := outputAuditor{run: r}
	return filepath.WalkDir(r.stage, func(path string, d fs.DirEntry, walkErr error) error {
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
	})
}

func (r *run) auditOutputFile(path string) error {
	auditor := outputAuditor{run: r}
	return auditor.file(path)
}

func (a *outputAuditor) file(path string) error {
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
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// Scan decoded JSON tokens so escaped ampersands/unicode are interpreted
	// as their actual content, including already-redacted URL query values.
	if ext := filepath.Ext(path); ext == ".json" || ext == ".jsonl" {
		// Token decoders can request many small reads. Refill from the file
		// in bounded blocks, but create a fresh decoder and reset all unread
		// buffered bytes for every file, including after a prior audit error.
		if a.jsonReader == nil {
			a.jsonReader = bufio.NewReaderSize(f, auditJSONReadBytes)
		} else {
			a.jsonReader.Reset(f)
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
		n, readErr := f.Read(a.buffer)
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
