package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// This is an opt-in acceptance reader, not another exporter. It never opens
// OS, SDK caches, or an unfinished archive. Only fixed labels and counts leave
// the reader; errors deliberately exclude source names, IDs, text and paths.
type finalArchiveCounts struct {
	ArchiveStatus              string         `json:"archive_status"`
	Included                   int            `json:"included_records"`
	States                     map[string]int `json:"record_states"`
	Summaries                  int            `json:"summaries"`
	SummariesWithBody          int            `json:"summaries_with_nonempty_summary_body"`
	SummariesWithAnyAnnotation int            `json:"summaries_with_nonempty_annotation"`
	RenderedAnnotationBodies   int            `json:"verified_rendered_annotation_bodies"`
	LinkedProfileAnnotations   int            `json:"verified_linked_profile_annotations"`
	SummaryBodiesFromCache     int            `json:"summaries_with_historical_body_links"`
	Persons                    int            `json:"included_persons"`
	PersonsWithProfile         int            `json:"persons_with_profile_history"`
	PersonHistoryLinks         int            `json:"person_profile_history_links"`
	Edges                      int            `json:"graph_edges"`
	HistoricalEdges            int            `json:"historical_graph_edges"`
	VerifiedProfileDocuments   int            `json:"verified_profile_history_documents"`
	CurrentSummaryAnnotations  int            `json:"summaries_with_current_annotation_evidence"`
	CurrentPersonAnnotations   int            `json:"persons_with_current_annotation_evidence"`
	CurrentPersonSummaries     int            `json:"persons_with_current_summary_evidence"`
	SummaryPipelineLinks       int            `json:"summary_pipeline_memberships"`
	PipelinesWithSummaries     int            `json:"pipelines_with_summary_memberships"`
	VerifiedAssociationEdges   int            `json:"verified_canonical_association_edges"`
}

