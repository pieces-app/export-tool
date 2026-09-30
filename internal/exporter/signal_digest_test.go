package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func signalDigestFixture() (*fakeOS, Policy) {
	a, b, old, ancient, undated, private := signalRecord("recent-a"), signalRecord("recent-b"), signalRecord("old"), signalRecord("ancient"), signalRecord("undated"), signalRecord("private")
	a["name"] = "Café project"
	b["created"] = map[string]any{"value": "2026-09-29T20:00:00-04:00"} // Same instant as a.
	old["created"], ancient["created"], undated["created"] = map[string]any{"value": "2020-01-01T00:00:00Z"}, map[string]any{"value": "0001-01-01T00:00:00Z"}, map[string]any{"value": "invalid"}
	for _, field := range projectionFields("SIGNALS") {
		delete(undated, field)
	}
	delete(b, "annotations")
	one, two, secret := record("description-one", "2026-09-30T00:00:00Z"), record("description-two", "2026-09-29T00:00:00Z"), record("private-body", "")
	one["type"], one["text"] = "SIGNAL_DESCRIPTION", "# Approved signal description\n\nWork with [Casey](pieces://persons/person) and [missing](pieces://unknown/absent).\n\nSynthetic credential: "+fakeSecret()
	two["type"], two["text"], two["signals"] = "SIGNAL_DESCRIPTION", "Earlier retained description.", refs("recent-b")
	secret["type"], secret["text"] = "SIGNAL_DESCRIPTION", "Private signal narrative sentinel"
	a["annotations"], private["annotations"] = refs("description-one", "description-two"), refs("private-body")
	private["name"], private["url"] = "Private signal label sentinel", "https://bank.example/private"
	person, pipeline, event, website, summary, span := record("person", ""), record("pipeline", ""), record("event", ""), record("website", ""), record("summary", ""), record("range", "")
	person["name"], person["summaries"], person["annotations"] = "Casey", refs(), refs()
	pipeline["name"], pipeline["summaries"] = "Daily standup", refs()
	website["url"] = "https://example.com/approved"
	summary["name"], summary["annotations"], summary["persons"], summary["pipelines"] = "Related summary", refs(), refs(), refs()
	span["from"], span["to"], span["between"] = map[string]any{"value": "2026-09-28T10:00:00Z"}, map[string]any{"value": "2026-09-28T11:00:00Z"}, true
	a["persons"], a["pipelines"], a["workstream_events"], a["websites"], a["summaries"], a["ranges"] = refs("person", "absent"), refs("pipeline"), refs("event"), refs("website"), refs("summary"), refs("range")
	f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {a, b, old, ancient, undated, private}, "ANNOTATIONS": {one, two, secret}, "PERSONS": {person}, "PIPELINES": {pipeline}, "WORKSTREAM_EVENTS": {event}, "WEBSITES": {website}, "WORKSTREAM_SUMMARIES": {summary}, "RANGES": {span}}}
	p := DefaultPolicy()
	p.Deny = []DomainRule{{"bank.example", true}}
	return f, p
}

