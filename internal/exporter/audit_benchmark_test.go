package exporter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Distinct summary-like Markdown and decoded JSON, with a cold audit cache on
// every iteration. All worker counts scan identical content under the same
// policy. Fixture creation and scanner initialization are outside the timer.
func BenchmarkFinalAuditWorkerCounts(b *testing.B) {
	for _, workers := range []int{1, 2, 4} {
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			scanner, err := NewScanner(DefaultPolicy(), "")
			if err != nil {
				b.Fatal(err)
			}
			r := &run{ctx: context.Background(), stage: b.TempDir(), opts: Options{Scanner: scanner, FileWorkers: workers}}
			var bytes int64
			for i := 0; i < 128; i++ {
				var document strings.Builder
				fmt.Fprintf(&document, "# Synthetic summary %d\n\n", i)
				document.WriteString(strings.Repeat("An ordinary paragraph describes the project discussion and follow-up work.\n\n", 64))
				for j := 0; j < 200; j++ {
					fmt.Fprintf(&document, "- [Related summary %d with useful project context](../timeline/summary-%06d.fixture.2026-10-01.md)\n", j, i*200+j)
				}
				for _, ext := range []string{"md", "json"} {
					body := []byte(document.String())
					if ext == "json" {
						body, err = json.Marshal(map[string]any{"id": fmt.Sprintf("synthetic-%04d", i), "text": document.String(), "type": "SUMMARY"})
						if err != nil {
							b.Fatal(err)
						}
					}
					if err := os.WriteFile(filepath.Join(r.stage, fmt.Sprintf("document-%04d.%s", i, ext)), body, 0600); err != nil {
						b.Fatal(err)
					}
					bytes += int64(len(body))
				}
			}
			b.ReportAllocs()
			b.SetBytes(bytes)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.auditCache = nil
				if err := r.auditOutput(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type auditReadCounter struct {
	io.Reader
	calls int
}

func (r *auditReadCounter) Read(b []byte) (int, error) {
	r.calls++
	return r.Reader.Read(b)
}

// Bounded synthetic files only. The measured work is the ordinary final audit,
// including pathname/content privacy checks and directory traversal. Fixture
// creation, scanner initialization, rendering and cleanup are excluded.
func BenchmarkFinalAuditSmallDocuments(b *testing.B) {
	for _, count := range []int{64, 512} {
		b.Run(fmt.Sprintf("files_%d", count), func(b *testing.B) {
			scanner, err := NewScanner(DefaultPolicy(), "")
			if err != nil {
				b.Fatal(err)
			}
			r := &run{ctx: context.Background(), stage: b.TempDir(), opts: Options{Scanner: scanner}}
			body := []byte("# Synthetic summary\n\n" + strings.Repeat("Approved local document content.\n", 64))
			for i := 0; i < count; i++ {
				if err := os.WriteFile(filepath.Join(r.stage, fmt.Sprintf("document-%04d.md", i)), body, 0600); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.SetBytes(int64(count * len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.auditCache = nil // Measure a full scan, never an implicit warm pass.
				if err := r.auditOutput(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// JSON deliberately varies identities/titles while keeping ordinary field
// names, timestamps and enum values. This profiles decoded-token audit work;
// the raw-text buffer optimization does not apply to this workload.
func BenchmarkFinalAuditJSONRecords(b *testing.B) {
	scanner, err := NewScanner(DefaultPolicy(), "")
	if err != nil {
		b.Fatal(err)
	}
	r := &run{ctx: context.Background(), stage: b.TempDir(), opts: Options{Scanner: scanner}}
	var totalBytes int64
	for i := 0; i < 512; i++ {
		record := map[string]any{
			"id": fmt.Sprintf("synthetic-%04d", i), "name": fmt.Sprintf("Synthetic document %04d", i),
			"type": "WORKSTREAM_EVENT", "created": map[string]any{"value": "2026-09-30T00:00:00Z"},
			"updated":  map[string]any{"value": "2026-09-30T12:00:00Z"},
			"websites": map[string]any{"indices": map[string]int{}, "iterable": []any{}},
			"text":     "Approved synthetic record with ordinary structured fields.",
		}
		body, err := json.Marshal(record)
		if err != nil {
			b.Fatal(err)
		}
		totalBytes += int64(len(body))
		if err := os.WriteFile(filepath.Join(r.stage, fmt.Sprintf("record-%04d.json", i)), body, 0600); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.SetBytes(totalBytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.auditCache = nil
		if err := r.auditOutput(); err != nil {
			b.Fatal(err)
		}
	}
}

// Compare full scans with byte-verified reuse of the same mixed output. The
// prerequisite successful scan is outside the reuse timer. This is a local
// phase comparison, not a whole-export estimate or a replacement for live data.
func BenchmarkAuditUnchangedOutput(b *testing.B) {
	for _, reuse := range []bool{false, true} {
		b.Run(fmt.Sprintf("reuse_%t", reuse), func(b *testing.B) {
			scanner, err := NewScanner(DefaultPolicy(), "")
			if err != nil {
				b.Fatal(err)
			}
			r := &run{ctx: context.Background(), stage: b.TempDir(), opts: Options{Scanner: scanner}}
			var total int64
			for i := 0; i < 256; i++ {
				for _, ext := range []string{"md", "json"} {
					body := []byte("# Synthetic document\n\n" + strings.Repeat("Ordinary approved document content.\n", 64))
					if ext == "json" {
						body, err = json.Marshal(map[string]any{"id": fmt.Sprintf("synthetic-%04d", i), "text": string(body), "tags": []string{"synthetic", "approved"}})
						if err != nil {
							b.Fatal(err)
						}
					}
					if err := os.WriteFile(filepath.Join(r.stage, fmt.Sprintf("document-%04d.%s", i, ext)), body, 0600); err != nil {
						b.Fatal(err)
					}
					total += int64(len(body))
				}
			}
			if reuse {
				if err := r.auditOutput(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.SetBytes(total)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !reuse {
					r.auditCache = nil
				}
				if err := r.auditOutput(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// File-backed token reading isolates the proposed I/O change. Underlying read
// counts include EOF probes; this excludes policy scans and is not export ETA.
func BenchmarkAuditJSONTokenReads(b *testing.B) {
	row := []byte(`{"id":"synthetic","created":{"value":"2026-09-30T00:00:00Z"},"type":"WORKSTREAM_EVENT","name":"Approved synthetic document"}` + "\n")
	for _, count := range []int{1, 512} {
		for _, buffered := range []bool{false, true} {
			b.Run(fmt.Sprintf("rows_%d/buffered_%t", count, buffered), func(b *testing.B) {
				path := filepath.Join(b.TempDir(), "synthetic.jsonl")
				if err := os.WriteFile(path, bytes.Repeat(row, count), 0600); err != nil {
					b.Fatal(err)
				}
				var calls int64
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					f, err := os.Open(path)
					if err != nil {
						b.Fatal(err)
					}
					counter := &auditReadCounter{Reader: f}
					var input io.Reader = counter
					if buffered {
						input = bufio.NewReaderSize(counter, auditJSONReadBytes)
					}
					decoder := json.NewDecoder(input)
					for {
						_, err := decoder.Token()
						if err != nil {
							f.Close()
							if err != io.EOF {
								b.Fatal(err)
							}
							break
						}
					}
					calls += int64(counter.calls)
				}
				b.StopTimer()
				b.ReportMetric(float64(calls)/float64(b.N), "reads/op")
			})
		}
	}
}
