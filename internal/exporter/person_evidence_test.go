package exporter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCombinedPersonHistoryPreservesTypesAndTimestampTies(t *testing.T) {
	for _, test := range []struct {
		name                 string
		rows, want, requests int
		ties, single, reject bool
		unknown, eventCounts bool
	}{
		{name: "empty", requests: 1},
		{name: "both_types", rows: 2, want: 2, requests: 1},
		{name: "overlapping_pages", rows: 75, want: 75, requests: 2},
		{name: "mixed_ties_fallback", rows: 60, want: 60, requests: 4, ties: true},
		{name: "saturated_single_type", rows: 75, want: 50, requests: 5, ties: true, single: true, unknown: true},
		{name: "older_server_fallback", rows: 2, want: 2, requests: 3, reject: true},
		{name: "connected_counts", rows: 2, want: 2, requests: 1, eventCounts: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := record("person", "")
			rows := []map[string]any{}
			for i := 0; i < test.rows; i++ {
				created := time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC)
				if test.ties {
					created = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				}
				a := record(fmt.Sprintf("annotation-%03d", i), created.Format(time.RFC3339))
				a["type"], a["person"] = "HIERARCHICAL_PROFILE_SUMMARY", map[string]any{"id": "person"}
				if i%2 != 0 && !test.single {
					a["type"] = "PROFILE_DESCRIPTION"
				}
				rows = append(rows, a)
			}
			f := &fakeOS{data: map[string][]map[string]any{"PERSONS": {p}, "ANNOTATIONS": rows}}
			base := f.server(t)
			defer base.Close()
			var annotations, counts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/person/person/annotations" {
					annotations.Add(1)
					if test.reject {
						b, _ := io.ReadAll(r.Body)
						_ = r.Body.Close()
						r.Body = io.NopCloser(bytes.NewReader(b))
						var input struct {
							Filter struct{ Types []string } `json:"filter"`
						}
						_ = json.Unmarshal(b, &input)
						if len(input.Filter.Types) > 1 {
							http.Error(w, "single type only", http.StatusBadRequest)
							return
						}
					}
				}
				if strings.HasPrefix(r.URL.Path, "/workstream_event_to_person_associations/") {
					counts.Add(1)
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer srv.Close()
			c, _ := NewClient(srv.URL, time.Second, 8<<20)
			facts := personFacts(p)
			values, err := personEvidence(context.Background(), c, facts, true, test.eventCounts)
			if err != nil || len(values) != test.want || facts.UnknownAnnotations != test.unknown || int(annotations.Load()) != test.requests {
				t.Fatalf("history: records=%d unknown=%t requests=%d err=%v", len(values), facts.UnknownAnnotations, annotations.Load(), err)
			}
			wantCounts := int32(0)
			if test.eventCounts {
				wantCounts = 1
			}
			if counts.Load() != wantCounts || facts.UnknownConnections == test.eventCounts {
				t.Fatal("event counts were fetched unnecessarily or unqueried counts became known zero")
			}
			seen := map[string]bool{}
			for _, v := range values {
				id := fieldString(v, "id")
				if seen[id] {
					t.Fatal("overlap/fallback duplicated an annotation")
				}
				seen[id] = true
			}
		})
	}
}

func TestProfileExportAndRebuildPreserveUnqueriedEventCounts(t *testing.T) {
	f := summaryScopeFixture()
	p := f.data["PERSONS"][0]
	delete(p, "annotations")
	delete(p, "summaries")
	srv := f.server(t)
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "archive")
	m, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: out, Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), PeopleMode: "profiles"})
	srv.Close()
	if err != nil || m.People.Selected != 1 || m.People.UnknownEventConnections != 1 {
		t.Fatalf("profile export lost unknown-count evidence: %v %+v", err, m.People)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "/workstream_event_to_person_associations/") {
			t.Fatal("profile export fetched event connectivity")
		}
	}
	rebuilt, err := Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: filepath.Join(t.TempDir(), "rebuilt"), Scanner: scanner(t, DefaultPolicy()), PeopleMode: "profiles"}})
	if err != nil || rebuilt.People.Selected != 1 || rebuilt.People.UnknownEventConnections != 1 {
		t.Fatalf("offline rebuild lost profile or changed unqueried counts to zero: %v %+v", err, rebuilt.People)
	}
	_, err = Rebuild(context.Background(), RebuildOptions{Source: out, Options: Options{Output: filepath.Join(t.TempDir(), "widened"), Scanner: scanner(t, DefaultPolicy()), PeopleMode: "connected"}})
	if err == nil || !strings.Contains(err.Error(), "omitted people cannot be restored") {
		t.Fatal("offline rebuild widened a profile-only archive without source evidence")
	}
}