func TestSignalDigestChronologyCoveragePrivacyAndRebuild(t *testing.T) {
	f, p := signalDigestFixture()
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS,PERSONS,PIPELINES,WORKSTREAM_EVENTS,WEBSITES,WORKSTREAM_SUMMARIES,RANGES")
	out := filepath.Join(t.TempDir(), "source")
	if path := os.Getenv("PIECES_EXPORT_SIGNAL_FIXTURE_OUTPUT"); path != "" {
		out = path
	}
	m, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Format: "both", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, p), SignalDigest: SignalDigestOptions{Mode: "split", RecordsPerPart: 2, MaxPartMiB: 1}})
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	s := m.SignalDigest
	if s == nil || s.Entries != 5 || s.Included != 5 || s.Undated != 1 || s.WithDescription != 2 || s.WithoutDescription != 3 || s.MultipleDescriptions != 1 || s.DescriptionAttachments != 3 || s.UnknownProjections != 2 || s.UnavailableReferences != 1 || len(s.Parts) != 3 {
		t.Fatalf("digest did not reconcile: %+v", s)
	}
	if m.Status != "partial" {
		t.Fatal("missing projections were certified")
	}
	var all strings.Builder
	bytes := int64(0)
	for _, part := range s.Parts {
		b, err := os.ReadFile(filepath.Join(out, part.Path))
		if err != nil {
			t.Fatal(err)
		}
		if len(b) != part.Bytes || len(b) > 1<<20 || part.Entries > 2 {
			t.Fatal("part budget/count mismatch")
		}
		bytes += int64(len(b))
		all.Write(b)
		if _, err := os.Stat(filepath.Join(out, pdfPath(part.Path))); err != nil {
			t.Fatal("digest PDF missing")
		}
	}
	index, _ := os.ReadFile(filepath.Join(out, s.Index))
	bytes += int64(len(index))
	if bytes != s.TotalBytes {
		t.Fatal("digest byte count mismatch")
	}
	previous := -1
	for _, id := range []string{"recent-a", "recent-b", "old", "ancient", "undated"} {
		position := strings.Index(all.String(), "ID: "+id+"\n")
		if position <= previous {
			t.Fatalf("chronology/tie/undated ordering lost for %s", id)
		}
		previous = position
	}
	for _, value := range []string{"Approved signal description", "Earlier retained description.", "derived inverse of annotation.signals", "2026-09-28T10:00:00Z", "Multiple descriptions", "REDACTED"} {
		if !strings.Contains(all.String(), value) {
			t.Fatalf("missing digest content: %s", value)
		}
	}
	if strings.Contains(all.String(), "pieces://") || strings.Contains(all.String(), "]()") {
		t.Fatal("invalid embedded links remained")
	}
	assertNoSignalPrivateText(t, out)
	rootIndex, _ := os.ReadFile(filepath.Join(out, "index.md"))
	if !strings.Contains(string(rootIndex), "signals/index.md") {
		t.Fatal("root navigation missing")
	}
	before := archiveHashes(t, out)
	for _, mode := range []string{"single", "off"} {
		newOut := filepath.Join(t.TempDir(), mode)
		rebuilt, err := Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: newOut, Scanner: scanner(t, p), SignalDigest: SignalDigestOptions{Mode: mode}}})
		if err != nil {
			t.Fatal(err)
		}
		if rebuilt.SignalDigest.Options.Mode != mode || rebuilt.SignalDigest.Included != 5 {
			t.Fatal("offline mode/selection changed")
		}
		if mode == "single" {
			if len(rebuilt.SignalDigest.Parts) != 1 || rebuilt.SignalDigest.Parts[0].Path != "signals/all-signals.md" || rebuilt.SignalDigest.Entries != 5 {
				t.Fatal("single digest missing entries")
			}
		} else if rebuilt.SignalDigest.Index != "" || rebuilt.SignalDigest.Entries != 0 {
			t.Fatal("off mode wrote a digest")
		}
		moved := newOut + " moved"
		if err := os.Rename(newOut, moved); err != nil {
			t.Fatal(err)
		}
		r := &run{ctx: context.Background(), stage: moved, opts: Options{Mode: "filtered", Scanner: scanner(t, p)}}
		if err := r.validateMarkdownLinks(); err != nil {
			t.Fatal(err)
		}
		if err := r.auditOutput(); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, archiveHashes(t, out)) {
		t.Fatal("offline rebuild changed source")
	}
}

