package exporter

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeSecret() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return "ghp_" + hex.EncodeToString(b)
}
func scanner(t *testing.T, p Policy) *Scanner {
	t.Helper()
	s, err := NewScanner(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func record(id, created string) map[string]any {
	return map[string]any{"id": id, "created": map[string]any{"value": created}, "updated": map[string]any{"value": created}}
}
func refs(ids ...string) map[string]any {
	indices := map[string]any{}
	for i, id := range ids {
		indices[id] = i
	}
	return map[string]any{"indices": indices, "iterable": []any{}}
}

type fakeOS struct {
	healthIdentity     string
	updatedUnavailable bool
	requestedMaterials []string
	beforeBatch        func()
	data               map[string][]map[string]any
	batchMissing       string
	calls              []string
	summaryChildren    map[string][]string
	currentUserID      string
	userPersons        map[string]string
}

func (f *fakeOS) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		associationParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(associationParts) == 5 && r.Method == "GET" {
			if family, ok := associationFamilyByName(associationParts[0]); ok && associationParts[1] == family.leftRoute && associationParts[3] == family.rightRoute {
				for _, v := range f.data[family.material().Type] {
					if fieldString(v, family.leftField) == associationParts[2] && fieldString(v, family.rightField) == associationParts[4] {
						write(v)
						return
					}
				}
				http.NotFound(w, r)
				return
			}
		}
		if r.URL.Path == "/.well-known/health" || r.URL.Path == "/.well-known/version" {
			if r.URL.Path == "/.well-known/health" {
				if f.healthIdentity != "" {
					write("ok:" + f.healthIdentity)
				} else {
					write("ok:macos")
				}
			} else {
				write("12.3.108")
			}
			return
		}
		// Hierarchy is intentionally read through its dedicated endpoint, not by
		// assuming that batch/singular summary snapshots include associations.
		hierarchy := map[string][]string{}
		for parent, children := range f.summaryChildren {
			hierarchy[parent] = append(hierarchy[parent], children...)
		}
		for _, summary := range f.data["WORKSTREAM_SUMMARIES"] {
			id := fieldString(summary, "id")
			for _, child := range references(summary["children"]) {
				hierarchy[id] = append(hierarchy[id], child)
			}
			for _, parent := range references(summary["parents"]) {
				hierarchy[parent] = append(hierarchy[parent], id)
			}
		}
		for parent, children := range hierarchy {
			hierarchy[parent] = unique(children)
		}
		flattened := func(ids []string) map[string]any {
			items := make([]any, 0, len(ids))
			for _, id := range unique(ids) {
				items = append(items, map[string]any{"id": id})
			}
			return map[string]any{"iterable": items}
		}
		if r.Method == "GET" && r.URL.Path == "/workstream_summaries/parent/identifiers" {
			parents := make([]string, 0, len(hierarchy))
			for parent := range hierarchy {
				parents = append(parents, parent)
			}
			write(flattened(parents))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/workstream_summaries/child/identifiers" {
			children := []string{}
			for _, values := range hierarchy {
				children = append(children, values...)
			}
			write(flattened(children))
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/workstream_summary/") && strings.HasSuffix(r.URL.Path, "/child/identifiers") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/workstream_summary/"), "/child/identifiers")
			write(flattened(hierarchy[id]))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/user" {
			if f.currentUserID == "" {
				http.NotFound(w, r)
				return
			}
			write(map[string]any{"user": map[string]any{"id": f.currentUserID}})
			return
		}
		// Fixture implementations of the read-only person evidence endpoints.
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if r.Method == "GET" && len(parts) == 3 && parts[0] == "user" && parts[2] == "person" {
			personID := f.userPersons[parts[1]]
			for _, person := range f.data["PERSONS"] {
				if personID != "" && fieldString(person, "id") == personID {
					write(person)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		if len(parts) == 3 && parts[0] == "person" && parts[2] == "annotations" {
			var input struct {
				Filter struct {
					Types   []string                     `json:"types"`
					Created map[string]map[string]string `json:"created"`
				} `json:"filter"`
				Limit int `json:"limit"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			var person map[string]any
			for _, p := range f.data["PERSONS"] {
				if fieldString(p, "id") == parts[1] {
					person = p
				}
			}
			if person == nil {
				http.NotFound(w, r)
				return
			}
			rows := []map[string]any{}
			for _, a := range f.data["ANNOTATIONS"] {
				match := false
				for _, id := range append(references(a["persons"]), references(a["person"])...) {
					if id == parts[1] {
						match = true
					}
				}
				for _, id := range references(person["annotations"]) {
					if id == fieldString(a, "id") {
						match = true
					}
				}
				allowed := false
				for _, typ := range input.Filter.Types {
					if typ == fieldString(a, "type") {
						allowed = true
					}
				}
				if bound := input.Filter.Created["to"]["value"]; bound != "" && timestamp(a, "created") > bound {
					match = false
				}
				if match && allowed {
					rows = append(rows, a)
				}
			}
			sort.Slice(rows, func(i, j int) bool { return timestamp(rows[i], "created") > timestamp(rows[j], "created") })
			if input.Limit > 0 && len(rows) > input.Limit {
				rows = rows[:input.Limit]
			}
			write(map[string]any{"person": person, "annotations": map[string]any{"iterable": rows}})
			return
		}
		if len(parts) == 3 && parts[0] == "workstream_event_to_person_associations" && parts[1] == "person" {
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			if limit != 1 {
				rows := []map[string]any{}
				for _, v := range f.data["WORKSTREAM_EVENT_TO_PERSON_ASSOCIATIONS"] {
					if fieldString(v, "person") == parts[2] {
						rows = append(rows, v)
					}
				}
				sort.Slice(rows, func(i, j int) bool { return fieldString(rows[i], "id") < fieldString(rows[j], "id") })
				total := len(rows)
				start, end := min(max(0, offset), total), min(max(0, offset)+max(1, limit), total)
				write(map[string]any{"iterable": rows[start:end], "total": total, "limit": limit, "offset": offset})
				return
			}
			ids := map[string]bool{}
			for _, p := range f.data["PERSONS"] {
				if fieldString(p, "id") == parts[2] {
					for _, id := range references(p["workstream_events"]) {
						ids[id] = true
					}
				}
			}
			for _, e := range f.data["WORKSTREAM_EVENTS"] {
				for _, id := range references(e["persons"]) {
					if id == parts[2] {
						ids[fieldString(e, "id")] = true
					}
				}
			}
			write(map[string]any{"iterable": []any{}, "total": len(ids)})
			return
		}
		if r.URL.Path == "/materials/metrics" || r.URL.Path == "/materials/identifiers" {
			var input map[string]any
			_ = json.NewDecoder(r.Body).Decode(&input)
			typ, _ := input["material_type"].(string)
			f.requestedMaterials = append(f.requestedMaterials, typ)
			records, ok := f.data[typ]
			if !ok {
				http.Error(w, "unsupported", 404)
				return
			}
			filters := input
			if x, ok := input["filters"].(map[string]any); ok {
				filters = x
			}
			if filters["updated"] != nil && f.updatedUnavailable {
				http.Error(w, "unsupported", 404)
				return
			}
			window, _ := filters["created"].(map[string]any)
			ids := []string{}
			for _, v := range records {
				include := true
				date, err := time.Parse(time.RFC3339Nano, timestamp(v, "created"))
				if window != nil && err != nil {
					continue
				}
				for k, raw := range window {
					bound, _ := raw.(map[string]any)
					ts, _ := time.Parse(time.RFC3339Nano, fieldString(bound, "value"))
					if k == "from" && date.Before(ts) || k == "to" && date.After(ts) {
						include = false
					}
				}
				if updated, ok := filters["updated"].(map[string]any); ok {
					date, e := time.Parse(time.RFC3339Nano, timestamp(v, "updated"))
					if e != nil {
						include = false
					}
					for k, raw := range updated {
						bound, _ := raw.(map[string]any)
						ts, _ := time.Parse(time.RFC3339Nano, fieldString(bound, "value"))
						if k == "from" && date.Before(ts) || k == "to" && date.After(ts) {
							include = false
						}
					}
				}
				if include {
					ids = append(ids, fieldString(v, "id"))
				}
			}
			if r.URL.Path == "/materials/metrics" {
				write(map[string]any{"total_count": len(ids)})
			} else {
				write(map[string]any{"identifiers": ids})
			}
			return
		}
		for _, m := range Materials {
			if r.URL.Path == m.Batch && m.Batch != "" {
				if f.beforeBatch != nil {
					f.beforeBatch()
				}
				var input map[string]any
				_ = json.NewDecoder(r.Body).Decode(&input)
				want := references(input[m.Field])
				items := []any{}
				missing := []string{}
				for _, id := range want {
					found := false
					for _, v := range f.data[m.Type] {
						if fieldString(v, "id") == id && id != f.batchMissing {
							items = append(items, v)
							found = true
						}
					}
					if !found {
						missing = append(missing, id)
					}
				}
				write(map[string]any{m.Field: map[string]any{"iterable": items}, "notFound": missing})
				return
			}
			if m.Singular != "" && strings.HasPrefix(r.URL.Path, m.Singular) {
				id := strings.TrimPrefix(r.URL.Path, m.Singular)
				for _, v := range f.data[m.Type] {
					if fieldString(v, "id") == id {
						write(v)
						return
					}
				}
			}
			if r.URL.Path == m.Collection && m.SnapshotOnly {
				write(map[string]any{"iterable": f.data[m.Type]})
				return
			}
		}
		http.NotFound(w, r)
	}))
}

func TestFilteredExportChronologyGraphAndLeaks(t *testing.T) {
	secret := fakeSecret()
	evt := record("event-secret", "2026-09-20T14:00:00Z")
	evt["readable"] = "token: " + secret
	evt["description"] = "An earlier copy of very-short-credential found in a later profile."
	evt["url"] = "https://reader:temporary-pass@docs.example/page?token=short-query-credential&view=normal"
	evt["context"] = map[string]any{"native_clipboard": map[string]any{"content": map[string]any{"text": secret, "html": "<p>" + secret + "</p>"}}}
	denied := record("event-denied", "2026-09-19T14:00:00Z")
	denied["browserUrl"] = "https://account.bank.example/balance"
	denied["readable"] = "private balance description"
	annotation := record("annotation-1", "2026-09-21T14:00:00Z")
	annotation["type"] = "SUMMARY"
	annotation["text"] = "Discussed [Alex](pieces://persons/person-1). Credential: " + secret
	summary := record("summary-1", "2026-09-22T14:00:00Z")
	summary["name"] = "Retained summary"
	summary["annotations"] = refs("annotation-1")
	summary["events"] = refs("event-secret")
	summary["persons"], summary["pipelines"] = refs(), refs()
	privateBody := record("annotation-private", "2026-09-21T14:00:00Z")
	privateBody["type"] = "SUMMARY"
	privateBody["text"] = "private balance description derived without URL"
	privateSummary := record("summary-private", "2026-09-22T14:00:00Z")
	privateSummary["annotations"] = refs("annotation-private")
	privateSummary["events"] = refs("event-denied")
	person := record("person-1", "2026-01-01T00:00:00Z")
	person["type"] = map[string]any{"platform": map[string]any{"name": "Alex", "apiKeys": []any{"very-short-credential"}}}
	person["annotations"] = refs("annotation-1")
	person["summaries"] = refs()
	undated := record("tag-undated", "")
	undated["text"] = "No date"
	f := &fakeOS{data: map[string][]map[string]any{"WORKSTREAM_EVENTS": {evt, denied}, "ANNOTATIONS": {annotation, privateBody}, "WORKSTREAM_SUMMARIES": {summary, privateSummary}, "PERSONS": {person}, "TAGS": {undated}}, batchMissing: "person-1"}
	srv := f.server(t)
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	p := DefaultPolicy()
	p.Deny = []DomainRule{{"bank.example", true}}
	s := scanner(t, p)
	mats, _ := SelectMaterials("WORKSTREAM_EVENTS,ANNOTATIONS,WORKSTREAM_SUMMARIES,PERSONS,TAGS")
	out := filepath.Join(t.TempDir(), "result")
	manifest, err := Export(context.Background(), client, Options{Output: out, Mode: "filtered", Timezone: "America/New_York", Version: "test", Format: "both", Materials: mats, BatchSize: 2, WindowIDs: 5000, Scanner: s})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "complete_for_implemented_scope" {
		t.Fatalf("unexpected status: %+v", manifest.Issues)
	}
	joined := strings.Builder{}
	err = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			joined.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{secret, "very-short-credential", "private balance description", "account.bank.example", "temporary-pass", "short-query-credential"} {
		if strings.Contains(joined.String(), private) {
			t.Fatal("private fixture survived the final archive")
		}
	}
	if !strings.Contains(joined.String(), "[REDACTED:") {
		t.Fatal("expected visible redaction markers")
	}
	if _, err := os.Stat(filepath.Join(out, "raw")); !os.IsNotExist(err) {
		t.Fatal("filtered export contains raw folder")
	}
	if _, err := os.Stat(out + ".partial"); !os.IsNotExist(err) {
		t.Fatal("staging directory was not finalized")
	}
	data, err := os.ReadFile(filepath.Join(out, "timeline/records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	previous := time.Time{}
	for _, line := range lines {
		var e TimelineEntry
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		ts, _ := time.Parse(time.RFC3339Nano, e.Created)
		if ts.Before(previous) {
			t.Fatal("timeline is not chronological")
		}
		previous = ts
		if _, err = os.Stat(filepath.Join(out, e.Path)); err != nil {
			t.Fatal("broken timeline path")
		}
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "GET /person/person-1") {
		t.Fatal("batch failure did not fall back to singular read")
	}
	for _, call := range f.calls {
		if strings.Contains(call, "create") || strings.Contains(call, "update") || strings.Contains(call, "delete") {
			t.Fatalf("unexpected mutation: %s", call)
		}
	}
}

func TestWindowedIDsIncludesTiesAndUnfilteredUndatedAudit(t *testing.T) {
	records := []map[string]any{record("a", "2026-01-01T00:00:00Z"), record("b", "2026-01-02T00:00:00Z"), record("c", "2026-01-02T00:00:00Z"), record("d", "2026-01-03T00:00:00Z")}
	f := &fakeOS{data: map[string][]map[string]any{"TAGS": records}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 1<<20)
	m, _ := materialByType("TAGS")
	lo, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	hi := lo.Add(48 * time.Hour)
	ids, err := c.WindowedIDs(context.Background(), m, Window{&lo, &hi}, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "a,b,c,d" {
		t.Fatalf("lost boundary/tie IDs: %v", ids)
	}
	if len(f.calls) < 3 {
		t.Fatal("did not exercise time splitting")
	}
	f.data["TAGS"] = append(f.data["TAGS"], record("undated", ""))
	r := &run{ctx: context.Background(), client: c, opts: Options{WindowIDs: 2}}
	all, err := r.inventoryMaterial(m, &Coverage{})
	if err != nil || strings.Join(all, ",") != "a,b,c,d,undated" {
		t.Fatalf("unfiltered inventory audit lost undated records: %v, %v", all, err)
	}
}

func TestFailedFetchCannotClaimComplete(t *testing.T) {
	f := &fakeOS{data: map[string][]map[string]any{"USERS": {record("unavailable", "2026-01-01T00:00:00Z")}}, batchMissing: "unavailable"}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 1<<20)
	m, _ := SelectMaterials("USERS")
	manifest, err := Export(context.Background(), c, Options{Output: filepath.Join(t.TempDir(), "out"), Mode: "filtered", Timezone: "UTC", Materials: m, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "partial" || len(manifest.Issues) == 0 {
		t.Fatal("missing record was treated as complete")
	}
}

func TestPreservationModeRetainsOriginalsAndRefusesOverwrite(t *testing.T) {
	secret := fakeSecret()
	v := record("a", "2026-01-01T00:00:00Z")
	v["text"] = secret
	f := &fakeOS{data: map[string][]map[string]any{"ANNOTATIONS": {v}}}
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 1<<20)
	m, _ := SelectMaterials("ANNOTATIONS")
	out := filepath.Join(t.TempDir(), "out")
	o := Options{Output: out, Mode: "preserve", Timezone: "UTC", Materials: m, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy())}
	if _, err := Export(context.Background(), c, o); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "raw/annotations", opaque("ANNOTATIONS", "a")+".json"))
	if err != nil || !strings.Contains(string(data), secret) {
		t.Fatal("preservation mode lost original content")
	}
	if _, err := Export(context.Background(), c, o); err == nil {
		t.Fatal("existing export overwritten")
	}
}

func TestSanitizeEncodedFinancialAndCredentialFields(t *testing.T) {
	secret := fakeSecret()
	p := DefaultPolicy()
	s := scanner(t, p)
	v := map[string]any{"id": "record", "apiKeys": []any{"short"}, "text": "4111 1111 1111 1111 and GB82 WEST 1234 5698 7654 32", "fragment": map[string]any{"string": map[string]any{"base64": base64.StdEncoding.EncodeToString([]byte(secret))}}, "file": map[string]any{"bytes": map[string]any{"raw": []any{1, 2, 3}}}}
	out, stats, err := s.Sanitize(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	for _, original := range []string{"short", "4111 1111 1111 1111", "GB82 WEST 1234 5698 7654 32", base64.StdEncoding.EncodeToString([]byte(secret))} {
		if strings.Contains(string(b), original) {
			t.Fatal("sensitive representation survived")
		}
	}
	if stats.Redactions < 4 || stats.WithheldRepresentations != 1 {
		t.Fatalf("unexpected counts: %+v", stats)
	}
}

func TestDomainBoundariesPrecedenceAndOfflineLists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bank.domains"), []byte("bank.example\nother.example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := DefaultPolicy()
	p.Lists = []DomainList{{"bank.domains", "bank"}}
	p.Allow = []DomainRule{{"research.bank.example", false}}
	p.Deny = []DomainRule{{"private.research.bank.example", false}}
	s, err := NewScanner(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{"bank.example": true, "login.bank.example": true, "BANK.EXAMPLE.": true, "bank.example.evil.test": false, "notbank.example": false, "research.bank.example": false, "private.research.bank.example": true} {
		if got := s.HostDenied(host); got != want {
			t.Errorf("%s denied=%v want=%v", host, got, want)
		}
	}
	if _, err := NewScanner(Policy{Version: 1, SourceMode: "denylist", Lists: []DomainList{{"missing", "bank"}}}, dir); err == nil {
		t.Fatal("missing category list silently ignored")
	}
}

func TestMarkdownLinksRespectCodeAndReferenceDefinitions(t *testing.T) {
	person := &Meta{State: "included", Path: "markdown/persons/person.md"}
	records := map[string]*Meta{"PERSONS\x00p": person}
	body := "[**Alex**](pieces://persons/p)\n\n`pieces://persons/p`\n\n```md\n[x](pieces://persons/p)\n```\n\n[Alex][person]\n\n[person]: pieces://persons/p\n"
	out := rewriteMarkdown(body, "markdown/summaries/summary.md", records)
	if strings.Count(out, "../persons/person.md") != 2 {
		t.Fatalf("expected inline and reference rewrites: %s", out)
	}
	if !strings.Contains(out, "`pieces://persons/p`") || !strings.Contains(out, "[x](pieces://persons/p)") {
		t.Fatal("code sample was rewritten")
	}
}

func TestClientRefusesRemoteEndpointsAndRedirects(t *testing.T) {
	for _, base := range []string{"https://example.com", "http://example.com", "http://user@127.0.0.1:39300", "http://127.0.0.1:39300/?token=x"} {
		if _, err := NewClient(base, time.Second, 1024); err == nil {
			t.Errorf("accepted unsafe endpoint %s", base)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://example.com", 302) }))
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 1024)
	if _, err := c.Probe(context.Background()); err == nil {
		t.Fatal("redirect followed")
	}
}
