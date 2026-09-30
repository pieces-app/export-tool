package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPrivacyRescanKeepsUnchangedFileAndRewritesLateCredential(t *testing.T) {
	material, _ := materialByType("TAGS")
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}, meta: map[string]*Meta{}, coverage: map[string]*Coverage{"TAGS": {Material: "TAGS"}}}
	for _, id := range []string{"unchanged", "late-secret"} {
		v := record(id, "2026-09-30T00:00:00Z")
		v["name"] = "Ordinary synthetic topic"
		if id == "late-secret" {
			v["description"] = "An earlier copy of very-short-credential."
		}
		if err := r.store(material, v, false); err != nil {
			t.Fatal(err)
		}
	}
	paths, before, data := map[string]string{}, map[string]os.FileInfo{}, map[string][]byte{}
	oldTime := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	for id := range map[string]bool{"unchanged": true, "late-secret": true} {
		path := filepath.Join(r.stage, r.meta["TAGS\x00"+id].DataPath)
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
		paths[id] = path
		var err error
		before[id], err = os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		data[id], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(data["late-secret"], []byte("very-short-credential")) {
		t.Fatal("fixture was already redacted before learning the late credential")
	}
	r.opts.Scanner.remember("very-short-credential")
	var progress bytes.Buffer
	r.progress = startProgress(&progress, nil)
	err := r.rescanKnownCredentials()
	r.progress.Close()
	if err != nil {
		t.Fatal(err)
	}
	for id, path := range paths {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if id == "unchanged" {
			if !os.SameFile(before[id], st) || !st.ModTime().Equal(before[id].ModTime()) || !bytes.Equal(b, data[id]) {
				t.Fatal("unchanged record was rewritten")
			}
		} else if bytes.Contains(b, []byte("very-short-credential")) || bytes.Equal(b, data[id]) || st.ModTime().Equal(oldTime) {
			t.Fatal("late credential was not safely rewritten")
		}
	}
	if !strings.Contains(progress.String(), "Privacy reconciliation (2 records)") || !strings.Contains(progress.String(), "2/2 (100.0%)") {
		t.Fatal("privacy phase omitted record progress")
	}
}

func TestPruneReferencesReportsOnlyActualDeletions(t *testing.T) {
	metas := map[string]*Meta{"TAGS\x00included": {State: "included"}, "TAGS\x00private": {State: "excluded"}}
	for _, scenario := range []string{"none", "unchanged", "indices", "iterable", "single"} {
		t.Run(scenario, func(t *testing.T) {
			v := map[string]any{"name": "Fixture"}
			switch scenario {
			case "unchanged":
				v["tags"] = map[string]any{"indices": map[string]any{"included": 0, "unavailable": 1}, "iterable": []any{map[string]any{"id": "included"}}}
			case "indices":
				v["tags"] = map[string]any{"indices": map[string]any{"included": 0, "private": -1}}
			case "iterable":
				v["tags"] = map[string]any{"iterable": []any{map[string]any{"id": "included"}, "private"}}
			case "single":
				v["tags"] = map[string]any{"id": "private"}
			}
			before, _ := json.Marshal(v)
			changed := pruneReferences(v, metas)
			after, _ := json.Marshal(v)
			wantChanged := scenario != "none" && scenario != "unchanged"
			if changed != wantChanged || changed == bytes.Equal(before, after) || bytes.Contains(after, []byte("private")) {
				t.Fatal("pruning changed flag or privacy deletion was incorrect")
			}
			if pruneReferences(v, metas) {
				t.Fatal("repeated pruning of an unchanged record requested a rewrite")
			}
		})
	}
}

// Isolates the previously mandatory disk rewrite from the exact equality
// check. This is a local I/O microbenchmark, not whole-export throughput.
func BenchmarkUnchangedRecordRewrite(b *testing.B) {
	for _, alwaysRewrite := range []bool{true, false} {
		name := "compare-and-retain"
		if alwaysRewrite {
			name = "always-rewrite"
		}
		b.Run(name, func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "record.json")
			v := map[string]any{"id": "fixture", "name": "Ordinary topic", "description": strings.Repeat("Synthetic retained context. ", 40)}
			if err := writeJSON(path, v); err != nil {
				b.Fatal(err)
			}
			original, err := readRecord(path)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if alwaysRewrite || !reflect.DeepEqual(original, v) {
					if err := rewriteJSON(path, v); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
