package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func archiveHashes(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[rel] = sha256.Sum256(b)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func createRebuildFixture(t *testing.T) (string, Policy, Manifest) {
	t.Helper()
	f := summaryScopeFixture()
	for _, id := range []string{"unconnected-1", "unconnected-2"} {
		p := record(id, "")
		p["name"], p["annotations"], p["summaries"] = "Unconnected Fixture Person", refs(), refs()
		f.data["PERSONS"] = append(f.data["PERSONS"], p)
	}
	private := record("PRIVATE_EXCLUDED_RECORD_ID", "")
	private["text"] = "PRIVATE_EXCLUDED_TEXT https://bank.example/private"
	f.data["ANNOTATIONS"] = append(f.data["ANNOTATIONS"], private)
	f.data["ANNOTATIONS"][0]["text"] = "Actual summary narrative. Synthetic credential: " + fakeSecret()
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	selection, _ := SelectScope("summaries", "")
	p := DefaultPolicy()
	p.Deny = []DomainRule{{"bank.example", true}}
	output := filepath.Join(t.TempDir(), "source")
	m, err := Export(context.Background(), c, Options{Output: output, Scope: selection.Name, Materials: selection.Materials, ReferenceOnly: selection.ReferenceOnly, Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, p), Version: "fixture-source"})
	srv.Close() // Offline tests cannot consult this OS again.
	if err != nil || m.Status != "complete_for_implemented_scope" {
		t.Fatalf("source fixture export failed: %v %+v", err, m.Issues)
	}
	return output, p, m
}

