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
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	f := summaryScopeFixture()
	srv := f.server(t)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	common := []string{"--scope", "summaries", "--base-url", srv.URL, "--launch-os=false"}
	for _, command := range []string{"scan", "benchmark"} {
		args := append([]string{command}, common...)
		if command == "benchmark" {
			args = append(args, "--benchmark-reads", "1", "--benchmark-duration", "5s")
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
	var paths map[string]string
	b, _ = os.ReadFile(filepath.Join(out, "link-map.json"))
	if err := json.Unmarshal(b, &paths); err != nil {
		t.Fatal(err)
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