func TestProfilePreviewKeepsBothTypesWithoutEventQueries(t *testing.T) {
	p := record("person", "")
	persona, profile := record("persona", "2026-01-01T00:00:00Z"), record("profile", "2026-01-02T00:00:00Z")
	persona["type"], profile["type"] = "HIERARCHICAL_PROFILE_SUMMARY", "PROFILE_DESCRIPTION"
	persona["persons"], profile["persons"] = refs("person"), refs("person")
	f := &fakeOS{data: map[string][]map[string]any{"PERSONS": {p}, "ANNOTATIONS": {persona, profile}}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	var out bytes.Buffer
	stats, err := PeopleReport(context.Background(), c, "profiles", 10, &out)
	if err != nil || stats.Personas != 1 || stats.Profiles != 1 || stats.Selected != 1 || stats.UnknownEventConnections != 1 || stats.Unknown != 0 {
		t.Fatalf("preview did not independently check each annotation type: %v %+v", err, stats)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "/workstream_event_to_person_associations/") {
			t.Fatal("profile preview queried unnecessary event counts")
		}
	}
	if !strings.Contains(out.String(), "source event counts unavailable/not queried 1") {
		t.Fatal("preview hid unknown event connectivity")
	}
}

func TestPersonHistoryCancellationStopsFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		http.Error(w, "canceled fixture", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	_, err := personEvidence(ctx, c, &PersonFacts{ID: "person"}, true, true)
	if err == nil || ctx.Err() == nil || requests.Load() != 1 {
		t.Fatal("canceled history continued into fallback/count reads")
	}
}

// Explicit opt-in, current-account history only, bounded time and sequential
// requests. Compare IDs/types/timestamps in memory; never log private values.
func TestLiveCombinedPersonHistory(t *testing.T) {
	base := os.Getenv("PIECES_EXPORT_LIVE_PERSON_HISTORY_URL")
	if base == "" {
		t.Skip("set PIECES_EXPORT_LIVE_PERSON_HISTORY_URL to opt into local reads")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := NewClient(base, 8*time.Second, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ConfigurePerformance("adaptive", 5, 250*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := currentUserID(ctx, c)
	if err != nil || user == "" {
		t.Fatal("current account could not be resolved")
	}
	person, err := userPersonID(ctx, c, user)
	if err != nil || person == "" {
		t.Fatal("current account person could not be resolved")
	}
	types := []string{"HIERARCHICAL_PROFILE_SUMMARY", "PROFILE_DESCRIPTION"}
	before := c.Performance().Requests
	combined, complete, err := personAnnotationTypes(ctx, c, person, types, true)
	if err != nil || !complete {
		t.Fatal("combined history could not establish complete pagination")
	}
	combinedRequests := c.Performance().Requests - before
	fingerprint := func(row map[string]any) string {
		text, _ := json.Marshal(row["text"])
		return fieldString(row, "type") + "\x00" + timestamp(row, "created") + "\x00" + timestamp(row, "updated") + fmt.Sprintf("\x00%x", sha256.Sum256(text))
	}
	comparison := map[string]string{}
	before = c.Performance().Requests
	for _, typ := range types {
		rows, complete, err := personAnnotations(ctx, c, person, typ, true)
		if err != nil || !complete {
			t.Fatal("single-type comparison could not establish complete pagination")
		}
		for _, row := range rows {
			comparison[fieldString(row, "id")] = fingerprint(row)
		}
	}
	singleRequests := c.Performance().Requests - before
	if len(combined) != len(comparison) {
		t.Fatal("combined/single-type inventories differ; source changes or query compatibility need investigation")
	}
	nonempty := 0
	for _, row := range combined {
		if comparison[fieldString(row, "id")] != fingerprint(row) {
			t.Fatal("combined/single-type record identity/version/text mismatch")
		}
		if fieldString(row, "text") != "" {
			nonempty++
		}
	}
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("Current-account history: %d matching records including text; nonempty bodies=%d; combined requests=%d single-type requests=%d; final health passed; performance=%+v", len(combined), nonempty, combinedRequests, singleRequests, c.Performance())
}
