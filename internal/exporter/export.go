package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Options struct {
	Scope                                   string
	ReferenceOnly                           map[string]bool
	SDKCaches                               []string
	Output, Mode, Timezone, Version         string
	Format, Naming, Relationships, Metadata string
	PDFFont                                 string
	PeopleMode                              string
	MinPersonConnections                    int
	RelatedOrder                            string
	RelatedLimit                            int
	RelatedSince                            time.Time
	Materials                               []Material
	BatchSize, WindowIDs                    int
	Scanner                                 *Scanner
	Progress                                io.Writer
}
type Coverage struct {
	InventoryMode string `json:"inventory_mode"`
	Omitted       int    `json:"intentionally_omitted"`
	Material      string `json:"material"`
	InitialCount  int    `json:"initial_count"`
	Inventoried   int    `json:"inventoried"`
	Fetched       int    `json:"fetched"`
	Included      int    `json:"included"`
	Redacted      int    `json:"redacted"`
	Excluded      int    `json:"excluded"`
	Withheld      int    `json:"withheld"`
	FinalCount    int    `json:"final_count"`
}
type Issue struct {
	Material string `json:"material"`
	Key      string `json:"record_ref,omitempty"`
	Code     string `json:"code"`
}
type Manifest struct {
	Naming                  string                   `json:"naming"`
	Relationships           string                   `json:"relationships"`
	Metadata                string                   `json:"metadata"`
	ArchiveState            *ArchiveState            `json:"archive_state,omitempty"`
	Rebuild                 *RebuildInfo             `json:"rebuild,omitempty"`
	Scope                   Scope                    `json:"scope"`
	SDKCache                CacheCoverage            `json:"sdk_cache"`
	RelationshipCoverage    []RelationshipCoverage   `json:"relationship_coverage"`
	People                  PeopleStats              `json:"people"`
	Performance             PerformanceStats         `json:"performance"`
	SummaryHierarchy        SummaryHierarchyCoverage `json:"summary_hierarchy"`
	RelatedOrder            string                   `json:"related_order"`
	RelatedLimit            int                      `json:"related_limit"`
	RelatedSince            string                   `json:"related_since,omitempty"`
	Format                  string                   `json:"format"`
	Warnings                []string                 `json:"warnings,omitempty"`
	FormatVersion           int                      `json:"format_version"`
	ToolVersion             string                   `json:"tool_version"`
	Mode                    string                   `json:"mode"`
	Status                  string                   `json:"status"`
	Started, Finished       time.Time
	OSVersion               string            `json:"os_version"`
	Timezone                string            `json:"timezone"`
	PolicyHash              string            `json:"policy_hash,omitempty"`
	CategoryHashes          map[string]string `json:"category_hashes,omitempty"`
	Coverage                []*Coverage       `json:"coverage"`
	Issues                  []Issue           `json:"issues"`
	Limitations             []string          `json:"limitations"`
	WithheldRepresentations int               `json:"withheld_representations"`
}
type Edge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
}
type Meta struct {
	ArchivePlaceholder                                             bool
	ArchiveDataSHA256                                              string
	SupplementableFields                                           map[string]bool
	ProjectionStates                                               map[string]string
	RelationshipProjectionUnknown                                  bool
	PersonProjection                                               bool
	PersonEvidence                                                 *PersonFacts
	Key, ID, Type, Folder, Path, DataPath, Title, Created, Updated string
	AnnotationType                                                 string
	SummaryKind                                                    string
	SummaryDescriptor                                              string
	Edges                                                          []Edge
	State                                                          string
	Redactions                                                     int
}
type run struct {
	priorDecisions   []archiveRecord
	rebuilding       bool
	ctx              context.Context
	client           *Client
	opts             Options
	stage            string
	manifest         Manifest
	meta             map[string]*Meta
	coverage         map[string]*Coverage
	inventory        map[string][]string
	people           map[string]*PersonFacts
	userPersonIDs    map[string]bool
	derivedEdges     map[Edge]bool
	cachedEdges      map[Edge]CacheEvidence
	progress         *Progress
	documentMetadata map[string]*DocumentMetadata
}

