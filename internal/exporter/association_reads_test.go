package exporter

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Extracted independently from the 23 server route declarations and each
// common-schema required-field list. No user data or database/cache values.
//
//go:embed testdata/association_contract.json
var associationContract []byte

func TestAssociationPairSourceContracts(t *testing.T) {
	var contracts []struct {
		Family, Path, Model string
		Required            []string
	}
	if err := json.Unmarshal(associationContract, &contracts); err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 23 || len(associationFamilies) != len(contracts) {
		t.Fatal("association source-contract inventory differs")
	}
	for _, contract := range contracts {
		t.Run(contract.Family, func(t *testing.T) {
			family, exists := associationFamilyByName(contract.Family)
			if !exists || len(contract.Required) != 5 {
				t.Fatal("source family missing or schema contract changed")
			}
			if _, ok := materialByType(family.leftType); !ok {
				t.Fatal("unknown left material")
			}
			if _, ok := materialByType(family.rightType); !ok {
				t.Fatal("unknown right material")
			}
			left, right := "left +/é", "right?#%suffix"
			parts := strings.Split(contract.Path, "/")
			if len(parts) != 6 {
				t.Fatal("unexpected source route shape")
			}
			parts[3], parts[5] = url.PathEscape(left), url.PathEscape(right)
			expected := strings.Join(parts, "/")
			count := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if r.Method != "GET" || r.URL.EscapedPath() != expected || r.URL.RawQuery != "" {
					t.Error("wrong source route/method or identity escaped as query")
				}
				v := record("actual-association-id", "2026-09-30T00:00:00Z")
				v[contract.Required[1]], v[contract.Required[2]] = left, right
				v["future_field"] = map[string]any{"value": json.Number("9007199254740993")}
				json.NewEncoder(w).Encode(v)
			}))
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 1<<20)
			v, err := client.readAssociationPair(context.Background(), contract.Family, left, right)
			if err != nil || fieldString(v, "id") != "actual-association-id" || count != 1 {
				t.Fatalf("source-contract read failed: %v", err)
			}
			if object(v, "future_field")["value"] != json.Number("9007199254740993") {
				t.Fatal("future metadata or numeric precision was lost")
			}
		})
	}
}