func inspectFinalArchive(ctx context.Context, source string) (finalArchiveCounts, error) {
	report := finalArchiveCounts{States: map[string]int{}}
	manifest, err := InspectArchive(source)
	if err != nil || manifest.FormatVersion != 5 && manifest.FormatVersion != 6 || manifest.ArchiveState == nil || manifest.ArchiveState.Version < 1 || manifest.ArchiveState.Version > 3 {
		return report, errConfig("acceptance requires a finalized format-5/6 archive with reconstruction evidence")
	}
	report.ArchiveStatus = manifest.Status
	root, err := os.OpenRoot(source)
	if err != nil {
		return report, errConfig("archive cannot be opened")
	}
	defer root.Close()
	coverage := map[string]*Coverage{}
	wantIncluded := 0
	for _, c := range manifest.Coverage {
		if _, ok := materialByType(c.Material); !ok || coverage[c.Material] != nil {
			return report, errConfig("unsupported or duplicate material coverage")
		}
		coverage[c.Material] = c
		wantIncluded += c.Included
	}
	links, err := archiveLinkMap(ctx, root, wantIncluded, manifest.FormatVersion >= 6)
	if err != nil {
		return report, errConfig("archive link map does not reconcile")
	}
	f, err := archiveOpen(root, "link-map.json")
	if err != nil {
		return report, errConfig("archive link map cannot be read")
	}
	digest, err := readerDigest(ctx, f)
	f.Close()
	if err != nil || digest != manifest.ArchiveState.LinkMapSHA256 {
		return report, errConfig("archive link map checksum differs")
	}
	byRef, byPath, byKey := map[string]*Meta{}, map[string]*Meta{}, map[string]*Meta{}
	seen := map[string]bool{}
	states := map[string]map[string]int{}
	annotationNonempty := map[string]bool{}
	associationPairs := map[string][2]string{}
	err = archiveLines(ctx, root, archiveStateFile, manifest.ArchiveState.StateSHA256, func(row archiveRecord) error {
		if !validDigest(row.Ref) || seen[row.Ref] || coverage[row.Material] == nil {
			return errConfig("duplicate or unsupported reconstruction record")
		}
		seen[row.Ref] = true
		switch row.State {
		case "included", "omitted", "excluded", "withheld", "missing":
		default:
			return errConfig("unknown reconstruction decision")
		}
		if states[row.Material] == nil {
			states[row.Material] = map[string]int{}
		}
		states[row.Material][row.State]++
		report.States[row.State]++
		if row.State != "included" {
			if links[row.Ref] != "" {
				return errConfig("non-included record has a navigation path")
			}
			return nil
		}
		material, _ := materialByType(row.Material)
		prefix := "data/"
		if manifest.Mode == "preserve" {
			prefix = "raw/"
		}
		dataPath := prefix + material.Folder + "/" + row.Ref + ".json"
		var body []byte
		if row.DataLength > 0 {
			dataPath = row.DataPath
			if manifest.FormatVersion != 6 || manifest.ArchiveState.Version != 3 || !strings.HasPrefix(dataPath, prefix+material.Folder+"/") || links[row.Ref] != groupedDocumentPath(dataPath) {
				return errConfig("invalid grouped record navigation")
			}
			f, e := openCanonicalSection(ctx, source, &Meta{DataPath: dataPath, DataOffset: row.DataOffset, DataLength: row.DataLength})
			if e != nil {
				return e
			}
			body, err = io.ReadAll(f)
			f.Close()
		} else {
			body, err = archiveRead(root, dataPath, 128<<20)
		}
		if err != nil {
			return errConfig("included canonical record is missing or unreadable")
		}
		sum := sha256.Sum256(body)
		var v map[string]any
		if hex.EncodeToString(sum[:]) != row.DataSHA256 || decodeArchiveJSON(body, &v) != nil || fieldString(v, "id") == "" || opaque(row.Material, fieldString(v, "id")) != row.Ref || links[row.Ref] == "" {
			return errConfig("included record identity, checksum or navigation differs")
		}
		md, err := archiveOpen(root, links[row.Ref])
		if err != nil {
			return errConfig("included Markdown document is missing or unsafe")
		}
		md.Close()
		m := &Meta{Type: row.Material, ID: fieldString(v, "id"), Path: links[row.Ref], DataPath: dataPath, State: "included", AnnotationType: fieldString(v, "type")}
		if err := validateJunctionFields(m.Type, row.JunctionFields); err != nil || len(row.JunctionFields) > 0 && manifest.ArchiveState.Version < 2 {
			return errConfig("invalid archived current-relationship evidence")
		}
		m.JunctionFields = row.JunctionFields
		m.DataOffset, m.DataLength, m.ArchiveDataSHA256 = row.DataOffset, row.DataLength, row.DataSHA256
		m.Key = m.Type + "\x00" + m.ID
		if family, ok := associationFamilyByType(m.Type); ok {
			associationPairs[row.Ref] = [2]string{family.leftType + "\x00" + fieldString(v, family.leftField), family.rightType + "\x00" + fieldString(v, family.rightField)}
		}
		byRef[row.Ref], byPath[m.Path], byKey[m.Key] = m, m, m
		report.Included++
		if m.Type == "ANNOTATIONS" {
			annotationNonempty[m.Key] = strings.TrimSpace(fieldString(v, "text")) != ""
		}
		return nil
	})
	if err != nil {
		return report, errConfig("canonical records or reconstruction evidence failed acceptance")
	}
	for typ, c := range coverage {
		s := states[typ]
		if s["included"] != c.Included || s["excluded"] != c.Excluded || s["withheld"] != c.Withheld || s["omitted"] != c.Omitted {
			return report, errConfig("manifest decisions do not reconcile with archive records")
		}
	}
	if len(byRef) != wantIncluded {
		return report, errConfig("included record count does not reconcile")
	}
	for typ, c := range coverage {
		material, _ := materialByType(typ)
		prefix := "data/"
		if manifest.Mode == "preserve" {
			prefix = "raw/"
		}
		directory := prefix + material.Folder
		if _, err := root.Stat(directory); os.IsNotExist(err) && c.Included == 0 {
			continue
		}
		count := 0
		checked := map[string]bool{}
		err := archiveCollection(ctx, root, material, manifest.Mode, manifest.FormatVersion, func(ref string, b []byte, file string, offset int64) error {
			m := byRef[ref]
			sum := sha256.Sum256(b)
			if m == nil || checked[ref] || m.Type != typ || m.DataPath != file || m.DataOffset != offset || hex.EncodeToString(sum[:]) != m.ArchiveDataSHA256 || m.DataLength > 0 && m.DataLength != int64(len(b)) {
				return errConfig("canonical directory contains an unaccounted file")
			}
			checked[ref] = true
			count++
			return ctx.Err()
		})
		if err != nil || count != c.Included {
			return report, errConfig("canonical directory entries differ from declared included records")
		}
	}
	cacheBodies := map[string]bool{}
	pipelineMembers, pipelines := map[Edge]bool{}, map[string]bool{}
	provedPairs := map[[2]string]bool{}
	err = archiveLines(ctx, root, "relationships.jsonl", manifest.ArchiveState.GraphSHA256, func(edge PublicEdge) error {
		a, b := byPath[edge.Source], byPath[edge.Target]
		if manifest.FormatVersion >= 6 {
			a, b = byRef[edge.SourceRef], byRef[edge.TargetRef]
			if a == nil || b == nil || a.Path != edge.Source || b.Path != edge.Target {
				return errConfig("graph identity does not match document path")
			}
		}
		if a == nil || b == nil || edge.Relation != "embedded_markdown" && referenceTypes[edge.Relation] == "" {
			return errConfig("graph has an unresolved endpoint or unsupported relation")
		}
		if edge.Relation != "embedded_markdown" && referenceTypes[edge.Relation] != b.Type {
			return errConfig("graph relation points to the wrong material type")
		}
		if edge.Provenance == "association_record" {
			pair, ok := associationPairs[edge.AssociationRef]
			if !ok || !(a.Key == pair[0] && b.Key == pair[1] || a.Key == pair[1] && b.Key == pair[0]) {
				return errConfig("graph edge differs from its canonical association endpoints")
			}
			report.VerifiedAssociationEdges++
			provedPairs[[2]string{a.Key, b.Key}] = true
		} else if edge.AssociationRef != "" {
			return errConfig("graph association reference lacks matching provenance")
		}
		a.Edges = append(a.Edges, Edge{a.Key, b.Key, edge.Relation})
		if a.Type == "WORKSTREAM_SUMMARIES" && b.Type == "PIPELINES" && edge.Relation == "pipelines" {
			pipelineMembers[Edge{a.Key, b.Key, "pipelines"}], pipelines[b.Key] = true, true
		} else if a.Type == "PIPELINES" && b.Type == "WORKSTREAM_SUMMARIES" && edge.Relation == "summaries" {
			pipelineMembers[Edge{b.Key, a.Key, "pipelines"}], pipelines[a.Key] = true, true
		}
		report.Edges++
		historical := edge.Provenance == "historical_client_cache" || edge.Provenance == "historical_client_cache_derived_inverse"
		if historical {
			report.HistoricalEdges++
			if a.Type == "WORKSTREAM_SUMMARIES" && b.Type == "ANNOTATIONS" && annotationNonempty[b.Key] && (b.AnnotationType == "SUMMARY" || b.AnnotationType == "DEEP_STUDY_HIERARCHICAL_SUMMARY") {
				cacheBodies[a.Key] = true
			}
		}
		return nil
	})
	if err != nil {
		return report, errConfig("graph evidence failed acceptance")
	}
	for _, pair := range associationPairs {
		if byKey[pair[0]] != nil && byKey[pair[1]] != nil && (!provedPairs[pair] || !provedPairs[[2]string{pair[1], pair[0]}]) {
			return report, errConfig("graph omits an included canonical association binding")
		}
	}
	report.SummaryPipelineLinks, report.PipelinesWithSummaries = len(pipelineMembers), len(pipelines)
	verifiedProfiles := map[string]bool{}
	for _, m := range byKey {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if m.Type != "WORKSTREAM_SUMMARIES" && m.Type != "PERSONS" {
			continue
		}
		var document []byte
		var links map[string]bool
		if m.Type == "WORKSTREAM_SUMMARIES" {
			report.Summaries++
			if m.JunctionFields["annotations"] {
				report.CurrentSummaryAnnotations++
			}
			document, err = archiveRead(root, m.Path, 128<<20)
			if err != nil {
				return report, errConfig("summary Markdown cannot be read")
			}
		} else {
			report.Persons++
			if m.JunctionFields["annotations"] {
				report.CurrentPersonAnnotations++
			}
			if m.JunctionFields["summaries"] {
				report.CurrentPersonSummaries++
			}
		}
		body, anyAnnotation, profile := false, false, false
		for _, e := range m.Edges {
			a := byKey[e.Target]
			if e.Relation != "annotations" || a == nil || !annotationNonempty[a.Key] {
				continue
			}
			if m.Type == "PERSONS" {
				if a.AnnotationType == "HIERARCHICAL_PROFILE_SUMMARY" || a.AnnotationType == "PROFILE_DESCRIPTION" {
					profile = true
					report.PersonHistoryLinks++
					if !verifiedProfiles[a.Key] {
						canonical, err := archiveRead(root, a.DataPath, 128<<20)
						var v map[string]any
						if err != nil || decodeArchiveJSON(canonical, &v) != nil {
							return report, errConfig("profile history canonical annotation cannot be read")
						}
						expected := rewriteMarkdown(fieldString(v, "text"), a.Path, byKey)
						doc, err := archiveRead(root, a.Path, 128<<20)
						if err != nil || strings.TrimSpace(expected) != "" && !strings.Contains(string(doc), expected) {
							return report, errConfig("profile history Markdown omits or truncates its canonical body")
						}
						verifiedProfiles[a.Key] = true
						report.VerifiedProfileDocuments++
					}
				}
				continue
			}
			b, err := archiveRead(root, a.DataPath, 128<<20)
			var v map[string]any
			if err != nil || decodeArchiveJSON(b, &v) != nil {
				return report, errConfig("attached canonical annotation cannot be read")
			}
			expected := rewriteMarkdown(fieldString(v, "text"), m.Path, byKey)
			if a.AnnotationType == "HIERARCHICAL_PROFILE_SUMMARY" || a.AnnotationType == "PROFILE_DESCRIPTION" {
				// Support earlier inline archives and current linked presentation,
				// but always require the complete text in the destination document.
				profileDoc, err := archiveRead(root, a.Path, 128<<20)
				profileText := rewriteMarkdown(fieldString(v, "text"), a.Path, byKey)
				if err != nil || !strings.Contains(string(profileDoc), profileText) {
					return report, errConfig("linked profile document omits or truncates its canonical body")
				}
				if !strings.Contains(string(document), expected) {
					if links == nil {
						links = acceptanceMarkdownLinks(document)
					}
					if !links[relative(m.Path, a.Path)] {
						return report, errConfig("summary omits its attached profile reference")
					}
					report.LinkedProfileAnnotations++
					anyAnnotation = true
					continue
				}
			}
			if strings.TrimSpace(expected) != "" && !strings.Contains(string(document), expected) {
				return report, errConfig("summary Markdown omits or truncates an attached annotation body")
			}
			report.RenderedAnnotationBodies++
			anyAnnotation = true
			body = body || a.AnnotationType == "SUMMARY" || a.AnnotationType == "DEEP_STUDY_HIERARCHICAL_SUMMARY"
		}
		if body {
			report.SummariesWithBody++
		}
		if anyAnnotation {
			report.SummariesWithAnyAnnotation++
		}
		if profile {
			report.PersonsWithProfile++
		}
	}
	report.SummaryBodiesFromCache = len(cacheBodies)
	// Final link traversal checks every Markdown file, including secondary
	// indexes/profile pages outside the canonical record link map.
	r := &run{ctx: ctx, stage: source}
	if err := r.validateMarkdownLinks(); err != nil {
		return report, errConfig("final Markdown link traversal failed")
	}
	return report, nil
}