func opaque(t, id string) string {
	h := sha256.Sum256([]byte(t + "\x00" + id))
	return hex.EncodeToString(h[:])
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(b, '\n'))
}
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(path)
	}
	return closeErr
}
func readRecord(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.UseNumber()
	var v map[string]any
	err = d.Decode(&v)
	return v, err
}
func fieldString(v map[string]any, key string) string { s, _ := v[key].(string); return s }
func timestamp(v map[string]any, key string) string {
	m, _ := v[key].(map[string]any)
	return fieldString(m, "value")
}
func title(v map[string]any, m Material) string {
	for _, k := range []string{"name", "title", "windowTitle", "readable", "text", "description", "url"} {
		s := fieldString(v, k)
		if s != "" {
			s = strings.Split(s, "\n")[0]
			r := []rune(s)
			if len(r) > 120 {
				s = string(r[:120]) + "…"
			}
			return s
		}
	}
	if t, ok := v["type"].(map[string]any); ok {
		for _, k := range []string{"basic", "platform"} {
			if p, ok := t[k].(map[string]any); ok && fieldString(p, "name") != "" {
				return fieldString(p, "name")
			}
		}
	}
	return strings.ReplaceAll(m.Type, "_", " ")
}

func Export(ctx context.Context, client *Client, o Options) (Manifest, error) {
	if err := validateScope(o); err != nil {
		return Manifest{}, err
	}
	if err := ValidateSDKCaches(ctx, o.SDKCaches); err != nil {
		return Manifest{}, err
	}
	if len(o.SDKCaches) > 0 {
		selected := false
		for _, m := range o.Materials {
			selected = selected || m.Type == "WORKSTREAM_SUMMARIES"
		}
		if !selected {
			return Manifest{}, errConfig("SDK cache recovery requires WORKSTREAM_SUMMARIES in selected materials")
		}
	}
	if o.PeopleMode == "" {
		o.PeopleMode = "all"
	}
	if o.MinPersonConnections == 0 {
		o.MinPersonConnections = 10
	}
	if (o.PeopleMode != "all" && o.PeopleMode != "profiles" && o.PeopleMode != "connected") || o.MinPersonConnections < 1 {
		return Manifest{}, errConfig("people must be all, profiles, or connected; min-person-connections must be positive")
	}
	if o.PeopleMode != "all" {
		has := map[string]bool{}
		for _, m := range o.Materials {
			has[m.Type] = true
		}
		if !has["PERSONS"] || !has["ANNOTATIONS"] {
			return Manifest{}, errConfig("focused people selection requires PERSONS and ANNOTATIONS in selected materials")
		}
	}

	if o.RelatedOrder == "" {
		o.RelatedOrder = "relevance"
	}
	if o.RelatedLimit == 0 {
		o.RelatedLimit = 50
	}
	if (o.RelatedOrder != "relevance" && o.RelatedOrder != "recent") || o.RelatedLimit < 1 || o.RelatedLimit > 500 {
		return Manifest{}, errConfig("related-order must be relevance or recent; related-limit must be 1–500")
	}
	if o.Format == "" {
		o.Format = "markdown"
	}
	if o.Naming == "" {
		o.Naming = "readable"
	}
	if o.Relationships == "" {
		o.Relationships = "both"
	}
	if o.Metadata == "" {
		o.Metadata = "off"
	}
	if (o.Format != "markdown" && o.Format != "pdf" && o.Format != "both") || (o.Naming != "readable" && o.Naming != "opaque") || (o.Metadata != "off" && o.Metadata != "auto") || (o.Relationships != "both" && o.Relationships != "inline" && o.Relationships != "sidecar") {
		return Manifest{}, errConfig("invalid format, naming, metadata, or relationships option")
	}
	if o.PDFFont != "" {
		if err := ValidatePDFFont(o.PDFFont); err != nil {
			return Manifest{}, err
		}
	}
	if o.Mode != "filtered" && o.Mode != "preserve" {
		return Manifest{}, errConfig("mode must be filtered or preserve")
	}
	if o.Scanner == nil {
		return Manifest{}, errConfig("missing policy scanner")
	}
	if o.BatchSize < 1 || o.BatchSize > 50 || o.WindowIDs < 1 {
		return Manifest{}, errConfig("batch size must be 1–50 and window-ids must be positive")
	}
	if _, err := time.LoadLocation(o.Timezone); err != nil {
		return Manifest{}, errConfig("invalid IANA timezone")
	}
	abs, err := filepath.Abs(o.Output)
	if err != nil {
		return Manifest{}, err
	}
	if _, err = os.Lstat(abs); !os.IsNotExist(err) {
		return Manifest{}, errConfig("output already exists or cannot be inspected; choose a new directory")
	}
	stage := abs + ".partial"
	if err = os.MkdirAll(filepath.Dir(stage), 0700); err != nil {
		return Manifest{}, err
	}
	if err = os.Mkdir(stage, 0700); err != nil {
		return Manifest{}, errConfig("partial directory already exists or cannot be created; choose a new output path")
	}
	r := &run{ctx: ctx, client: client, opts: o, stage: stage, meta: map[string]*Meta{}, coverage: map[string]*Coverage{}, inventory: map[string][]string{}, userPersonIDs: map[string]bool{}}
	r.progress = startProgress(o.Progress, client)
	defer r.progress.Close()
	if r.progress != nil {
		r.opts.Progress = r.progress.out
	}

	r.manifest = Manifest{Naming: o.Naming, Relationships: o.Relationships, Metadata: o.Metadata, Scope: scopeFor(o), RelatedOrder: o.RelatedOrder, RelatedLimit: o.RelatedLimit, Format: o.Format, FormatVersion: 5, ToolVersion: o.Version, Mode: o.Mode, Status: "running", Started: time.Now().UTC(), Timezone: o.Timezone, Coverage: []*Coverage{}, Issues: []Issue{}, Limitations: []string{
		"Retained local HTTP data only; not an atomic database backup or deleted history.",
		"Association record metadata, supplementary analysis/settings views, and fingerprint audio downloads are not implemented in this version.",
		"Server projections can omit vectors and internal data. Binary attachments are not extracted; preserve mode retains exposed byte/encoded fields in JSON.",
		"The dedicated summary-hierarchy endpoints recover direct parent/child edges when available. Summary annotation bodies and person/pipeline memberships still require relationship projections or an enumerable association API. A person association can mean authorship or involvement, not exclusive subject matter.",
		"Selected scope controls which collections are read. Unselected relationships have no local links; event-derived source/person/website connections cannot be recovered when events are omitted. Domain filtering checks exposed URLs and known dependencies, not unseen origins.",
		"IDs and graph metadata are held in memory; record bodies are staged on disk. Large deployments need sizing validation.",
		"Final ID reconciliation detects set changes, not all in-place edits; pause capture/editing for a quieter export interval.",
	}}
	if !o.RelatedSince.IsZero() {
		r.manifest.RelatedSince = o.RelatedSince.UTC().Format(time.RFC3339Nano)
	}
	if o.Mode == "filtered" {
		r.manifest.PolicyHash = o.Scanner.Hash
		r.manifest.CategoryHashes = o.Scanner.ListHashes
		r.manifest.Limitations = append(r.manifest.Limitations, "Secret/financial detection and domain categories are best effort, not anonymity or semantic-content guarantees.", "Financial detection includes Luhn-valid cards, checksum-valid IBANs, and formatted US SSNs; generic bank account numbers and Presidio NLP are not implemented.", "Unsupported binary/data-URL representations are withheld; alternate embedded reference projections are removed.")
	}
	version, err := client.Probe(ctx)
	if err != nil {
		return r.manifest, err
	}
	clean, _, err := o.Scanner.Sanitize(ctx, map[string]any{"version": version})
	if err != nil {
		return r.manifest, err
	}
	r.manifest.OSVersion = fieldString(clean, "version")
	for _, m := range o.Materials {
		if ctx.Err() != nil {
			return r.manifest, ctx.Err()
		}
		r.progress.Stage("Inventory "+m.Type, 0)
		cov := &Coverage{Material: m.Type, InventoryMode: "full", InitialCount: -1, FinalCount: -1}
		r.coverage[m.Type] = cov
		r.manifest.Coverage = append(r.manifest.Coverage, cov)
		if o.ReferenceOnly[m.Type] {
			cov.InventoryMode = "references"
			continue
		}
		ids, err := r.inventoryMaterial(m, cov)
		if err != nil {
			if errors.Is(err, ErrOSBusy) || ctx.Err() != nil {
				return r.manifest, err
			}
			r.issue(m.Type, "", "inventory_failed")
			continue
		}
		r.inventory[m.Type] = ids
		cov.Inventoried = len(ids)
		if m.Collection == "" {
			if len(ids) > 0 {
				r.issue(m.Type, "", "no_record_read_endpoint")
			}
			continue
		}
		r.progress.Stage("Fetch "+m.Type, len(ids))
		for start := 0; start < len(ids); {
			end := min(start+client.BatchSize(m, o.BatchSize), len(ids))
			if err := r.fetch(m, ids[start:end]); err != nil {
				return r.manifest, err
			}
			r.progress.Add(end - start)
			start = end
			if ctx.Err() != nil {
				return r.manifest, ctx.Err()
			}
		}
	}
	if err := r.resolveSummaryHierarchy(); err != nil {
		return r.manifest, err
	}
	if err := r.resolveUserPeople(); err != nil {
		return r.manifest, err
	}
	if err := r.loadPersonEvidence(); err != nil {
		return r.manifest, err
	}
	r.progress.Stage("Resolve references", 0)
	if err := r.resolveReferences(); err != nil {
		return r.manifest, err
	}
	r.progress.Stage("Reconcile inventories", 0)
	// A second identity pass makes concurrent additions/deletions explicit; no snapshot claim.
	for _, m := range o.Materials {
		if m.SnapshotOnly || o.ReferenceOnly[m.Type] {
			continue
		}
		cov := r.coverage[m.Type]
		if cov.InitialCount < 0 {
			continue
		}
		ids, err := client.IDs(ctx, m, Window{})
		if err != nil {
			if errors.Is(err, ErrOSBusy) || ctx.Err() != nil {
				return r.manifest, err
			}
			r.issue(m.Type, "", "final_inventory_failed")
			continue
		}
		cov.FinalCount = len(ids)
		if !sameIDs(ids, r.inventory[m.Type]) {
			r.issue(m.Type, "", "inventory_changed_during_export")
		}
	}
	if ctx.Err() != nil {
		return r.manifest, ctx.Err()
	}
	if o.Mode == "filtered" {
		r.progress.Stage("Privacy reconciliation", 0)
		if err = r.rescanKnownCredentials(); err != nil {
			return r.manifest, err
		}
	}
	if err = r.recoverSDKCacheRelationships(); err != nil {
		return r.manifest, err
	}
	r.reconcileSummaryAnnotations()
	if err = r.filterGraph(); err != nil {
		return r.manifest, err
	}
	r.progress.Stage("Select people and build persona navigation", 0)
	if err = r.preparePeople(); err != nil {
		return r.manifest, err
	}
	for _, meta := range r.meta {
		cov := r.coverage[meta.Type]
		if cov == nil {
			continue
		}
		switch meta.State {
		case "included":
			cov.Included++
			if meta.Redactions > 0 {
				cov.Redacted++
			}
		case "omitted":
			cov.Omitted++
		case "excluded":
			cov.Excluded++
		case "withheld":
			cov.Withheld++
		}
	}
	return r.finish(abs)
}