// Small approved local fixtures exercise document budgets without OS I/O.
func stagedSignalDigest(t testing.TB, count, textBytes int) *run {
	t.Helper()
	root := t.TempDir()
	r := &run{ctx: context.Background(), stage: root, opts: Options{Mode: "preserve", Timezone: "UTC", SignalDigest: SignalDigestOptions{Mode: "split", RecordsPerPart: 1000, MaxPartMiB: 1}}, meta: map[string]*Meta{}, coverage: map[string]*Coverage{"SIGNALS": {Included: count, InitialCount: count, Fetched: count}}, documentMetadata: map[string]*DocumentMetadata{}}
	for _, folder := range []string{"data/signals", "data/annotations", "markdown/signals", "markdown/annotations"} {
		if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("signal-%06d", i)
		v := signalRecord(id)
		m := &Meta{Key: "SIGNALS\x00" + id, ID: id, Type: "SIGNALS", State: "included", Title: id, Created: timestamp(v, "created"), Path: "markdown/signals/" + id + ".md", DataPath: "data/signals/" + id + ".json"}
		if textBytes > 0 {
			aid := fmt.Sprintf("body-%06d", i)
			a := record(aid, "")
			a["type"], a["text"] = "SIGNAL_DESCRIPTION", "```\n"+strings.Repeat("x", textBytes)+"\n```\n"
			am := &Meta{Key: "ANNOTATIONS\x00" + aid, ID: aid, Type: "ANNOTATIONS", State: "included", Title: aid, Path: "markdown/annotations/" + aid + ".md", DataPath: "data/annotations/" + aid + ".json"}
			v["annotations"] = refs(aid)
			m.Edges = []Edge{{m.Key, am.Key, "annotations"}}
			r.meta[am.Key] = am
			b, _ := json.Marshal(a)
			if err := os.WriteFile(filepath.Join(root, am.DataPath), b, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, am.Path), []byte("# Canonical annotation\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		m.ProjectionStates = projectionStates("SIGNALS", v)
		r.meta[m.Key] = m
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(root, m.DataPath), b, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, m.Path), []byte("# Canonical signal\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"index.md", "coverage.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# Fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestSignalDigestByteBoundsAndEmptySelection(t *testing.T) {
	for _, mode := range []string{"split", "single"} {
		r := stagedSignalDigest(t, 2, 600<<10)
		r.opts.SignalDigest.Mode = mode
		err := r.renderSignalDigest()
		if mode == "single" {
			if err == nil || !strings.Contains(err.Error(), "signals-max-part-mib") {
				t.Fatalf("oversized single digest accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(r.stage, "signals/index.md")); !os.IsNotExist(err) {
				t.Fatal("failed digest index published")
			}
		} else if err != nil || len(r.manifest.SignalDigest.Parts) != 2 {
			t.Fatalf("byte-based splitting failed: %v", err)
		}
	}
	r := stagedSignalDigest(t, 1, 2<<20)
	if err := r.renderSignalDigest(); err == nil {
		t.Fatal("oversized individual entry silently accepted")
	}
	for _, selected := range []bool{true, false} {
		r := stagedSignalDigest(t, 0, 0)
		if !selected {
			r.coverage = nil
		}
		if err := r.renderSignalDigest(); err != nil {
			t.Fatal(err)
		}
		if r.manifest.SignalDigest.Selected != selected || r.manifest.SignalDigest.Entries != 0 {
			t.Fatal("empty/not-selected coverage mixed")
		}
		_, err := os.Stat(filepath.Join(r.stage, "signals/index.md"))
		if selected && err != nil || !selected && !os.IsNotExist(err) {
			t.Fatal("empty digest navigation incorrect")
		}
	}
}

func TestSignalDigestCancellationAndChangedStage(t *testing.T) {
	for _, phase := range []string{"Stage: Plan signals digest", "Stage: Write signals digest"} {
		r := stagedSignalDigest(t, 2, 1024)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.ctx = ctx
		w := &phaseHookWriter{phase: phase, hook: cancel}
		r.progress = startProgress(w, nil)
		err := r.renderSignalDigest()
		r.progress.Close()
		if !errors.Is(err, context.Canceled) || !w.fired {
			t.Fatalf("digest cancellation failed: %v", err)
		}
	}
	r := stagedSignalDigest(t, 1, 0)
	w := &phaseHookWriter{phase: "Stage: Write signals digest", hook: func() {
		m := r.meta["SIGNALS\x00signal-000000"]
		b, err := os.ReadFile(filepath.Join(r.stage, m.DataPath))
		if err != nil {
			t.Error(err)
			return
		}
		b = bytes.Replace(b, []byte(`"name":"signal-000000"`), []byte(`"name":"edited-000000"`), 1)
		if err := os.WriteFile(filepath.Join(r.stage, m.DataPath), b, 0600); err != nil {
			t.Error(err)
		}
	}}
	r.progress = startProgress(w, nil)
	err := r.renderSignalDigest()
	r.progress.Close()
	if err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("same-size stage edit went undetected: %v", err)
	}
}

func TestSignalDigestLargeInventory(t *testing.T) {
	r := stagedSignalDigest(t, 6716, 0)
	r.opts.SignalDigest = DefaultSignalDigestOptions()
	start := time.Now()
	if err := r.renderSignalDigest(); err != nil {
		t.Fatal(err)
	}
	if r.manifest.SignalDigest.Entries != 6716 || len(r.manifest.SignalDigest.Parts) != 7 {
		t.Fatal("large inventory was truncated")
	}
	t.Logf("6716 synthetic signals: 7 documents, %d bytes, %s; local staging only", r.manifest.SignalDigest.TotalBytes, time.Since(start))
}

func TestSignalInverseRelationshipsCoverAllDeclaredDimensions(t *testing.T) {
	for _, direction := range []string{"to-signal", "from-signal"} {
		r := &run{meta: map[string]*Meta{}}
		s := &Meta{Key: "SIGNALS\x00signal", ID: "signal", Type: "SIGNALS", State: "included"}
		r.meta[s.Key] = s
		for typ, field := range signalInverseFields {
			m := &Meta{Key: typ + "\x00fixture", ID: "fixture", Type: typ, State: "included"}
			r.meta[m.Key] = m
			if direction == "to-signal" {
				m.Edges = []Edge{{m.Key, s.Key, "signals"}}
			} else {
				s.Edges = append(s.Edges, Edge{s.Key, m.Key, field})
			}
		}
		r.reconcileAnnotationAttachments()
		for typ, field := range signalInverseFields {
			e := Edge{s.Key, typ + "\x00fixture", field}
			if direction == "from-signal" {
				e = Edge{typ + "\x00fixture", s.Key, "signals"}
			}
			if !r.derivedEdges[e] {
				t.Fatalf("missing inverse %s in %s", field, direction)
			}
		}
		before := len(r.derivedEdges)
		r.reconcileAnnotationAttachments()
		if len(r.derivedEdges) != before {
			t.Fatal("inverse reconciliation not idempotent")
		}
	}
}

func TestSignalDigestLimitStopsExportAndPreservesMarkdown(t *testing.T) {
	// Exceed the 1 MiB digest budget without crossing the scanner's independent
	// 2 MiB field limit, which would correctly withhold the annotation earlier.
	const textBytes = 1536 << 10
	signal, body := signalRecord("signal"), record("body", "")
	signal["annotations"] = refs("body")
	body["type"], body["text"] = "SIGNAL_DESCRIPTION", "```\n"+strings.Repeat("x", textBytes)+"\n```\n"
	f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}, "ANNOTATIONS": {body}}}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS")
	out := filepath.Join(t.TempDir(), "archive")
	m, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), SignalDigest: SignalDigestOptions{MaxPartMiB: 1}})
	if err == nil || !strings.Contains(err.Error(), "signals-max-part-mib") || m.Status != "failed" || !m.Finished.IsZero() {
		t.Fatalf("oversize digest did not fail the archive: %s %v", m.Status, err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("oversize digest finalized")
	}
	if _, err := InspectArchive(out + ".partial"); err == nil {
		t.Fatal("failed digest accepted as rebuild source")
	}
	paths, _ := filepath.Glob(filepath.Join(out+".partial", "markdown/annotations/*.md"))
	if len(paths) != 1 {
		t.Fatal("canonical annotation removed")
	}
	b, err := os.ReadFile(paths[0])
	if err != nil || bytes.Count(b, []byte("x")) < textBytes {
		t.Fatal("canonical text was truncated")
	}
}

func TestPackagedSignalDigestCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f, p := signalDigestFixture()
	srv := f.server(t)
	defer srv.Close()
	root := t.TempDir()
	policy := filepath.Join(root, "policy.json")
	if err := writeJSON(policy, p); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{"export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--materials", "SIGNALS,ANNOTATIONS,PERSONS,PIPELINES,WORKSTREAM_EVENTS,WEBSITES,WORKSTREAM_SUMMARIES,RANGES", "--format", "both", "--metadata", "off", "--policy", policy, "--output", source, "--signals-digest", "split", "--signals-per-part", "2"}
	call := func(args []string) {
		t.Helper()
		b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("expected honest partial signal coverage: %v\n%s", err, b)
		}
	}
	call(args)
	srv.Close()
	m, err := InspectArchive(source)
	if err != nil || m.SignalDigest == nil || m.SignalDigest.Entries != 5 || len(m.SignalDigest.Parts) != 3 {
		t.Fatalf("compiled digest coverage: %v", err)
	}
	out := filepath.Join(root, "single")
	call([]string{"rebuild", "--source", source, "--output", out, "--policy", policy, "--signals-digest", "single", "--format", "both", "--yes"})
	m, err = InspectArchive(out)
	if err != nil || m.SignalDigest.Entries != 5 || len(m.SignalDigest.Parts) != 1 {
		t.Fatal("compiled offline single digest failed")
	}
	moved := out + " moved"
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: ctx, stage: moved, opts: Options{Mode: "filtered", Scanner: scanner(t, p)}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	assertNoSignalPrivateText(t, moved)
	t.Log("actual compiled split export/offline single rebuild: counts, descriptions, filtering, PDFs, and moved links passed")
}
