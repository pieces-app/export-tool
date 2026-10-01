package exporter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pdfread "github.com/ledongthuc/pdf"
)

// Exercise the distinction with real source sanitization, capture, Markdown/PDF
// rendering and offline reconstruction, rather than only graph state fixtures.
func TestPersonNavigationMissingVersusDeniedThroughRecovery(t *testing.T) {
	for _, denied := range []bool{false, true} {
		name := "missing"
		if denied {
			name = "denied"
		}
		t.Run(name, func(t *testing.T) {
			f := missingJunctionFixture(true)
			if denied {
				person := record("unavailable-person", "")
				person["url"] = "https://bank.example/private"
				f.data["PERSONS"] = append(f.data["PERSONS"], person)
			}
			policy := DefaultPolicy()
			policy.Deny = []DomainRule{{"bank.example", true}}
			o := junctionExportOptions(t)
			o.Format, o.Scanner = "both", scanner(t, policy)
			store, _ := captureTestStore(t)
			o.captureCheckpoint = func(r *run) error {
				want := "missing"
				if denied {
					want = "excluded"
				}
				if m := r.meta["PERSONS\x00unavailable-person"]; m == nil || m.State != want {
					t.Fatal("source decision was not captured before graph filtering")
				}
				return r.saveCapture(store)
			}
			srv := junctionServer(t, f, nil)
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			if _, err := Export(context.Background(), client, o); err != nil {
				t.Fatal(err)
			}
			srv.Close()
			outputs := []string{o.Output, filepath.Join(t.TempDir(), "replay"), filepath.Join(t.TempDir(), "rebuilt")}
			if _, err := replayCapture(context.Background(), store, outputs[1], nil); err != nil {
				t.Fatal(err)
			}
			if _, err := Rebuild(context.Background(), RebuildOptions{Source: outputs[1], Options: Options{Output: outputs[2], Format: "both", Scanner: scanner(t, policy)}}); err != nil {
				t.Fatal(err)
			}
			for _, output := range outputs {
				// All navigation must keep working after moving the whole archive.
				moved := output + "-moved"
				if err := os.Rename(output, moved); err != nil {
					t.Fatal(err)
				}
				counts, err := inspectFinalArchive(context.Background(), moved)
				if err != nil {
					t.Fatal("reconstructed archive failed independent body/graph/link checks", err)
				}
				if denied {
					if counts.Summaries != 0 || counts.States["excluded"] == 0 {
						t.Fatal("privacy-excluded person no longer withholds dependent summary")
					}
					requireNoCapturePlaintext(t, moved, "Current linked narrative", "Retained profile annotation", "bank.example")
					continue
				}
				if counts.SummariesWithBody != 1 || counts.States["missing"] != 1 {
					t.Fatal("missing-person fallback changed source gap or summary coverage")
				}
				assertMissingPersonNavigation(t, moved)
				var links map[string]string
				b, _ := os.ReadFile(filepath.Join(moved, "link-map.json"))
				if err := json.Unmarshal(b, &links); err != nil {
					t.Fatal(err)
				}
				path := links[opaque("WORKSTREAM_SUMMARIES", "summary")]
				f, pdf, err := pdfread.Open(filepath.Join(moved, pdfPath(path)))
				if err != nil {
					t.Fatal("summary PDF was not retained", err)
				}
				var text strings.Builder
				for page := 1; page <= pdf.NumPage(); page++ {
					s, err := pdf.Page(page).GetPlainText(nil)
					if err != nil {
						f.Close()
						t.Fatal(err)
					}
					text.WriteString(s)
				}
				f.Close()
				if !strings.Contains(text.String(), "Unavailable colleague") || strings.Contains(text.String(), "pieces://persons/unavailable-person") {
					t.Fatal("PDF lost the plain-text unavailable-person label")
				}
			}
		})
	}
}
