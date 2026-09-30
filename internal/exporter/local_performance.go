package exporter

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const localPerformanceFile = "performance.json"

// Measurements retain only fixed operation names, counts, bytes and durations.
// Write time includes sync time; operation totals must not be summed as wall time.
type OperationMeasurement struct {
	Calls        int64 `json:"calls"`
	Failures     int64 `json:"failures"`
	Bytes        int64 `json:"bytes"`
	Milliseconds int64 `json:"milliseconds"`
}

type PhaseMeasurement struct {
	Name               string                          `json:"name"`
	Visits             int64                           `json:"visits"`
	Milliseconds       int64                           `json:"milliseconds"`
	Processed          int64                           `json:"processed"`
	KnownTotal         int64                           `json:"known_total"`
	UnknownTotalVisits int64                           `json:"unknown_total_visits"`
	Operations         map[string]OperationMeasurement `json:"operations"`
}

type LocalPerformanceReport struct {
	Version     int                             `json:"version"`
	State       string                          `json:"state"`
	Boundary    string                          `json:"measurement_boundary"`
	ElapsedMS   int64                           `json:"elapsed_ms"`
	Operations  map[string]OperationMeasurement `json:"operations"`
	Phases      []PhaseMeasurement              `json:"phases"`
	HTTP        PerformanceStats                `json:"http"`
	Limitations []string                        `json:"limitations"`
}

type operationTotal struct {
	OperationMeasurement
	duration time.Duration
}

type localMeasurements struct {
	mu      sync.Mutex
	started time.Time
	totals  map[string]operationTotal
}

func newLocalMeasurements() *localMeasurements {
	return &localMeasurements{started: time.Now(), totals: map[string]operationTotal{}}
}

func (m *localMeasurements) record(operation string, elapsed time.Duration, bytes int64, err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.totals[operation]
	v.Calls++
	if err != nil {
		v.Failures++
	}
	v.Bytes += bytes
	v.duration += elapsed
	m.totals[operation] = v
}

func (m *localMeasurements) snapshot() map[string]OperationMeasurement {
	out := map[string]OperationMeasurement{}
	if m == nil {
		return out
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, total := range m.totals {
		v := total.OperationMeasurement
		v.Milliseconds = total.duration.Milliseconds()
		out[name] = v
	}
	return out
}

func addMeasurements(to, values, subtract map[string]OperationMeasurement) {
	for name, value := range values {
		before, current := subtract[name], to[name]
		current.Calls += value.Calls - before.Calls
		current.Failures += value.Failures - before.Failures
		current.Bytes += value.Bytes - before.Bytes
		current.Milliseconds += value.Milliseconds - before.Milliseconds
		to[name] = current
	}
}

func (m *localMeasurements) syncFile(f *os.File) error {
	started := time.Now()
	err := f.Sync()
	m.record("artifact_sync", time.Since(started), 0, err)
	return err
}

func (r *run) writeFile(path string, b []byte) error {
	return writeFileMeasured(path, b, r.local)
}

func (r *run) writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return r.writeFile(path, append(b, '\n'))
}

func (r *run) rewriteJSON(path string, v any) error {
	if err := r.writeJSON(path+".tmp", v); err != nil {
		return err
	}
	started := time.Now()
	err := os.Rename(path+".tmp", path)
	r.local.record("canonical_rename", time.Since(started), 0, err)
	return err
}

type countedReader struct {
	io.Reader
	bytes int64
}

func (r *countedReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	r.bytes += int64(n)
	return n, err
}

type countedWriter struct {
	io.Writer
	bytes int64
}

func (w *countedWriter) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (r *run) readRecord(path string) (v map[string]any, err error) {
	started := time.Now()
	reader := &countedReader{}
	defer func() { r.local.record("canonical_json_read", time.Since(started), reader.bytes, err) }()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader.Reader = f
	d := json.NewDecoder(reader)
	d.UseNumber()
	err = d.Decode(&v)
	return v, err
}

func (r *run) sanitizeRecord(v map[string]any) (map[string]any, ScanResult, error) {
	started := time.Now()
	clean, stats, err := r.opts.Scanner.Sanitize(r.ctx, v)
	r.local.record("record_scan", time.Since(started), 0, err)
	return clean, stats, err
}

// The report is diagnostic, not a resume checkpoint or success certificate.
// Failed-run output is best effort, fixed-schema and contains no error strings.
func (r *run) performanceReport(state string) *LocalPerformanceReport {
	if r.local == nil {
		return nil
	}
	report := &LocalPerformanceReport{
		Version: 1, State: state,
		Boundary:  "before diagnostic/final-manifest writes and directory finalization",
		ElapsedMS: time.Since(r.local.started).Milliseconds(), Operations: r.local.snapshot(), Phases: r.progress.measurements(),
		Limitations: []string{
			"Phase durations are wall time; operation durations overlap (artifact writes include sync and streamed encoding/hash work). Do not sum them as CPU or elapsed time.",
			"Canonical JSON reads/scans and generated artifact writes/syncs are measured. Hashing, archive/cache/PDF/audit reads, directory operations and native property writes are not individual operation counters; their phase wall time remains included.",
			"Repeated phase names are aggregated; processed/known-total counts are work items, not distinct source records. Unknown totals stay unknown.",
			"Diagnostic/final-manifest writes, final directory rename and preflight before staging are excluded. This file cannot establish archive completeness or resume a partial export.",
			"HTTP counters cover the client lifetime, which can include preflight. Logical bytes are bytes read/written through these helpers, not physical disk traffic or unique archive size.",
		},
	}
	if r.client != nil {
		report.HTTP = r.client.Performance()
	}
	return report
}

func (r *run) writePerformanceReport(report *LocalPerformanceReport) error {
	if report == nil {
		return nil
	}
	path := filepath.Join(r.stage, localPerformanceFile)
	var err error
	if r.performanceWritten {
		err = rewriteJSON(path, report)
	} else {
		err = writeJSON(path, report)
	}
	if err == nil {
		r.performanceWritten = true
	}
	return err
}

func (r *run) closeMeasurements() {
	if !r.finalized {
		// Never overwrite an unrelated preexisting report on a failed first write.
		_ = r.writePerformanceReport(r.performanceReport("stopped_before_finalization"))
	}
}
