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
		if err := r.auditOutput(); err != nil {
			b.Fatal(err)
		}
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
