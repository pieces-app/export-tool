package exporter

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pdfread "github.com/ledongthuc/pdf"
)

func signalRecord(id string) map[string]any {
	v := record(id, "2026-09-30T00:00:00Z")
	v["name"], v["origin"], v["category"] = id, "HIERARCHICAL_ROLLUP", "INTENT"
	for _, field := range projectionFields("SIGNALS") {
		v[field] = refs()
	}
	return v
}

func assertNoSignalPrivateText(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".pdf" {
			f, pdf, err := pdfread.Open(path)
			if err != nil {
				return err
			}
			var text strings.Builder
			for page := 1; page <= pdf.NumPage(); page++ {
				s, err := pdf.Page(page).GetPlainText(nil)
				if err != nil {
					f.Close()
					return err
				}
				text.WriteString(s)
			}
			f.Close()
			b = []byte(text.String())
		}
		for _, marker := range []string{"Private signal label sentinel", "Private signal narrative sentinel", "bank.example"} {
			if strings.Contains(string(b), marker) || strings.Contains(path, marker) {
				t.Fatal("withheld signal content survived in output")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSignalPrivacyAttachmentsAndSourceDependencies(t *testing.T) {
	for _, direction := range []string{"forward", "inverse"} {
		for _, scenario := range []string{"event", "inverse-event", "website", "inverse-website", "signal", "annotation", "shared-description", "strict", "strict-without-source-rules", "preserve"} {
			t.Run(direction+"/"+scenario, func(t *testing.T) {
				private, clean := signalRecord("private-signal"), signalRecord("clean-signal")
				private["name"] = "Private signal label sentinel"
				privateBody, cleanBody := record("private-body", ""), record("clean-body", "")
				privateBody["type"], privateBody["text"] = "SIGNAL_DESCRIPTION", "Private signal narrative sentinel"
				cleanBody["type"], cleanBody["text"] = "SIGNAL_DESCRIPTION", "Approved unrelated signal narrative."
				if direction == "forward" {
					private["annotations"], clean["annotations"] = refs("private-body"), refs("clean-body")
				} else {
					privateBody["signals"], cleanBody["signals"] = refs("private-signal"), refs("clean-signal")
				}
				event, website := record("event", ""), record("website", "")
				policy := DefaultPolicy()
				policy.Deny = []DomainRule{{"bank.example", true}}
				mode, want := "filtered", 1
				switch scenario {
				case "event", "shared-description":
					event["url"], private["workstream_events"] = "https://bank.example/private", refs("event")
				case "website":
					website["url"], private["websites"] = "https://bank.example/private", refs("website")
				case "inverse-event":
					event["url"], event["signals"] = "https://bank.example/private", refs("private-signal")
				case "inverse-website":
					website["url"], website["signals"] = "https://bank.example/private", refs("private-signal")
				case "signal":
					private["url"] = "https://bank.example/private"
				case "annotation":
					privateBody["url"] = "https://bank.example/private"
				case "strict":
					policy.StrictDerived, want = true, 0
				case "strict-without-source-rules":
					policy.StrictDerived, policy.Deny, want = true, nil, 2
				case "preserve":
					mode, want = "preserve", 2
					private["url"] = "https://bank.example/private"
				}
				if scenario == "shared-description" {
					clean["annotations"] = refs("private-body", "clean-body")
					want = 0
				}
				f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {private, clean}, "ANNOTATIONS": {privateBody, cleanBody}, "WORKSTREAM_EVENTS": {event}, "WEBSITES": {website}}}
				srv := f.server(t)
				defer srv.Close()
				client, _ := NewClient(srv.URL, time.Second, 8<<20)
				materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS,WORKSTREAM_EVENTS,WEBSITES")
				out := filepath.Join(t.TempDir(), "archive")
				m, err := Export(context.Background(), client, Options{Output: out, Mode: mode, Format: "both", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy)})
				if err != nil {
					t.Fatal(err)
				}
				if m.SignalPrivacyVersion != currentSignalPrivacyVersion {
					t.Fatal("signal privacy capability not recorded")
				}
				for _, c := range m.Coverage {
					if (c.Material == "SIGNALS" || c.Material == "ANNOTATIONS") && c.Included != want {
						t.Fatalf("%s included %d, wanted %d", c.Material, c.Included, want)
					}
				}
				if want < 2 {
					assertNoSignalPrivateText(t, out)
				}
				graph, err := os.ReadFile(filepath.Join(out, "relationships.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				if want > 0 && !strings.Contains(string(graph), "derived_inverse") {
					t.Fatal("annotation inverse provenance missing")
				}
				if mode == "filtered" {
					r := &run{ctx: context.Background(), stage: out, opts: Options{Mode: mode, Scanner: scanner(t, policy)}}
					if err := r.auditOutput(); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestLegacySignalPrivacyGuardOnlyWithSourceFiltering(t *testing.T) {
	for _, sourceRules := range []bool{false, true} {
		t.Run(map[bool]string{true: "source-filtering", false: "credentials-only"}[sourceRules], func(t *testing.T) {
			policy := DefaultPolicy()
			if sourceRules {
				policy.Deny = []DomainRule{{"bank.example", true}}
			}
			signal, body := signalRecord("retained"), record("body", "")
			signal["annotations"] = refs("body")
			body["type"], body["text"] = "SIGNAL_DESCRIPTION", "Retained description without direct URLs."
			f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}, "ANNOTATIONS": {body}}}
			srv := f.server(t)
			client, _ := NewClient(srv.URL, time.Second, 8<<20)
			materials, _ := SelectMaterials("SIGNALS,ANNOTATIONS")
			source := filepath.Join(t.TempDir(), "source")
			m, err := Export(context.Background(), client, Options{Output: source, Mode: "filtered", Timezone: "UTC", Materials: materials, BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, policy)})
			srv.Close()
			if err != nil {
				t.Fatal(err)
			}
			m.SignalPrivacyVersion = 0
			b, _ := json.Marshal(m)
			if err := os.WriteFile(filepath.Join(source, "manifest.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			before := archiveHashes(t, source)
			m, err = Rebuild(context.Background(), RebuildOptions{Source: source, Options: Options{Output: source + "-rebuilt", Scanner: scanner(t, policy)}})
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if sourceRules {
				want = 0
			}
			for _, c := range m.Coverage {
				if c.Included != want {
					t.Fatalf("legacy source-rule guard: %s retained %d, want %d", c.Material, c.Included, want)
				}
			}
			if sourceRules && m.Status != "partial" {
				t.Fatal("unverified legacy privacy was certified")
			}
			if !reflect.DeepEqual(before, archiveHashes(t, source)) {
				t.Fatal("source archive changed")
			}
		})
	}
}

func TestPackagedSignalPrivacyCLI(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_TEST_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_TEST_BINARY to the native release executable")
	}
	verifySignalPrivacyCLI(t, binary, false)
}

func TestOlderPackagedSignalPrivacyRebuild(t *testing.T) {
	binary := os.Getenv("PIECES_EXPORT_LEGACY_SIGNAL_PRIVACY_BINARY")
	if binary == "" {
		t.Skip("set PIECES_EXPORT_LEGACY_SIGNAL_PRIVACY_BINARY to a pre-fix executable, such as 0.8.9-dev")
	}
	verifySignalPrivacyCLI(t, binary, true)
}

func verifySignalPrivacyCLI(t *testing.T, binary string, legacy bool) {
	t.Helper()
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	signal, body, event := signalRecord("signal"), record("body", ""), record("event", "")
	signal["name"], signal["annotations"], signal["workstream_events"] = "Private signal label sentinel", refs("body"), refs("event")
	body["type"], body["text"] = "SIGNAL_DESCRIPTION", "Private signal narrative sentinel"
	event["url"] = "https://bank.example/private"
	if legacy {
		signal["workstream_events"] = refs()
		event["signals"] = refs("signal")
	}
	f := &fakeOS{data: map[string][]map[string]any{"SIGNALS": {signal}, "ANNOTATIONS": {body}, "WORKSTREAM_EVENTS": {event}}}
	srv := f.server(t)
	defer srv.Close()
	policy := DefaultPolicy()
	policy.Deny = []DomainRule{{"bank.example", true}}
	root := t.TempDir()
	policyPath := filepath.Join(root, "policy.json")
	if err := writeJSON(policyPath, policy); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "source")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := []string{"export", "--base-url", srv.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--materials", "SIGNALS,ANNOTATIONS,WORKSTREAM_EVENTS", "--policy", policyPath, "--output", out, "--metadata", "off"}
	if b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("packaged fixture export: %v\n%s", err, b)
	}
	srv.Close()
	m, err := InspectArchive(out)
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		if m.SignalPrivacyVersion >= currentSignalPrivacyVersion {
			t.Fatal("fixture executable already has signal dependency filtering")
		}
		kept := 0
		for _, c := range m.Coverage {
			if c.Material != "WORKSTREAM_EVENTS" {
				kept += c.Included
			}
		}
		if kept != 2 {
			t.Fatal("older writer did not reproduce the removed-dependency case")
		}
		before := archiveHashes(t, out)
		m, err = Rebuild(ctx, RebuildOptions{Source: out, Options: Options{Output: out + "-rebuilt", Scanner: scanner(t, policy)}})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, archiveHashes(t, out)) {
			t.Fatal("legacy source changed")
		}
		out += "-rebuilt"
		if m.Status != "partial" {
			t.Fatal("legacy privacy gap did not keep partial status")
		}
	}
	for _, c := range m.Coverage {
		if c.Included != 0 {
			t.Fatal("signal/description survived excluded event provenance")
		}
	}
	assertNoSignalPrivateText(t, out)
	t.Log("compiled export and offline reconciliation prevent signal descriptions from surviving blocked evidence")
}