func (r *run) finish(destination string) (Manifest, error) {
	var err error
	o := r.opts
	stage := r.stage
	r.progress.Stage("Assign paths and build graph", 0)
	r.collectScopeOmissions()
	r.collectRelationshipCoverage()
	if err = r.render(); err != nil {
		return r.manifest, err
	}
	if o.Mode == "filtered" {
		if err = r.auditOutput(); err != nil {
			return r.manifest, err
		}
	}
	if o.Format != "markdown" {
		if err = r.renderPDFs(); err != nil {
			return r.manifest, err
		}
	}
	r.progress.Stage("Native and portable metadata", len(r.documentMetadata))
	if err = r.applyMetadata(); err != nil {
		return r.manifest, err
	}
	r.progress.Stage("Validate links and output", 0)
	if err = r.validateMarkdownLinks(); err != nil {
		return r.manifest, err
	}
	r.manifest.Status = "complete_for_implemented_scope"
	if len(r.manifest.Issues) > 0 {
		r.manifest.Status = "partial"
	}
	if r.client != nil {
		r.manifest.Performance = r.client.Performance()
	}
	if err = r.writeArchiveState(); err != nil {
		return r.manifest, err
	}
	r.manifest.Finished = time.Now().UTC()
	if err = writeJSON(filepath.Join(stage, "manifest.json"), r.manifest); err != nil {
		return r.manifest, err
	}
	if o.Mode == "filtered" {
		if err = r.auditOutput(); err != nil {
			return r.manifest, err
		}
	}
	if err = os.Rename(stage, destination); err != nil {
		return r.manifest, err
	}
	return r.manifest, nil
}

