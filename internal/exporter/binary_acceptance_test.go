package exporter

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Executes the actual distributed executable against a synthetic OS. The
// fixture cannot launch, shut down, or read an installed Pieces OS instance.
func TestPackagedCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	summary := record("summary", "2026-09-29T12:00:00Z")
	summary["name"], summary["parentHierarchicalType"] = "Acceptance summary", "TEMPORAL_DAY_HIERARCHICAL_SUMMARY"
	summary["annotations"], summary["persons"], summary["pipelines"] = refs("body"), refs("person"), refs()
	body := record("body", "2026-09-29T12:00:00Z")
	secret := fakeSecret()
	body["type"], body["text"], body["summaries"] = "SUMMARY", "# Portable memory\n\nA summary with [a person](pieces://persons/person).\n\nSynthetic credential: "+secret, refs("summary")
	person := record("person", "2026-09-29T12:00:00Z")
	person["name"], person["annotations"], person["summaries"] = "Example User", refs("persona"), refs("summary")
	persona := record("persona", "2026-09-29T12:00:00Z")
	persona["type"], persona["text"], persona["persons"] = "HIERARCHICAL_PROFILE_SUMMARY", "A retained synthetic profile.", refs("person")
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {summary}, "PERSONS": {person}, "ANNOTATIONS": {body, persona}}, currentUserID: "user", userPersons: map[string]string{"user": "person"}}
	srv := f.server(t)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	common := []string{"--base-url", srv.URL, "--environment", "production", "--launch-os=false", "--materials", "WORKSTREAM_SUMMARIES,PERSONS,ANNOTATIONS"}
	for _, command := range []string{"doctor", "scan"} {
		args := append([]string{command}, common...)
		if b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
			t.Fatalf("packaged %s failed: %v\n%s", command, err, b)
		}
	}
	out := filepath.Join(t.TempDir(), "archive with spaces")
	args := append([]string{"export"}, common...)
	args = append(args, "--output", out, "--close-desktop=false", "--yes", "--format", "both", "--metadata", "auto", "--people", "profiles")
	if b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("packaged export failed: %v\n%s", err, b)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "complete_for_implemented_scope" || len(manifest.Issues) != 0 {
		t.Fatalf("synthetic archive is incomplete: %+v", manifest.Issues)
	}
	diagnostics := readLocalPerformance(t, out)
	if diagnostics.HTTP.Requests == 0 || manifest.FileWorkers != 2 || diagnostics.FileWorkers != 2 || manifest.LocalPerformance == nil || diagnostics.Operations["artifact_sync"].Calls == 0 || diagnostics.State != "finalizing" {
		t.Fatal("packaged export did not report HTTP/local persistence measurements")
	}
	for _, coverage := range manifest.Coverage {
		if coverage.InitialCount != coverage.FinalCount || coverage.Fetched != coverage.InitialCount || coverage.Included != coverage.InitialCount {
			t.Fatalf("synthetic collection did not reconcile: %+v", coverage)
		}
	}
	profiles, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/personas/users/*/profile.md"))
	if len(profiles) != 1 {
		t.Fatal("verified user persona profile missing")
	}
	moved := out + "-moved"
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: ctx, stage: moved, opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(moved, "workstream_summaries/timeline/000000.*.md"))
	foundBody := false
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), secret) {
			t.Fatal("synthetic credential leaked")
		}
		foundBody = foundBody || strings.Contains(string(b), "Portable memory")
	}
	if !foundBody {
		t.Fatal("summary body was not rendered")
	}
	if _, err := os.Stat(filepath.Join(moved, "index.pdf")); err != nil {
		t.Fatal("PDF index missing")
	}
	t.Log("native binary doctor/scan/export, counts, user profile, body, PDF/privacy checks, and moved local links passed")
}

func TestPackagedSummaryCLI(t *testing.T) {
	testPackagedSummaryCLI(t, []string{"--scope", "summaries"})
}

func TestPackagedDefaultSummaryCLI(t *testing.T) {
	testPackagedSummaryCLI(t, nil)
}

func testPackagedSummaryCLI(t *testing.T, scopeFlags []string) {
	t.Helper()
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f := summaryScopeFixture()
	unprofiled := record("unprofiled-person", "")
	unprofiled["name"], unprofiled["annotations"], unprofiled["summaries"] = "Unprofiled person", refs(), refs()
	f.data["PERSONS"] = append(f.data["PERSONS"], unprofiled)
	srv := f.server(t)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	common := append([]string{"--base-url", srv.URL, "--launch-os=false"}, scopeFlags...)
	for _, command := range []string{"scan", "benchmark", "export"} {
		args := append([]string{command}, common...)
		if command == "benchmark" {
			args = append(args, "--benchmark-reads", "1", "--benchmark-duration", "5s")
		}
		if command == "export" {
			args = append(args, "--dry-run", "--benchmark-reads", "1", "--benchmark-duration", "5s")
		}
		b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if err != nil || !strings.Contains(string(b), "Export scope: summaries") {
			t.Fatalf("packaged scoped %s failed: %v\n%s", command, err, b)
		}
	}
	out := filepath.Join(t.TempDir(), "summaries only")
	args := append([]string{"export"}, common...)
	args = append(args, "--output", out, "--close-desktop=false", "--yes", "--format", "both", "--metadata", "off")
	if b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("packaged summaries export failed: %v\n%s", err, b)
	}
	assertSummaryScopeRequests(t, f)
	var manifest Manifest
	b, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil || json.Unmarshal(b, &manifest) != nil || manifest.Scope.Name != "summaries" || manifest.Status != "complete_for_implemented_scope" {
		t.Fatal("packaged summary scope manifest missing or incomplete")
	}
	if manifest.People.Mode != "profiles" || manifest.People.Selected != 1 || manifest.People.Omitted != 1 {
		t.Fatalf("default people selection did not retain only the profile: %+v", manifest.People)
	}
	var paths map[string]string
	b, _ = os.ReadFile(filepath.Join(out, "link-map.json"))
	if err := json.Unmarshal(b, &paths); err != nil {
		t.Fatal(err)
	}
	if paths[opaque("PERSONS", "unprofiled-person")] != "" || !strings.HasPrefix(paths[opaque("ANNOTATIONS", "profile")], "workstream_summaries/personas/users/") {
		t.Fatal("profile selection or verified user history paths incorrect")
	}
	b, err = os.ReadFile(filepath.Join(out, paths[opaque("WORKSTREAM_SUMMARIES", "summary")]))
	if err != nil || !strings.Contains(string(b), "Actual summary narrative") {
		t.Fatal("packaged summaries export lost its annotation body")
	}
	moved := out + "-moved"
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: ctx, stage: moved, opts: Options{Scanner: scanner(t, DefaultPolicy())}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	t.Log("scoped scan/benchmark/export read no event bodies or supporting inventories; body, PDF/privacy, and moved links passed")
}

func TestPackagedRebuildCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	source, policy, original := createRebuildFixture(t)
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := writeJSON(policyPath, policy); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out := filepath.Join(t.TempDir(), "offline archive")
	args := []string{"rebuild", "--source", source, "--output", out, "--policy", policyPath, "--format", "both", "--metadata", "off", "--people", "profiles", "--file-workers", "4"}
	// EOF declines without writing or connecting to any server.
	if b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil || !strings.Contains(string(b), "Canceled") {
		t.Fatalf("packaged rebuild EOF did not decline: %v %s", err, b)
	}
	if _, err := os.Stat(out + ".partial"); !os.IsNotExist(err) {
		t.Fatal("declined rebuild created staging directory")
	}
	args = append(args, "--yes")
	if b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("packaged offline rebuild failed: %v\n%s", err, b)
	}
	var m Manifest
	b, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	if json.Unmarshal(b, &m) != nil || m.Performance.Requests != 0 || m.People.Selected != 1 || !m.Rebuild.SourceStarted.Equal(original.Started) {
		t.Fatal("packaged rebuild evidence/selection incorrect")
	}
	diagnostics := readLocalPerformance(t, out)
	if diagnostics.HTTP.Requests != 0 || m.FileWorkers != 4 || diagnostics.FileWorkers != 4 || m.Rebuild.SourceLocalPerformance == nil || diagnostics.Operations["artifact_sync"].Calls == 0 {
		t.Fatal("packaged rebuild did not separate source and local measurements")
	}
	r := &run{ctx: ctx, stage: out, opts: Options{Scanner: scanner(t, policy)}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	t.Log("actual binary offline rebuild, EOF cancellation, PDF/privacy, original provenance and person narrowing passed with fixture OS already closed")
}

func TestPackagedLegacyRebuildCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	source := createLegacyProjectedArchive(t)
	out := filepath.Join(t.TempDir(), "legacy profiles")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b, err := exec.CommandContext(ctx, binary, "rebuild", "--source", source, "--output", out, "--people", "profiles", "--yes").CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
		t.Fatalf("legacy rebuild must stay partial: %v %s", err, b)
	}
	m, err := InspectArchive(out)
	if err != nil || !m.Rebuild.LegacyPersonEvidence || m.People.Selected != 2 || m.People.Unknown != 1 || m.People.Omitted != 2 {
		t.Fatalf("packaged legacy selection lost conservative evidence: %v", err)
	}
	r := &run{ctx: ctx, stage: out, opts: Options{Scanner: scanner(t, DefaultPolicy())}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	t.Log("legacy aggregate/issue reconciliation supports profile selection; unknown person retained and partial exit preserved")
}

// Use a real pre-signal-coverage format-5 writer to check compatibility rather
// than relying only on mutation of a new archive's reconstruction state.
func TestOlderPackagedSignalRebuild(t *testing.T) {
	oldBinary := os.Getenv("PIECES_EXPORT_TEST_LEGACY_SIGNAL_BINARY")
	if oldBinary == "" {
		t.Skip("set PIECES_EXPORT_TEST_LEGACY_SIGNAL_BINARY to the native 0.8.6-dev executable")
	}
	oldBinary, err := filepath.Abs(oldBinary)
	if err != nil {
		t.Fatal(err)
	}
	signal := record("signal", "2026-09-30T00:00:00Z")
	signal["name"] = "Old binary signal fixture"
	for _, field := range projectionFields("SIGNALS") {
		signal[field] = refs()
	}
	f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}}}
	srv := f.server(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	source := filepath.Join(t.TempDir(), "old archive")
	b, err := exec.CommandContext(ctx, oldBinary, "export", "--base-url", srv.URL, "--materials", "SIGNALS", "--launch-os=false", "--close-desktop=false", "--output", source, "--metadata", "off", "--yes").CombinedOutput()
	srv.Close()
	if err != nil {
		t.Fatalf("old fixture writer failed: %v %s", err, b)
	}
	old, err := InspectArchive(source)
	if err != nil || old.ToolVersion != "0.8.6-dev" || old.ArchiveState == nil {
		t.Fatal("expected the actual older format-5 package")
	}
	b, err = os.ReadFile(filepath.Join(source, archiveStateFile))
	var row archiveRecord
	if err != nil || json.Unmarshal(b, &row) != nil || row.ProjectionStates != nil {
		t.Fatal("old writer unexpectedly supplied signal projection evidence")
	}
	out := filepath.Join(t.TempDir(), "rebuilt")
	m, err := Rebuild(ctx, RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, DefaultPolicy())}})
	if err != nil || m.Status != "partial" || len(m.RelationshipCoverage) != 7 || m.Performance.Requests != 0 {
		t.Fatalf("old binary archive was not conservatively rebuilt: %v", err)
	}
	for _, coverage := range m.RelationshipCoverage {
		if coverage.Material != "SIGNALS" || coverage.Absent != 1 {
			t.Fatal("pruned legacy JSON was treated as original empty evidence")
		}
	}
	t.Log("actual 0.8.6-dev archive rebuilt offline with all seven signal projections unknown and partial status")
}

func TestPackagedCacheCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	summary := record("summary", "2026-09-20T00:00:00Z")
	summary["persons"], summary["pipelines"] = refs(), refs()
	body := record("body", "2026-09-20T00:00:00Z")
	body["type"], body["text"] = "SUMMARY", "Packaged cache recovery body."
	cached := record("summary", "2026-09-20T00:00:00Z")
	cached["annotations"] = refs("body")
	cache, _ := cacheFixture(t, []map[string]any{cached}, true)
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {summary}, "ANNOTATIONS": {body}}}
	srv := f.server(t)
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "cache archive")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{"export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--format", "both", "--metadata", "off", "--materials", "WORKSTREAM_SUMMARIES,ANNOTATIONS", "--output", out, "--sdk-cache", cache}
	b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 {
		t.Fatalf("cache export must return partial exit 2: %v\n%s", err, b)
	}
	if strings.Contains(string(b), cache) {
		t.Fatal("cache path leaked to terminal")
	}
	data, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "partial" || manifest.SDKCache.AddedEdges != 2 || manifest.SDKCache.RetainedEdges != 2 {
		t.Fatalf("cache evidence missing: %+v", manifest.SDKCache)
	}
	files, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/timeline/*.md"))
	found := false
	for _, path := range files {
		data, _ := os.ReadFile(path)
		found = found || strings.Contains(string(data), "Packaged cache recovery body.") && strings.Contains(string(data), "Historical attachment")
	}
	if !found {
		t.Fatal("packaged cache recovery failed to attach current body with provenance")
	}
	moved := out + "-moved"
	if err := os.Rename(out, moved); err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: ctx, stage: moved, opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	t.Log("native binary read-only SDK-cache recovery, body/provenance, partial exit status, PDF/privacy checks and moved links passed")
}