func TestRebuildOfflinePreservesPrivacyGraphAndUserEvidence(t *testing.T) {
	source, policy, original := createRebuildFixture(t)
	before := archiveHashes(t, source)
	data, _ := os.ReadFile(filepath.Join(source, archiveStateFile))
	if original.FormatVersion != 5 || original.ArchiveState == nil || strings.Contains(string(data), "PRIVATE_EXCLUDED_RECORD_ID") || strings.Contains(string(data), "PRIVATE_EXCLUDED_TEXT") {
		t.Fatal("reconstruction evidence absent or disclosed an excluded identity")
	}
	dest := filepath.Join(t.TempDir(), "rebuilt")
	m, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: dest, Scanner: scanner(t, policy), Format: "both", Naming: "opaque", Relationships: "sidecar", PeopleMode: "profiles", Version: "fixture-rebuilder"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "complete_for_implemented_scope" || m.Rebuild == nil || m.Rebuild.LegacyEvidence || m.Rebuild.SourceToolVersion != "fixture-source" || m.Performance.Requests != 0 {
		t.Fatalf("invalid reconstruction provenance: %+v", m.Rebuild)
	}
	if m.People.Total != 3 || m.People.Selected != 1 || m.People.Omitted != 2 {
		t.Fatalf("offline people narrowing incorrect: %+v", m.People)
	}
	if m.Rebuild.SourcePeople.Selected != 3 || m.PolicyHash != original.PolicyHash || !m.Rebuild.SourceStarted.Equal(original.Started) || !m.Rebuild.SourceFinished.Equal(original.Finished) {
		t.Fatal("original people/policy/read interval lost")
	}
	profiles, _ := filepath.Glob(filepath.Join(dest, "workstream_summaries/personas/users/*/profile.md"))
	if len(profiles) != 1 {
		t.Fatal("verified user evidence was not replayed")
	}
	for _, cov := range m.Coverage {
		if cov.Material == "ANNOTATIONS" && (cov.Excluded != 1 || cov.Redacted != 1) {
			t.Fatalf("original exclusion/redaction counts lost: %+v", cov)
		}
	}
	if !reflect.DeepEqual(before, archiveHashes(t, source)) {
		t.Fatal("source archive changed during reconstruction")
	}
	moved := dest + "-moved"
	if err := os.Rename(dest, moved); err != nil {
		t.Fatal(err)
	}
	r := &run{ctx: context.Background(), stage: moved, opts: Options{Scanner: scanner(t, policy)}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
	// Rebuilding a narrowed archive never reintroduces its omitted people.
	again := filepath.Join(t.TempDir(), "again")
	second, err := Rebuild(context.Background(), RebuildOptions{Source: moved, Options: Options{Output: again, Scanner: scanner(t, policy), Format: "markdown"}})
	if err != nil || second.Naming != "opaque" || second.Relationships != "sidecar" || second.Rebuild.SourcePeople.Selected != 1 || !second.Rebuild.OriginalReadStarted.Equal(original.Started) || !second.Rebuild.OriginalReadFinished.Equal(original.Finished) {
		t.Fatalf("rebuilding reconstructed archive failed: %v", err)
	}
	for _, cov := range second.Coverage {
		if cov.Material == "PERSONS" && (cov.Omitted != 2 || cov.Included != 1) {
			t.Fatal("prior person omissions changed")
		}
	}
}

func TestRebuildRejectsAlterationUnsafePathsAndPolicyChanges(t *testing.T) {
	for _, mutation := range []string{"record", "graph", "state", "link-map", "missing-checksum", "symlink", "fifo", "outside-link", "policy", "inside-output", "inside-output-alias", "partial", "canceled", "existing-output"} {
		t.Run(mutation, func(t *testing.T) {
			source, p, _ := createRebuildFixture(t)
			out := filepath.Join(t.TempDir(), "rebuilt")
			ctx := context.Background()
			switch mutation {
			case "fifo":
				mkfifo, err := exec.LookPath("mkfifo")
				if err != nil {
					t.Skip("mkfifo unavailable on this platform")
				}
				file := filepath.Join(source, "data/annotations/"+opaque("ANNOTATIONS", "body")+".json")
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := exec.Command(mkfifo, file).Run(); err != nil {
					t.Fatal(err)
				}
			case "missing-checksum":
				b, _ := os.ReadFile(filepath.Join(source, "manifest.json"))
				var m Manifest
				_ = json.Unmarshal(b, &m)
				m.ArchiveState.StateSHA256 = ""
				if err := rewriteJSON(filepath.Join(source, "manifest.json"), m); err != nil {
					t.Fatal(err)
				}
			case "link-map":
				file := filepath.Join(source, "link-map.json")
				var links map[string]string
				b, _ := os.ReadFile(file)
				_ = json.Unmarshal(b, &links)
				a, bkey := opaque("ANNOTATIONS", "body"), opaque("ANNOTATIONS", "profile")
				links[a], links[bkey] = links[bkey], links[a]
				if err := rewriteJSON(file, links); err != nil {
					t.Fatal(err)
				}
			case "record":
				file := filepath.Join(source, "data/annotations/"+opaque("ANNOTATIONS", "body")+".json")
				b, _ := os.ReadFile(file)
				b = []byte(strings.Replace(string(b), "Actual summary narrative", "Altered summary narrative", 1))
				if err := os.WriteFile(file, b, 0600); err != nil {
					t.Fatal(err)
				}
			case "graph", "state":
				name := "relationships.jsonl"
				if mutation == "state" {
					name = archiveStateFile
				}
				file := filepath.Join(source, name)
				b, _ := os.ReadFile(file)
				if err := os.WriteFile(file, append(b, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				file := filepath.Join(source, "data/annotations/"+opaque("ANNOTATIONS", "body")+".json")
				if err := os.Rename(file, file+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(file+".original", file); err != nil {
					t.Skip("symlink unavailable")
				}
			case "outside-link":
				file := filepath.Join(source, "link-map.json")
				var links map[string]string
				b, _ := os.ReadFile(file)
				_ = json.Unmarshal(b, &links)
				links[opaque("ANNOTATIONS", "body")] = "../../escape.md"
				if err := rewriteJSON(file, links); err != nil {
					t.Fatal(err)
				}
			case "policy":
				p.Deny = nil
			case "inside-output-alias":
				alias := filepath.Join(t.TempDir(), "source-alias")
				if err := os.Symlink(source, alias); err != nil {
					t.Skip("symlink unavailable")
				}
				out = filepath.Join(alias, "rebuilt")
			case "inside-output":
				out = filepath.Join(source, "rebuilt")
			case "partial":
				if err := os.Rename(source, source+".partial"); err != nil {
					t.Fatal(err)
				}
				source += ".partial"
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "existing-output":
				if err := os.Mkdir(out, 0700); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Rebuild(ctx, RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, p)}})
			if err == nil {
				t.Fatal("invalid reconstruction was accepted")
			}
			if _, err := os.Stat(filepath.Join(out, "manifest.json")); !os.IsNotExist(err) {
				t.Fatal("invalid reconstruction was finalized")
			}
		})
	}
}

func TestRebuildLegacyArchiveKeepsCoveragePartial(t *testing.T) {
	source, policy, m := createRebuildFixture(t)
	m.FormatVersion, m.ArchiveState = 4, nil
	if err := rewriteJSON(filepath.Join(source, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, archiveStateFile)); err != nil {
		t.Fatal(err)
	}
	before := archiveHashes(t, source)
	out := filepath.Join(t.TempDir(), "legacy-rebuilt")
	result, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, policy)}})
	if err != nil || result.Status != "partial" || !result.Rebuild.LegacyEvidence {
		t.Fatalf("legacy archive coverage overclaimed: %v", err)
	}
	profiles, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/personas/users/*/profile.md"))
	if len(profiles) != 1 {
		t.Fatal("existing legacy verified-user navigation lost")
	}
	if !reflect.DeepEqual(before, archiveHashes(t, source)) {
		t.Fatal("legacy input modified")
	}
	for _, cov := range result.Coverage {
		if cov.Material == "ANNOTATIONS" && cov.Redacted != -1 {
			t.Fatal("legacy per-record redaction evidence invented")
		}
	}
	again, err := Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: filepath.Join(t.TempDir(), "legacy-again"), Scanner: scanner(t, policy)}})
	if err != nil || !again.Rebuild.LegacyEvidence {
		t.Fatalf("legacy evidence warning disappeared on repeated rebuild: %v", err)
	}
	for _, cov := range again.Coverage {
		if cov.Material == "ANNOTATIONS" && cov.Redacted != -1 {
			t.Fatal("unknown legacy redaction count became an invented exact count")
		}
	}
}

func TestRebuildCacheRecoveryBlocksUnavailableDependencies(t *testing.T) {
	data := map[string][]map[string]any{"WORKSTREAM_SUMMARIES": {}, "ANNOTATIONS": {}, "WORKSTREAM_EVENTS": {}}
	for _, id := range []string{"safe", "dependent", "excluded-root"} {
		s := record(id, "2026-09-29T12:00:00Z")
		s["name"] = id
		if id == "excluded-root" {
			s["url"] = "https://bank.example/private"
		}
		data["WORKSTREAM_SUMMARIES"] = append(data["WORKSTREAM_SUMMARIES"], s)
		a := record(id+"-body", "2026-09-29T12:00:00Z")
		a["type"], a["text"] = "SUMMARY", "Narrative for "+id
		data["ANNOTATIONS"] = append(data["ANNOTATIONS"], a)
	}
	e := record("excluded-event", "")
	e["url"] = "https://bank.example/private-event"
	data["WORKSTREAM_EVENTS"] = append(data["WORKSTREAM_EVENTS"], e)
	f := &fakeOS{data: data}
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("WORKSTREAM_SUMMARIES,ANNOTATIONS,WORKSTREAM_EVENTS")
	p := DefaultPolicy()
	p.Deny = []DomainRule{{"bank.example", true}}
	source := filepath.Join(t.TempDir(), "source")
	_, err := Export(context.Background(), c, Options{Output: source, Materials: materials, Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, p)})
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	cached := []map[string]any{}
	for _, id := range []string{"safe", "dependent", "excluded-root"} {
		s := record(id, "2026-09-29T12:00:00Z")
		s["annotations"] = refs(id + "-body")
		if id == "dependent" {
			s["events"] = refs("excluded-event")
		}
		cached = append(cached, s)
	}
	cache, _ := cacheFixture(t, cached, true)
	out := filepath.Join(t.TempDir(), "recovered")
	m, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, p), SDKCaches: []string{cache}, Format: "both"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "partial" || m.Rebuild.UnavailableTargets != 1 || m.Rebuild.BlockedCacheBodies != 1 {
		t.Fatalf("missing dependency evidence lost: %+v", m.Rebuild)
	}
	var links map[string]string
	b, _ := os.ReadFile(filepath.Join(out, "link-map.json"))
	_ = json.Unmarshal(b, &links)
	for _, item := range [][2]string{{"WORKSTREAM_SUMMARIES", "dependent"}, {"WORKSTREAM_SUMMARIES", "excluded-root"}, {"ANNOTATIONS", "dependent-body"}, {"ANNOTATIONS", "excluded-root-body"}} {
		if links[opaque(item[0], item[1])] != "" {
			t.Fatal("excluded or unproven dependency content reintroduced")
		}
	}
	b, err = os.ReadFile(filepath.Join(out, links[opaque("WORKSTREAM_SUMMARIES", "safe")]))
	if err != nil || !strings.Contains(string(b), "Narrative for safe") || !strings.Contains(string(b), "Historical attachment") {
		t.Fatal("safe cached body/provenance not recovered")
	}
	// Existing historical provenance must survive a second rebuild without caches.
	second := filepath.Join(t.TempDir(), "again")
	again, err := Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: second, Scanner: scanner(t, p), Format: "markdown"}})
	if err != nil || again.SDKCache.RetainedEdges != m.SDKCache.RetainedEdges {
		t.Fatalf("historical edge provenance not preserved: %v", err)
	}
}

func TestRebuildPreserveKeepsOriginalsAndRejectsModeChange(t *testing.T) {
	u := record("user", "2026-09-29T12:00:00Z")
	u["api_key"] = fakeSecret()
	f := &fakeOS{data: map[string][]map[string]any{"USERS": {u}}}
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("USERS")
	source := filepath.Join(t.TempDir(), "preserved")
	_, err := Export(context.Background(), c, Options{Output: source, Mode: "preserve", Materials: materials, Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "rebuilt")
	m, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, DefaultPolicy())}})
	if err != nil || m.Mode != "preserve" {
		t.Fatalf("preservation mode lost: %v", err)
	}
	name := filepath.Join("raw", "users", opaque("USERS", "user")+".json")
	before, _ := os.ReadFile(filepath.Join(source, name))
	after, _ := os.ReadFile(filepath.Join(out, name))
	if string(before) != string(after) || !strings.Contains(string(after), fieldString(u, "api_key")) {
		t.Fatal("preserved original changed")
	}
	_, err = Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: filepath.Join(t.TempDir(), "wrong-mode"), Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}})
	if err == nil {
		t.Fatal("rebuild silently changed privacy mode")
	}
}
