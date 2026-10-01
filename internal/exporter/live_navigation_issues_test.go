package exporter

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

// Reconcile newly visible unresolved-link issues against the authenticated
// source capture and prior/new public decisions. Never changes an archive.
func TestDiagnosticRestoredNavigationIssues(t *testing.T) {
	work, keys := os.Getenv("PIECES_EXPORT_DIAGNOSTIC_WORK"), os.Getenv("PIECES_EXPORT_DIAGNOSTIC_KEYS")
	prior, current := os.Getenv("PIECES_EXPORT_DIAGNOSTIC_PRIOR_ARCHIVE"), os.Getenv("PIECES_EXPORT_DIAGNOSTIC_ARCHIVE")
	reportPath := os.Getenv("PIECES_EXPORT_DIAGNOSTIC_ISSUE_REPORT")
	if work == "" || keys == "" || prior == "" || current == "" || reportPath == "" {
		t.Skip("explicit private capture, archives and new report path required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	session, err := OpenRecovery(ctx, RecoveryOptions{Directory: work, KeyDirectory: keys})
	if err != nil {
		t.Fatal("cannot authenticate the completed source capture")
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error("diagnostic recovery ownership did not close cleanly")
		}
	}()
	if !session.info.CanResume {
		t.Fatal("source capture is incomplete")
	}
	read := func(directory string) (Manifest, map[string]string) {
		t.Helper()
		m, err := InspectArchive(directory)
		if err != nil || m.ArchiveState == nil {
			t.Fatal("invalid finalized archive")
		}
		root, err := os.OpenRoot(directory)
		if err != nil {
			t.Fatal("archive unavailable")
		}
		defer root.Close()
		states := map[string]string{}
		if err := archiveLines(ctx, root, archiveStateFile, m.ArchiveState.StateSHA256, func(row archiveRecord) error {
			if states[row.Ref] != "" {
				return errConfig("duplicate record decision")
			}
			states[row.Ref] = row.State
			return nil
		}); err != nil {
			t.Fatal("archive decision evidence does not verify")
		}
		return m, states
	}
	old, oldStates := read(prior)
	now, newStates := read(current)
	if old.PolicyHash != now.PolicyHash || now.PolicyHash != session.r.opts.Scanner.Hash || now.Mode != "filtered" || now.FormatVersion != 6 {
		t.Fatal("diagnostic requires the unchanged captured policy and format-6 filtered output")
	}
	publicRoot, err := os.OpenRoot(current)
	if err != nil {
		t.Fatal("current archive unavailable")
	}
	defer publicRoot.Close()
	scanner := session.r.opts.Scanner.forkForAudit()
	verifiedBodies := 0
	want, got := map[Issue]int{}, map[Issue]int{}
	for _, issue := range old.Issues {
		want[issue]++
	}
	for _, issue := range now.Issues {
		got[issue]++
	}
	delta := []Issue{}
	for _, m := range session.r.sortedMeta() {
		ref := opaque(m.Type, m.ID)
		if m.Type != "ANNOTATIONS" || oldStates[ref] != "withheld" || newStates[ref] != "included" {
			continue
		}
		payload, err := session.store.Get(ctx, captureRef(capturePart{Kind: "record", Name: m.Key}))
		if err != nil {
			t.Fatal("restored annotation is missing from authenticated capture")
		}
		var part capturePart
		err = decodeCapture(payload, &part)
		clear(payload)
		if err != nil || part.Kind != "record" || part.Name != m.Key || part.Record == nil || !reflect.DeepEqual(&part.Record.Meta, m) {
			t.Fatal("captured annotation identity or evidence differs")
		}
		var captured, published map[string]any
		if err := decodeArchiveJSON(part.Record.Data, &captured); err != nil {
			t.Fatal("captured annotation is invalid")
		}
		clean, stats, err := scanner.Sanitize(ctx, captured)
		if err != nil || stats.Denied {
			t.Fatal("restored source annotation does not pass the captured policy")
		}
		data, err := archiveRead(publicRoot, "data/annotations/"+ref+".json", 128<<20)
		if err != nil || decodeArchiveJSON(data, &published) != nil || !reflect.DeepEqual(clean["text"], published["text"]) {
			t.Fatal("restored annotation text differs from its authenticated source after privacy filtering")
		}
		verifiedBodies++
		for _, edge := range m.Edges {
			target := session.r.meta[edge.Target]
			if edge.Relation != "embedded_markdown" || target == nil || target.Type != "PERSONS" || target.State != "missing" {
				continue
			}
			issue := Issue{Material: m.Type, Key: ref, Code: "unresolved_reference"}
			want[issue]++
			delta = append(delta, issue)
		}
	}
	if len(delta) == 0 || !reflect.DeepEqual(want, got) {
		t.Fatal("issue changes are not exactly the restored annotations' captured missing-person links")
	}
	b, err := json.MarshalIndent(struct {
		PreservedIssues         int     `json:"preserved_issues"`
		Added                   []Issue `json:"added_issues"`
		Verified                bool    `json:"verified_against_authenticated_capture"`
		RestoredAnnotationTexts int     `json:"verified_restored_annotation_texts"`
		PriorStateHash          string  `json:"prior_state_sha256"`
		CurrentStateHash        string  `json:"current_state_sha256"`
	}{len(old.Issues), delta, true, verifiedBodies, old.ArchiveState.StateSHA256, now.ArchiveState.StateSHA256}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(reportPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("cannot create new private issue report")
	}
	_, writeErr := f.Write(append(b, '\n'))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("private issue report could not be written")
	}
	t.Logf("preserved_source_issues=%d added_unresolved_navigation_issues=%d verified_restored_annotation_texts=%d; source capture and current filtered output reconcile", len(old.Issues), len(delta), verifiedBodies)
}
