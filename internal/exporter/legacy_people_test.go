package exporter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyPersonEvidenceRequiresReconciledExplicitReports(t *testing.T) {
	for _, scenario := range []string{"complete", "known-unknown", "count-mismatch", "missing-field", "missing-issues", "narrowed", "selection-mismatch", "excluded-issue", "unkeyed-issue", "duplicate-issue"} {
		t.Run(scenario, func(t *testing.T) {
			byRef := map[string]*Meta{}
			for _, id := range []string{"one", "two", "three"} {
				byRef[opaque("PERSONS", id)] = &Meta{Type: "PERSONS", ID: id, PersonProjection: true}
			}
			m := Manifest{People: PeopleStats{Mode: "all", Total: 3, Selected: 3}, Issues: []Issue{}}
			want := true
			switch scenario {
			case "known-unknown", "duplicate-issue":
				m.People.Unknown = 1
				m.Issues = append(m.Issues, Issue{"PERSONS", opaque("PERSONS", "two"), "persona_history_unresolved"})
				if scenario == "duplicate-issue" {
					m.Issues = append(m.Issues, m.Issues[0])
				}
			case "count-mismatch":
				m.People.Unknown, want = 2, false
				m.Issues = append(m.Issues, Issue{"PERSONS", opaque("PERSONS", "two"), "persona_history_unresolved"})
			case "narrowed":
				m.People.Mode, want = "profiles", false
			case "selection-mismatch":
				m.People.Selected, want = 2, false
			case "excluded-issue":
				m.Issues = append(m.Issues, Issue{"PERSONS", opaque("PERSONS", "previously-excluded"), "persona_history_unresolved"})
			case "unkeyed-issue":
				m.Issues = append(m.Issues, Issue{"PERSONS", "", "persona_history_unresolved"})
				want = false
			}
			raw, _ := json.Marshal(m)
			if scenario == "missing-field" || scenario == "missing-issues" {
				var v map[string]any
				_ = json.Unmarshal(raw, &v)
				if scenario == "missing-field" {
					delete(object(v, "people"), "persons_with_incomplete_selection_evidence")
				} else {
					delete(v, "issues")
				}
				raw, _ = json.Marshal(v)
				want = false
			}
			r := &run{coverage: map[string]*Coverage{"PERSONS": {}, "ANNOTATIONS": {}}, manifest: Manifest{Rebuild: &RebuildInfo{}}}
			r.restoreLegacyPersonEvidence(m, raw, byRef)
			if r.manifest.Rebuild.LegacyPersonEvidence != want {
				t.Fatalf("evidence=%v, want %v", r.manifest.Rebuild.LegacyPersonEvidence, want)
			}
			for ref, person := range byRef {
				if !want {
					if person.PersonEvidence != nil {
						t.Fatal("unreconciled evidence changed selection")
					}
					continue
				}
				p := person.PersonEvidence
				unknown := m.People.Unknown == 1 && ref == opaque("PERSONS", "two")
				if p == nil || p.UnknownAnnotations != unknown || !p.UnknownConnections || p.SourceEventConnections != 0 {
					t.Fatal("unknown flags or unavailable connectivity invented")
				}
			}
		})
	}
}

func legacyProjectedPeopleServer(t *testing.T) *httptest.Server {
	t.Helper()
	persons := []map[string]any{}
	for _, id := range []string{"with-profile", "known-empty-1", "known-empty-2", "unknown"} {
		v := record(id, "2026-09-29T12:00:00Z")
		v["name"] = id // All relationship projections are deliberately absent.
		persons = append(persons, v)
	}
	a := record("profile", "2026-09-29T12:00:00Z")
	a["type"], a["text"], a["persons"] = "HIERARCHICAL_PROFILE_SUMMARY", "A retained synthetic persona.", refs("with-profile")
	f := &fakeOS{data: map[string][]map[string]any{"PERSONS": persons, "ANNOTATIONS": {a}}, currentUserID: "user", userPersons: map[string]string{"user": "with-profile"}}
	base := f.server(t)
	handler := base.Config.Handler
	base.Close()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/person/unknown/annotations" {
			http.Error(w, "synthetic unavailable history", http.StatusForbidden)
			return
		}
		handler.ServeHTTP(w, r)
	}))
}