func acceptanceMarkdownLinks(document []byte) map[string]bool {
	links := map[string]bool{}
	root := goldmark.DefaultParser().Parse(text.NewReader(document))
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if link, ok := n.(*ast.Link); ok && entering {
			links[string(link.Destination)] = true
		}
		return ast.WalkContinue, nil
	})
	return links
}

func TestLiveFinalArchiveAcceptance(t *testing.T) {
	source := os.Getenv("PIECES_EXPORT_LIVE_FINAL_ARCHIVE")
	if source == "" {
		t.Skip("set PIECES_EXPORT_LIVE_FINAL_ARCHIVE to a finalized archive for read-only acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	report, err := inspectFinalArchive(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(report)
	t.Log(string(b))
	t.Log("Archive consistency and rendered-body checks passed; these do not certify missing source relationships, unseen history or source text completeness.")
}

func TestFinalArchiveAcceptanceReconcilesAssociationProofs(t *testing.T) {
	srv := junctionServer(t, junctionFixture(), nil)
	client, _ := NewClient(srv.URL, time.Second, 8<<20)
	o := junctionExportOptions(t)
	m, err := Export(context.Background(), client, o)
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	report, err := inspectFinalArchive(context.Background(), o.Output)
	if err != nil || report.VerifiedAssociationEdges != 8 {
		t.Fatal("fixture canonical associations did not reconcile", err)
	}
	graphPath := filepath.Join(o.Output, "relationships.jsonl")
	original, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"wrong-proof", "missing-edge"} {
		t.Run(mutation, func(t *testing.T) {
			var changed strings.Builder
			mutated := false
			for _, line := range strings.Split(strings.TrimSpace(string(original)), "\n") {
				var edge PublicEdge
				if json.Unmarshal([]byte(line), &edge) != nil {
					t.Fatal("invalid fixture graph")
				}
				if edge.SourceRef == opaque("WORKSTREAM_SUMMARIES", "summary") && edge.TargetRef == opaque("ANNOTATIONS", "body") && edge.Provenance == "association_record" {
					mutated = true
					if mutation == "missing-edge" {
						continue
					}
					edge.AssociationRef = opaque("PERSON_TO_ANNOTATION_ASSOCIATIONS", "association-3")
				}
				row, _ := json.Marshal(edge)
				changed.Write(row)
				changed.WriteByte('\n')
			}
			if !mutated {
				t.Fatal("required edge absent before mutation")
			}
			if err := os.WriteFile(graphPath, []byte(changed.String()), 0600); err != nil {
				t.Fatal(err)
			}
			m.ArchiveState.GraphSHA256, err = fileDigest(context.Background(), graphPath)
			if err != nil {
				t.Fatal(err)
			}
			manifest, _ := json.Marshal(m)
			if err := os.WriteFile(filepath.Join(o.Output, "manifest.json"), manifest, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := inspectFinalArchive(context.Background(), o.Output); err == nil {
				t.Fatal("consistent checksums hid incorrect canonical association evidence")
			}
		})
	}
}

func TestFinalArchiveAcceptanceDetectsDamage(t *testing.T) {
	f := summaryScopeFixture()
	srv := f.server(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, time.Second, 8<<20)
	sel, _ := SelectScope("summaries", "")
	out := filepath.Join(t.TempDir(), "archive")
	_, err := Export(context.Background(), c, Options{Scope: sel.Name, ReferenceOnly: sel.ReferenceOnly, Materials: sel.Materials, Output: out, Mode: "filtered", Timezone: "UTC", BatchSize: 50, WindowIDs: 5000, Scanner: scanner(t, DefaultPolicy()), Format: "markdown"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := inspectFinalArchive(context.Background(), out)
	if err != nil || report.Summaries != 1 || report.SummariesWithBody != 1 || report.PersonsWithProfile != 1 || report.RenderedAnnotationBodies != 1 || report.VerifiedProfileDocuments != 1 {
		t.Fatal("intact fixture did not reconcile", err, report)
	}
	var links map[string]string
	b, _ := os.ReadFile(filepath.Join(out, "link-map.json"))
	json.Unmarshal(b, &links)
	mdPath := filepath.Join(out, links[opaque("WORKSTREAM_SUMMARIES", "summary")])
	for _, mutation := range []struct {
		name, path string
		data       []byte
	}{
		{"truncated-body", mdPath, []byte("# Body removed\n")},
		{"truncated-profile", filepath.Join(out, links[opaque("ANNOTATIONS", "profile")]), []byte("# Profile body removed\n")},
		{"broken-index-link", filepath.Join(out, "index.md"), []byte("[missing](missing.md)\n")},
		{"changed-canonical", filepath.Join(out, "data/annotations/"+opaque("ANNOTATIONS", "body")+".json"), []byte("{}\n")},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			before, err := os.ReadFile(mutation.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(mutation.path, mutation.data, 0600); err != nil {
				t.Fatal(err)
			}
			defer os.WriteFile(mutation.path, before, 0600)
			if _, err := inspectFinalArchive(context.Background(), out); err == nil {
				t.Fatal("damaged fixture passed acceptance")
			}
		})
	}
	if _, err := inspectFinalArchive(context.Background(), out+".partial"); err == nil {
		t.Fatal("unfinished archive accepted")
	}
	extra := filepath.Join(out, "data/annotations/"+opaque("ANNOTATIONS", "unaccounted")+".json")
	if err := os.WriteFile(extra, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectFinalArchive(context.Background(), out); err == nil {
		t.Fatal("undeclared canonical file accepted")
	}
}