func (r *run) issue(t, id, code string) {
	key := ""
	if id != "" {
		key = opaque(t, id)
	}
	r.manifest.Issues = append(r.manifest.Issues, Issue{t, key, code})
}
func (r *run) inventoryMaterial(m Material, cov *Coverage) ([]string, error) {
	if m.SnapshotOnly {
		var out map[string]any
		if err := r.client.JSON(r.ctx, "GET", m.Collection+"?transferables=true", nil, &out); err != nil {
			return nil, err
		}
		ids := []string{}
		items, ok := out["iterable"].([]any)
		if !ok {
			return nil, errConfig("snapshot has no iterable")
		}
		for _, x := range items {
			if v, ok := x.(map[string]any); ok {
				if id := fieldString(v, "id"); id != "" {
					ids = append(ids, id)
					continue
				}
			}
			return nil, errConfig("snapshot contains a record without an ID")
		}
		cov.InitialCount = len(unique(ids))
		cov.FinalCount = -1
		return unique(ids), nil
	}
	n, err := r.client.Count(r.ctx, m, Window{})
	if err != nil {
		return nil, err
	}
	cov.InitialCount = n
	var ids []string
	if n > r.opts.WindowIDs {
		ids, err = r.client.WindowedIDs(r.ctx, m, Window{}, r.opts.WindowIDs, 0)
	} else {
		ids, err = r.client.IDs(r.ctx, m, Window{})
	}
	if err != nil {
		return nil, err
	}
	// Unfiltered audit catches missing timestamps; the API still lacks a real cursor.
	if n > r.opts.WindowIDs {
		all, err := r.client.IDs(r.ctx, m, Window{})
		if err != nil {
			return nil, err
		}
		ids = unique(append(ids, all...))
	}
	if len(ids) != n {
		r.issue(m.Type, "", "count_inventory_mismatch")
	}
	return ids, nil
}
func (r *run) fetch(m Material, ids []string) error {
	pending := map[string]bool{}
	for _, id := range ids {
		key := m.Type + "\x00" + id
		if _, ok := r.meta[key]; !ok {
			pending[id] = true
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if m.Batch != "" {
		items := []map[string]string{}
		for id := range pending {
			items = append(items, map[string]string{"id": id})
		}
		body := map[string]any{m.Field: map[string]any{"iterable": items}}
		var out map[string]any
		err := r.client.JSON(r.ctx, "POST", m.Batch+"?transferables=true", body, &out)
		if errors.Is(err, ErrOSBusy) || r.ctx.Err() != nil {
			return err
		}
		if err == nil {
			collection, _ := out[m.Field].(map[string]any)
			iterable, _ := collection["iterable"].([]any)
			for _, item := range iterable {
				if v, ok := item.(map[string]any); ok {
					id := fieldString(v, "id")
					if pending[id] {
						if err := r.store(m, v, false); err != nil {
							return err
						}
						delete(pending, id)
					} else {
						r.issue(m.Type, "", "unexpected_batch_id")
					}
				}
			}
		}
	}
	for id := range pending {
		var v map[string]any
		var err error
		if m.Singular != "" {
			param := "?transferables=true"
			if m.Type == "FORMATS" {
				param = "?transferable=true"
			}
			err = r.client.JSON(r.ctx, "GET", m.Singular+url.PathEscape(id)+param, nil, &v)
		} else {
			err = errConfig("no singular read")
		}
		if errors.Is(err, ErrOSBusy) || r.ctx.Err() != nil {
			return err
		}
		if err != nil || fieldString(v, "id") != id {
			r.meta[m.Type+"\x00"+id] = &Meta{Key: m.Type + "\x00" + id, ID: id, Type: m.Type, State: "missing"}
			r.issue(m.Type, id, "record_fetch_failed")
			continue
		}
		if err := r.store(m, v, false); err != nil {
			return err
		}
	}
	return nil
}
func (r *run) store(m Material, v map[string]any, replace bool) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	id := fieldString(v, "id")
	key := m.Type + "\x00" + id
	name := opaque(m.Type, id)
	meta := &Meta{Key: key, ID: id, Type: m.Type, Folder: m.Folder, Path: "markdown/" + m.Folder + "/" + name + ".md", DataPath: "data/" + m.Folder + "/" + name + ".json", State: "included"}
	if r.opts.Mode == "preserve" {
		meta.DataPath = "raw/" + m.Folder + "/" + name + ".json"
	}
	meta.Edges = extractEdges(m.Type, id, v)
	meta.ProjectionStates = projectionStates(m.Type, v)
	if m.Type == "WORKSTREAM_SUMMARIES" {
		meta.SupplementableFields = map[string]bool{}
		for _, field := range cacheRelations {
			meta.SupplementableFields[field] = v[field] == nil
		}
		for _, field := range []string{"annotations", "persons", "pipelines"} {
			if object(v, field) == nil {
				meta.RelationshipProjectionUnknown = true
				break
			}
		}
	} else if m.Type == "PIPELINES" {
		meta.RelationshipProjectionUnknown = object(v, "summaries") == nil
	}
	if m.Type == "PERSONS" {
		meta.PersonProjection = personFacts(v).Projected
	}
	if r.opts.Mode == "filtered" {
		r.opts.Scanner.ObserveCredentials(v)
		if m.Type == "SENSITIVES" {
			category := fieldString(v, "category")
			if strings.Contains(category, "SECRET") || strings.Contains(category, "TOKEN") || category == "API_KEY" || category == "PRIVATE_KEY" || category == "ACCESS_KEY" {
				r.opts.Scanner.remember(fieldString(v, "text"))
			}
		}
		clean, stats, err := r.opts.Scanner.Sanitize(r.ctx, v)
		r.manifest.WithheldRepresentations += stats.WithheldRepresentations
		meta.Redactions = stats.Redactions
		if err != nil {
			meta.State = "withheld"
			r.issue(m.Type, id, "scan_or_encoding_failed")
		} else if stats.Denied {
			meta.State = "excluded"
		} else if fieldString(clean, "id") != id {
			meta.State = "withheld"
			r.issue(m.Type, id, "sensitive_identifier")
		} else {
			v = clean
		}
	}
	meta.Title = title(v, m)
	meta.Created = timestamp(v, "created")
	meta.Updated = timestamp(v, "updated")
	meta.AnnotationType = fieldString(v, "type")
	meta.SummaryKind = fieldString(v, "parentHierarchicalType")
	meta.SummaryDescriptor = fieldString(v, "parentHierarchicalTypeDescriptor")
	if meta.State == "included" {
		if err := writeJSON(filepath.Join(r.stage, meta.DataPath), v); err != nil {
			meta.State = "withheld"
			r.issue(m.Type, id, "record_write_failed")
			r.meta[key] = meta
			if !replace {
				r.coverage[m.Type].Fetched++
			}
			// A destination failure is unlikely to be record-specific. Stop before
			// fetching more source data into an unwritable/full filesystem.
			return errConfig("could not write an export record; check destination space and permissions; partial directory was not finalized")
		}
	}
	r.meta[key] = meta
	if !replace {
		r.coverage[m.Type].Fetched++
	}
	return nil
}
func (r *run) sortedMeta() []*Meta {
	result := make([]*Meta, 0, len(r.meta))
	for _, m := range r.meta {
		result = append(result, m)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}
func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func splitRef(key string) (string, string, bool) {
	a, b, ok := strings.Cut(key, "\x00")
	return a, b, ok
}

func references(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	ids := []string{}
	negative := map[string]bool{}
	if indices, ok := m["indices"].(map[string]any); ok {
		for id, n := range indices {
			f := 0.0
			switch x := n.(type) {
			case json.Number:
				f, _ = x.Float64()
			case float64:
				f = x
			case int:
				f = float64(x)
			default:
				continue
			}
			if f < 0 {
				negative[id] = true
			} else {
				ids = append(ids, id)
			}
		}
	}
	if id := fieldString(m, "id"); id != "" {
		ids = append(ids, id)
	}
	if items, ok := m["iterable"].([]any); ok {
		for _, item := range items {
			switch x := item.(type) {
			case string:
				ids = append(ids, x)
			case map[string]any:
				if id := fieldString(x, "id"); id != "" {
					ids = append(ids, id)
				}
			}
		}
	}
	active := []string{}
	for _, id := range ids {
		if !negative[id] {
			active = append(active, id)
		}
	}
	return unique(active)
}
func extractEdges(t, id string, v map[string]any) []Edge {
	edges := []Edge{}
	for field, targetType := range referenceTypes {
		for _, target := range references(v[field]) {
			edges = append(edges, Edge{t + "\x00" + id, targetType + "\x00" + target, field})
		}
	}
	var texts func(any)
	texts = func(x any) {
		switch a := x.(type) {
		case map[string]any:
			for _, b := range a {
				texts(b)
			}
		case []any:
			for _, b := range a {
				texts(b)
			}
		case string:
			for _, match := range piecesPattern.FindAllStringSubmatch(a, -1) {
				if targetType, ok := piecesTypes[strings.ToLower(match[1])]; ok {
					if target, err := url.PathUnescape(match[2]); err == nil {
						edges = append(edges, Edge{t + "\x00" + id, targetType + "\x00" + target, "embedded_markdown"})
					}
				}
			}
		}
	}
	texts(v)
	seen := map[Edge]bool{}
	uniqueEdges := []Edge{}
	for _, e := range edges {
		if !seen[e] {
			seen[e] = true
			uniqueEdges = append(uniqueEdges, e)
		}
	}
	sort.Slice(uniqueEdges, func(i, j int) bool {
		if uniqueEdges[i].Relation != uniqueEdges[j].Relation {
			return uniqueEdges[i].Relation < uniqueEdges[j].Relation
		}
		return uniqueEdges[i].Target < uniqueEdges[j].Target
	})
	return uniqueEdges
}

func (r *run) filterGraph() error {
	if r.opts.Mode != "filtered" {
		return nil
	}
	if r.opts.Scanner.Policy.StrictDerived && r.opts.Scanner.SourceFiltering() {
		for _, m := range r.meta {
			if m.State == "included" && generated(m) {
				m.State = "withheld"
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, m := range r.meta {
			if m.State != "included" {
				continue
			}
			for _, e := range m.Edges {
				target := r.meta[e.Target]
				if target == nil || target.State == "included" {
					continue
				}
				dependent := m.Type == "WORKSTREAM_SUMMARIES" && (e.Relation == "events" || e.Relation == "children" || e.Relation == "summaries" || e.Relation == "annotations")
				dependent = dependent || generated(m) && (e.Relation == "workstream_events" || e.Relation == "summaries" || e.Relation == "summaryRoot")
				dependent = dependent || e.Relation == "websites" || e.Relation == "source_windows" || e.Relation == "sources" || e.Relation == "embedded_markdown"
				if dependent {
					m.State = "withheld"
					changed = true
					break
				}
			}
		}
		// An annotation body must not survive merely because its excluded summary is another node.
		for _, m := range r.meta {
			if m.Type == "WORKSTREAM_SUMMARIES" && m.State != "included" {
				for _, e := range m.Edges {
					if e.Relation == "annotations" {
						if target := r.meta[e.Target]; target != nil && target.State == "included" {
							target.State = "withheld"
							changed = true
						}
					}
				}
			}
		}
	}
	for _, m := range r.meta {
		if m.State != "included" && m.DataPath != "" {
			if err := os.Remove(filepath.Join(r.stage, m.DataPath)); err != nil && !os.IsNotExist(err) {
				return errConfig("cannot remove withheld content; export was not finalized")
			}
		}
	}
	return nil
}
func generated(m *Meta) bool {
	return m.Type == "WORKSTREAM_SUMMARIES" || m.Type == "CONVERSATION_MESSAGES" || m.Type == "ANNOTATIONS" && (strings.Contains(m.AnnotationType, "SUMMARY") || m.AnnotationType == "COMPACTION")
}

// Credentials found late in a profile or Sensitive record may also occur in earlier prose.
func (r *run) rescanKnownCredentials() error {
	for _, m := range r.sortedMeta() {
		if m.State != "included" {
			continue
		}
		v, err := readRecord(filepath.Join(r.stage, m.DataPath))
		if err != nil {
			return err
		}
		clean, stats, err := r.opts.Scanner.Sanitize(r.ctx, v)
		if err != nil || fieldString(clean, "id") != m.ID {
			m.State = "withheld"
			r.issue(m.Type, m.ID, "final_record_scan_failed")
			continue
		}
		if stats.Denied {
			m.State = "excluded"
			continue
		}
		m.Redactions += stats.Redactions
		material, _ := materialByType(m.Type)
		m.Title = title(clean, material)
		m.SummaryKind = fieldString(clean, "parentHierarchicalType")
		m.SummaryDescriptor = fieldString(clean, "parentHierarchicalTypeDescriptor")
		m.Created = timestamp(clean, "created")
		m.Updated = timestamp(clean, "updated")
		if err := rewriteJSON(filepath.Join(r.stage, m.DataPath), clean); err != nil {
			return err
		}
	}
	return nil
}