func assertLegacyPersonReduction(t *testing.T, source string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "profiles")
	m, err := Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: out, Scanner: scanner(t, DefaultPolicy()), PeopleMode: "profiles"}})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Rebuild.LegacyPersonEvidence || m.Rebuild.LegacyPersonsReconciled != 4 || m.Rebuild.LegacyPersonsUnknown != 1 || m.Status != "partial" {
		t.Fatalf("legacy evidence not reconciled accurately: %+v", m.Rebuild)
	}
	if m.People.Total != 4 || m.People.Selected != 2 || m.People.Omitted != 2 || m.People.Unknown != 1 {
		t.Fatalf("legacy selection must keep the profile and unknown person: %+v", m.People)
	}
	var links map[string]string
	b, _ := os.ReadFile(filepath.Join(out, "link-map.json"))
	_ = json.Unmarshal(b, &links)
	if links[opaque("PERSONS", "unknown")] == "" || links[opaque("PERSONS", "with-profile")] == "" || links[opaque("PERSONS", "known-empty-1")] != "" || links[opaque("PERSONS", "known-empty-2")] != "" {
		t.Fatal("legacy unknown/profile retention incorrect")
	}
	profiles, _ := filepath.Glob(filepath.Join(out, "workstream_summaries/personas/users/*/profile.md"))
	if len(profiles) != 1 {
		t.Fatal("legacy verified user folder lost")
	}
	r := &run{ctx: context.Background(), stage: out, opts: Options{Scanner: scanner(t, DefaultPolicy())}}
	if err := r.validateMarkdownLinks(); err != nil {
		t.Fatal(err)
	}
	if err := r.auditOutput(); err != nil {
		t.Fatal(err)
	}
}

func createLegacyProjectedArchive(t *testing.T) string {
	t.Helper()
	srv := legacyProjectedPeopleServer(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	materials, _ := SelectMaterials("PERSONS,ANNOTATIONS")
	source := filepath.Join(t.TempDir(), "source")
	m, err := Export(context.Background(), c, Options{Output: source, Mode: "filtered", Materials: materials, Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
	srv.Close()
	if err != nil {
		t.Fatal(err)
	}
	m.FormatVersion, m.ArchiveState = 4, nil
	if err := rewriteJSON(filepath.Join(source, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, archiveStateFile)); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestLegacyProjectedPeopleSelectionRetainsUnknowns(t *testing.T) {
	assertLegacyPersonReduction(t, createLegacyProjectedArchive(t))
}

// Opt-in compatibility proof using the actual older executable, not a fixture
// that merely relabels a new archive. It talks only to this synthetic server.
func TestLegacyBinaryPersonRebuild(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_LEGACY_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_LEGACY_TEST_BINARY to the older exporter executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	srv := legacyProjectedPeopleServer(t)
	defer srv.Close()
	source := filepath.Join(t.TempDir(), "legacy-original")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	args := []string{"export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--materials", "PERSONS,ANNOTATIONS", "--output", source, "--people", "all", "--format", "markdown", "--metadata", "off", "--yes"}
	b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
			t.Fatalf("legacy fixture export failed: %v %s", err, b)
		}
	}
	srv.Close()
	m, err := InspectArchive(source)
	if err != nil || m.FormatVersion != 4 || !strings.HasPrefix(m.ToolVersion, "0.4.") {
		t.Fatalf("unexpected legacy executable/archive version: %v", err)
	}
	assertLegacyPersonReduction(t, source)
	t.Log("actual 0.4.x archive rebuilt offline: 4 synthetic projected people -> 2; profile and unknown history retained")
}
