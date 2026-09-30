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
	"testing"
	"time"
)

// Extracted from current source route declarations and generated required JSON
// fields, rather than inferred by pluralizing API/model names.
//
//go:embed testdata/junction_contract.json
var junctionContract []byte

func TestJunctionSourceContracts(t *testing.T) {
	var contracts []struct {
		Family     string `json:"family"`
		LeftRoute  string `json:"left_route"`
		RightRoute string `json:"right_route"`
		LeftField  string `json:"left_field"`
		RightField string `json:"right_field"`
		LeftType   string `json:"left_type"`
		RightType  string `json:"right_type"`
	}
	if err := json.Unmarshal(junctionContract, &contracts); err != nil || len(contracts) != 87 || len(junctionFamilies) != 87 {
		t.Fatal("current source inventory is invalid", err)
	}
	for _, contract := range contracts {
		t.Run(contract.Family, func(t *testing.T) {
			want := associationFamily{contract.Family, contract.LeftType, contract.RightType, contract.LeftRoute, contract.RightRoute, contract.LeftField, contract.RightField}
			got, ok := junctionFamilyByName(contract.Family)
			if !ok || got != want {
				t.Fatal("source binding differs")
			}
			for _, typ := range []string{got.leftType, got.rightType} {
				if _, ok := materialByType(typ); !ok {
					t.Fatal("unknown endpoint material")
				}
			}
			for _, side := range []string{contract.LeftRoute, contract.RightRoute} {
				owner := "owner +/é?#"
				row := record("junction", "2026-09-30T00:00:00Z")
				row[contract.LeftField], row[contract.RightField] = owner, owner
				row["future_number"] = json.Number("9007199254740993")
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					base := "/" + contract.Family + "/" + side
					switch calls {
					case 1:
						if r.Method != "GET" || r.URL.EscapedPath() != base+"/"+url.PathEscape(owner)+"/count" {
							t.Error("wrong count route")
						}
						json.NewEncoder(w).Encode(map[string]any{"id": owner, "count": 1})
						return
					case 2:
						if r.Method != "GET" || r.URL.EscapedPath() != base+"/"+url.PathEscape(owner) || r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("offset") != "7" || r.URL.Query().Get("transferables") != "false" {
							t.Error("wrong page request")
						}
					case 3:
						var input map[string][]string
						if r.Method != "POST" || r.URL.Path != base+"/bulk" || json.NewDecoder(r.Body).Decode(&input) != nil || !reflect.DeepEqual(input["iterable"], []string{owner}) {
							t.Error("wrong bulk request")
						}
					default:
						t.Error("unexpected request")
					}
					// Current generic pages do not expose legacy pagination metadata.
					json.NewEncoder(w).Encode(map[string]any{"iterable": []any{row}, "indices": map[string]int{}})
				}))
				c, _ := NewClient(srv.URL, time.Second, 1<<20)
				count, err := c.readJunctionCount(context.Background(), contract.Family, side, owner)
				if err != nil || count != 1 {
					t.Fatal("count failed", err)
				}
				rows, err := c.readJunctionPage(context.Background(), contract.Family, side, owner, 5, 7)
				if err != nil || len(rows) != 1 {
					t.Fatal("page failed", err)
				}
				rows, err = c.readJunctionBulk(context.Background(), contract.Family, side, map[string]int{owner: count})
				if err != nil || len(rows) != 1 || rows[0]["future_number"] != json.Number("9007199254740993") {
					t.Fatal("bulk or metadata retention failed", err)
				}
				srv.Close()
			}
		})
	}
}

func TestJunctionReadsRejectMalformedAndUnboundResponses(t *testing.T) {
	const family = "workstream_summary_to_annotation_associations"
	const side = "workstream_summary"
	const owner = "PRIVATE_OWNER_SENTINEL"
	for _, scenario := range []string{"truncated", "bad-truncated", "duplicate", "wrong-owner", "missing-peer", "bad-time", "wrong-index", "negative-index", "missing-rows", "bad-rows", "too-many", "partial-owner-count", "not-found"} {
		t.Run(scenario, func(t *testing.T) {
			wantCount := 1
			if scenario == "duplicate" {
				wantCount = 2 // Count matches: identity validation must catch this.
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				row := record("row", "2026-09-30T00:00:00Z")
				row["workstreamSummary"], row["annotation"] = owner, "annotation"
				v := map[string]any{"iterable": []any{row}, "indices": map[string]int{}}
				switch scenario {
				case "truncated":
					v["truncated"] = true
				case "bad-truncated":
					v["truncated"] = "false"
				case "duplicate":
					v["iterable"] = []any{row, row}
				case "wrong-owner":
					row["workstreamSummary"] = "different"
				case "missing-peer":
					delete(row, "annotation")
				case "bad-time":
					row["created"] = "invalid"
				case "wrong-index":
					v["indices"] = map[string]int{"missing": 0}
				case "negative-index":
					v["indices"] = map[string]int{"row": -1}
				case "missing-rows":
					delete(v, "iterable")
				case "bad-rows":
					v["iterable"] = []any{"row"}
				case "too-many":
					v["iterable"] = []any{row, row, row}
				case "partial-owner-count":
					v["iterable"] = []any{}
				case "not-found":
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(v)
			}))
			defer srv.Close()
			c, _ := NewClient(srv.URL, time.Second, 1<<20)
			_, err := c.readJunctionBulk(context.Background(), family, side, map[string]int{owner: wantCount})
			if err == nil || strings.Contains(err.Error(), owner) {
				t.Fatal("invalid bulk accepted or private identity logged", err)
			}
			if scenario == "not-found" {
				var api *APIError
				if !errors.As(err, &api) || api.Status != 404 {
					t.Fatal("capability failure was not retained")
				}
			}
		})
	}
	for _, count := range []any{-1, 1.5, "1", nil, maxAssociationPairs + 1} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"id": owner, "count": count})
		}))
		c, _ := NewClient(srv.URL, time.Second, 1<<20)
		if _, err := c.readJunctionCount(context.Background(), family, side, owner); err == nil {
			t.Fatal("invalid count accepted")
		}
		srv.Close()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": "different-owner", "count": 1})
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 1<<20)
	if _, err := c.readJunctionCount(context.Background(), family, side, owner); err == nil {
		t.Fatal("valid count for the wrong owner accepted")
	}
}

func TestJunctionReadsBoundBeforeRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 1<<20)
	const family, side = "workstream_summary_to_annotation_associations", "workstream_summary"
	for _, input := range []map[string]int{nil, {"id": -1}, {"id": maxJunctionBulkRows + 1}, {"a": maxJunctionBulkRows, "b": 1}, {"": 0}} {
		if _, err := c.readJunctionBulk(context.Background(), family, side, input); err == nil {
			t.Fatal("invalid bulk bound accepted")
		}
	}
	if _, err := c.readJunctionPage(context.Background(), family, side, "id", 501, 0); err == nil {
		t.Fatal("invalid page bound accepted")
	}
	if _, err := c.readJunctionCount(context.Background(), "unrecognized", side, "id"); err == nil {
		t.Fatal("unrecognized route accepted")
	}
	if calls != 0 {
		t.Fatal("invalid requests reached OS")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.readJunctionCount(ctx, family, side, "id"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}