func TestAssociationPairRejectsUnboundResponsesAndRoutes(t *testing.T) {
	for _, scenario := range []string{"wrong-left", "wrong-right", "empty-id", "bad-created", "bad-updated", "null", "array", "trailing", "not-found", "unsupported", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				v := record("association", "2026-09-30T00:00:00Z")
				v["signal"], v["annotation"] = "PRIVATE_LEFT_SENTINEL", "PRIVATE_RIGHT_SENTINEL"
				switch scenario {
				case "wrong-left":
					v["signal"] = "unexpected"
				case "wrong-right":
					v["annotation"] = "unexpected"
				case "empty-id":
					v["id"] = ""
				case "bad-created":
					v["created"] = "unknown"
				case "bad-updated":
					delete(v, "updated")
				case "null":
					w.Write([]byte("null"))
					return
				case "array":
					w.Write([]byte("[]"))
					return
				case "not-found", "unsupported":
					status := 404
					if scenario == "unsupported" {
						status = 501
					}
					w.WriteHeader(status)
					w.Write([]byte("PRIVATE_RESPONSE_SENTINEL"))
					return
				case "oversized":
					v["text"] = strings.Repeat("x", 2<<20)
				}
				json.NewEncoder(w).Encode(v)
				if scenario == "trailing" {
					w.Write([]byte("{}"))
				}
			}))
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 1<<20)
			v, err := client.readAssociationPair(context.Background(), "signal_to_annotation_associations", "PRIVATE_LEFT_SENTINEL", "PRIVATE_RIGHT_SENTINEL")
			if err == nil || v != nil {
				t.Fatal("invalid source response became a verified association")
			}
			if strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("private response/binding appeared in error")
			}
			if scenario == "not-found" || scenario == "unsupported" {
				var api *APIError
				if !errors.As(err, &api) || api.Status != 404 && api.Status != 501 {
					t.Fatal("source availability error lost")
				}
			}
		})
	}
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 1<<20)
	for _, input := range [][3]string{{"../mutate", "a", "b"}, {"signal_to_annotation_associations", ".", "b"}, {"signal_to_annotation_associations", "a", ".."}, {"signal_to_annotation_associations", "a", "\x00b"}} {
		if _, err := client.readAssociationPair(context.Background(), input[0], input[1], input[2]); err == nil {
			t.Fatal("unsafe/unsupported lookup was accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.readAssociationPair(ctx, "signal_to_annotation_associations", "a", "b"); !errors.Is(err, context.Canceled) {
		t.Fatal("pair lookup ignored cancellation")
	}
	if count.Load() != 0 {
		t.Fatal("invalid/cancelled pair lookup contacted source")
	}
}

func TestAssociationBatchAccountingAndMetadata(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong-binding", "unexpected", "duplicate", "missing-accounting", "contradictory", "string-array", "tombstone", "unreturned-index", "invalid-envelope", "absent-iterable"} {
		t.Run(scenario, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/workstream_event_to_person_associations/batch/fetch" || r.URL.RawQuery != "transferables=true" {
					t.Error("incorrect association batch route")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if ids := references(body["associations"]); !reflect.DeepEqual(ids, []string{"found", "unavailable"}) {
					t.Errorf("expected actual, deduplicated association IDs: %v", ids)
				}
				v := record("found", "2026-09-30T00:00:00Z")
				v["workstream_event"], v["person"] = "event", "person"
				v["role"], v["confidence"], v["evidence_type"], v["source_type"], v["explanation"] = "SPEAKER", "HIGH", "EXPLICIT", "AUDIO", "Synthetic extraction explanation."
				found := map[string]any{"iterable": []any{v}, "indices": map[string]any{"found": 0}}
				missing := refs("unavailable")
				envelope := map[string]any{"associations": found, "notFound": missing}
				switch scenario {
				case "wrong-binding":
					v["person"] = "wrong"
				case "unexpected":
					v["id"] = "unrequested"
				case "duplicate":
					found["iterable"] = []any{v, v}
				case "missing-accounting":
					envelope["notFound"] = refs()
				case "contradictory":
					envelope["notFound"] = refs("found", "unavailable")
				case "string-array":
					envelope["notFound"] = []string{"unavailable"}
				case "tombstone":
					found["indices"] = map[string]any{"found": -1}
				case "unreturned-index":
					found["indices"] = map[string]any{"found": 0, "unreturned": 1}
				case "invalid-envelope":
					envelope["associations"] = nil
				case "absent-iterable":
					delete(found, "iterable")
				}
				json.NewEncoder(w).Encode(envelope)
			}))
			defer srv.Close()
			client, _ := NewClient(srv.URL, time.Second, 1<<20)
			requested := []associationReference{{"found", "event", "person"}, {"unavailable", "event2", "person2"}, {"found", "event", "person"}}
			result, err := client.readAssociationBatch(context.Background(), "workstream_event_to_person_associations", requested)
			if scenario != "valid" {
				if err == nil || len(result.records) != 0 {
					t.Fatal("inconsistent batch accepted or partially exposed")
				}
				return
			}
			if err != nil || len(result.records) != 1 || !reflect.DeepEqual(result.unavailable, requested[1:2]) {
				t.Fatalf("association batch failed: %v", err)
			}
			for _, field := range []string{"role", "confidence", "evidence_type", "source_type", "explanation"} {
				if fieldString(result.records[0], field) == "" {
					t.Fatal("association metadata was lost")
				}
			}
		})
	}
}

func TestAssociationBatchBoundsBeforeRequests(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
	defer srv.Close()
	client, _ := NewClient(srv.URL, time.Second, 1<<20)
	for _, refs := range [][]associationReference{make([]associationReference, 51), {{"same", "a", "b"}, {"same", "other", "b"}}, {{"", "a", "b"}}, {{"id", "..", "b"}}} {
		if _, err := client.readAssociationBatch(context.Background(), "signal_to_annotation_associations", refs); err == nil {
			t.Fatal("invalid/bounded input caused a source request")
		}
	}
	if _, err := client.readAssociationBatch(context.Background(), "../unsupported", nil); err == nil {
		t.Fatal("unsupported batch family accepted")
	}
	if result, err := client.readAssociationBatch(context.Background(), "signal_to_annotation_associations", nil); err != nil || len(result.records) != 0 {
		t.Fatal("empty batch should need no source request")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.readAssociationBatch(ctx, "signal_to_annotation_associations", []associationReference{{"id", "a", "b"}}); !errors.Is(err, context.Canceled) {
		t.Fatal("batch cancellation ignored")
	}
	if count.Load() != 0 {
		t.Fatal("invalid/empty/cancelled input contacted source")
	}
}
