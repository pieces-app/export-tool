package exporter

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// Bounded synthetic I/O only: no OS client, real archive, policy change, or
// disabled Sync. Use -benchtime=1x; longer automatic calibration is unnecessary
// while a live migration shares the filesystem. TempDir cleanup is untimed.
func BenchmarkDurableArtifactWrites(b *testing.B) {
	const files = 128
	for _, size := range []int{4096, 65536} {
		for _, workers := range []int{1, 2, 4} {
			b.Run(fmt.Sprintf("bytes_%d/workers_%d", size, workers), func(b *testing.B) {
				root := b.TempDir()
				body := bytes.Repeat([]byte("Synthetic export content.\n"), size/26+1)[:size]
				paths := make([]string, b.N*files)
				for i := range paths {
					paths[i] = filepath.Join(root, fmt.Sprintf("record-%08d.md", i))
				}
				b.ReportAllocs()
				b.SetBytes(int64(files * size))
				b.ResetTimer()
				for iteration := 0; iteration < b.N; iteration++ {
					w := newMarkdownWriter(context.Background(), workers, maxAsyncMarkdownBytes, writeFile)
					for _, path := range paths[iteration*files : (iteration+1)*files] {
						if err := w.Submit(path, body, nil); err != nil {
							_ = w.Close()
							b.Fatal(err)
						}
					}
					if err := w.Close(); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(files*b.N)/b.Elapsed().Seconds(), "files/s")
			})
		}
	}
}

// Measures the actual Markdown renderer and its ordinary file syncs. Fixture
// creation, source hydration/privacy reconciliation, PDF, metadata application,
// final audit and finalization are outside this isolated stage measurement.
func BenchmarkStagedMarkdownWrites(b *testing.B) {
	const files = 128
	for _, workers := range []int{1, 2, 4} {
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			b.ReportAllocs()
			var syncCalls, syncMS int64
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				r := &run{ctx: context.Background(), stage: b.TempDir(), meta: map[string]*Meta{}, opts: Options{Mode: "preserve", Timezone: "UTC", Format: "markdown", Metadata: "off", Naming: "opaque", FileWorkers: workers}}
				for record := 0; record < files; record++ {
					putGraphFixtureRecord(b, r, "ANNOTATIONS", fmt.Sprintf("synthetic-%04d", record), "DESCRIPTION", map[string]any{"text": string(bytes.Repeat([]byte("Synthetic Markdown document. "), 128))})
				}
				r.local = newLocalMeasurements()
				r.progress = startMeasuredProgress(nil, nil, r.local)
				b.StartTimer()
				err := r.render()
				b.StopTimer()
				r.progress.Close()
				if err != nil {
					b.Fatal(err)
				}
				stats := r.local.snapshot()["artifact_sync"]
				syncCalls += stats.Calls
				syncMS += stats.Milliseconds
			}
			b.StopTimer()
			b.ReportMetric(float64(syncCalls)/float64(b.N), "syncs/op")
			b.ReportMetric(float64(syncMS)/float64(b.N), "sum_sync_ms/op")
			b.ReportMetric(float64(files*b.N)/b.Elapsed().Seconds(), "records/s")
		})
	}
}
