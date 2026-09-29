package exporter

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Final defense against values accidentally restored by a renderer or metadata writer.
// Overlapping bounded chunks avoid loading the entire exported timeline into memory.
func (r *run) auditOutput() error {
	return filepath.WalkDir(r.stage, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errConfig("unexpected symlink in export output")
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
			decoder := json.NewDecoder(f)
			decoder.UseNumber()
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return errConfig("final output contains invalid JSON")
				}
				switch value := token.(type) {
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
		buffer := make([]byte, 1<<20)
		tail := ""
		for {
			n, readErr := f.Read(buffer)
			if n > 0 {
				chunk := tail + string(buffer[:n])
				if err := r.auditText(chunk); err != nil {
					return err
				}
				start := max(0, len(chunk)-4096)
				tail = chunk[start:]
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	})
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
