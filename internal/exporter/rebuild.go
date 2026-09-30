package exporter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type RebuildInfo struct {
	LegacyPersonEvidence    bool             `json:"legacy_person_evidence_reconciled"`
	LegacyPersonsReconciled int              `json:"legacy_persons_reconciled"`
	LegacyPersonsUnknown    int              `json:"legacy_persons_with_unknown_annotations"`
	SourceManifestSHA256    string           `json:"source_manifest_sha256"`
	SourceToolVersion       string           `json:"source_tool_version"`
	SourceStarted           time.Time        `json:"source_started"`
	SourceFinished          time.Time        `json:"source_finished"`
	OriginalReadStarted     time.Time        `json:"original_read_started"`
	OriginalReadFinished    time.Time        `json:"original_read_finished"`
	SourceStatus            string           `json:"source_status"`
	SourceCoverage          []*Coverage      `json:"source_coverage"`
	SourcePeople            PeopleStats      `json:"source_people"`
	SourcePerformance       PerformanceStats `json:"source_performance"`
	LegacyEvidence          bool             `json:"legacy_evidence"`
	UnavailableTargets      int              `json:"unavailable_dependency_targets"`
	BlockedCacheBodies      int              `json:"bodies_with_unavailable_cached_summary"`
}

type RebuildOptions struct {
	Source string
	Options
}

func InspectArchive(source string) (Manifest, error) {
	absolute, err := filepath.Abs(source)
	if err != nil || source == "" {
		return Manifest{}, errConfig("rebuild requires a finalized source archive")
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil || strings.HasSuffix(absolute, ".partial") {
		return Manifest{}, errConfig("rebuild requires a finalized source archive, not an active .partial directory")
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return Manifest{}, errConfig("source archive cannot be opened")
	}
	defer root.Close()
	b, err := archiveRead(root, "manifest.json", 16<<20)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if decodeArchiveJSON(b, &m) != nil || (m.FormatVersion != 4 && m.FormatVersion != 5) || (m.Status != "partial" && m.Status != "complete_for_implemented_scope") || m.Finished.IsZero() || (m.Mode != "filtered" && m.Mode != "preserve") {
		return Manifest{}, errConfig("source manifest is unfinished or unsupported")
	}
	for _, c := range m.Coverage {
		if c == nil || c.Included < 0 {
			return Manifest{}, errConfig("source coverage contains invalid entries")
		}
	}
	return m, nil
}

// No Client is constructed: rebuilding cannot discover, launch, close, or query
// Pieces OS. It writes a new archive and only imports canonical included JSON.
func Rebuild(ctx context.Context, input RebuildOptions) (Manifest, error) {
	input.PDFLimits = input.PDFLimits.defaults()
	if err := input.PDFLimits.Validate(); err != nil {
		return Manifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	source, err := filepath.Abs(input.Source)
	if err != nil || input.Source == "" || strings.HasSuffix(strings.TrimRight(source, string(filepath.Separator)), ".partial") {
		return Manifest{}, errConfig("rebuild requires a finalized archive, not an active .partial directory")
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil || strings.HasSuffix(source, ".partial") {
		return Manifest{}, errConfig("source archive must be an existing finalized directory")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return Manifest{}, errConfig("source archive cannot be opened")
	}
	defer root.Close()
	manifestBytes, err := archiveRead(root, "manifest.json", 16<<20)
	if err != nil {
		return Manifest{}, err
	}
	var original Manifest
	if err := decodeArchiveJSON(manifestBytes, &original); err != nil || (original.FormatVersion != 4 && original.FormatVersion != 5) || (original.Status != "partial" && original.Status != "complete_for_implemented_scope") || original.Finished.IsZero() || original.Started.IsZero() || original.Finished.Before(original.Started) {
		return Manifest{}, errConfig("source manifest is unfinished or uses an unsupported archive format")
	}
	if original.FormatVersion == 5 && (original.ArchiveState == nil || original.ArchiveState.Version != 1) {
		return Manifest{}, errConfig("archive reconstruction evidence is missing or unsupported")
	}
	if s := original.ArchiveState; s != nil && (s.Version != 1 || !validDigest(s.StateSHA256) || !validDigest(s.GraphSHA256) || !validDigest(s.LinkMapSHA256)) {
		return Manifest{}, errConfig("archive reconstruction checksums are missing or invalid")
	}
	o := input.Options
	if o.SignalDigest.Mode == "" && original.SignalDigest != nil {
		o.SignalDigest.Mode = original.SignalDigest.Options.Mode
	}
	o.SignalDigest = o.SignalDigest.defaults()
	if err := o.SignalDigest.Validate(); err != nil {
		return Manifest{}, err
	}
	if o.Scanner == nil || (original.Mode != "filtered" && original.Mode != "preserve") {
		return Manifest{}, errConfig("rebuild requires a policy scanner and a supported source privacy mode")
	}
	if original.Mode == "filtered" && (original.PolicyHash != o.Scanner.Hash || !maps.Equal(original.CategoryHashes, o.Scanner.ListHashes)) {
		return Manifest{}, errConfig("rebuild requires the original privacy policy and unchanged domain lists; policy/category hashes differ")
	}
	if o.Mode != "" && o.Mode != original.Mode {
		return Manifest{}, errConfig("rebuild cannot change the archive privacy mode")
	}
	o.Mode = original.Mode
	if o.Format == "" {
		o.Format = original.Format
	}
	if o.Format != "markdown" && o.Format != "pdf" && o.Format != "both" {
		return Manifest{}, errConfig("format must be markdown, pdf, or both")
	}
	if o.Timezone == "" {
		o.Timezone = original.Timezone
	}
	if _, err := time.LoadLocation(o.Timezone); err != nil {
		return Manifest{}, errConfig("invalid rebuild timezone")
	}
	if o.Naming == "" {
		o.Naming = original.Naming
	}
	if o.Naming == "" {
		o.Naming = "readable"
	}
	if o.Relationships == "" {
		o.Relationships = original.Relationships
	}
	if o.Relationships == "" {
		o.Relationships = "both"
	}
	if o.Metadata == "" {
		o.Metadata = original.Metadata
	}
	if o.Metadata == "" {
		o.Metadata = "off"
	}
	if (o.Naming != "readable" && o.Naming != "opaque") || (o.Relationships != "both" && o.Relationships != "inline" && o.Relationships != "sidecar") || (o.Metadata != "off" && o.Metadata != "auto") {
		return Manifest{}, errConfig("invalid rebuild naming, relationship, or metadata option")
	}
	if o.PDFFont != "" {
		if err := ValidatePDFFont(ctx, o.PDFFont); err != nil {
			return Manifest{}, err
		}
	}
	if o.RelatedOrder == "" {
		o.RelatedOrder = original.RelatedOrder
	}
	if o.RelatedLimit == 0 {
		o.RelatedLimit = original.RelatedLimit
	}
	if (o.RelatedOrder != "relevance" && o.RelatedOrder != "recent") || o.RelatedLimit < 1 || o.RelatedLimit > 500 {
		return Manifest{}, errConfig("invalid rebuild related-summary settings")
	}
	if o.RelatedSince.IsZero() && original.RelatedSince != "" {
		o.RelatedSince, err = time.Parse(time.RFC3339Nano, original.RelatedSince)
		if err != nil {
			return Manifest{}, errConfig("source archive has an invalid related-summary cutoff")
		}
	}
	if o.PeopleMode == "" {
		o.PeopleMode = original.People.Mode
	}
	if o.PeopleMode != original.People.Mode && !(original.People.Mode == "all" && (o.PeopleMode == "profiles" || o.PeopleMode == "connected")) {
		return Manifest{}, errConfig("rebuild can retain the original people mode or narrow an all-people archive; omitted people cannot be restored")
	}
	if o.MinPersonConnections == 0 {
		o.MinPersonConnections = original.People.MinConnections
	}
	if o.MinPersonConnections < 1 || (o.PeopleMode != "all" && o.PeopleMode != "profiles" && o.PeopleMode != "connected") {
		return Manifest{}, errConfig("invalid source people selection")
	}
	if err := ValidateSDKCaches(ctx, o.SDKCaches); err != nil {
		return Manifest{}, err
	}
	if len(o.SDKCaches) > 0 && original.SDKCache.Selected > 0 {
		return Manifest{}, errConfig("additional cache recovery requires an archive without prior cache recovery; rebuild the original archive to compare a new cache set")
	}
	abs, err := rebuildDestination(source, o.Output)
	if err != nil {
		return Manifest{}, err
	}
	r := &run{ctx: ctx, opts: o, stage: abs + ".partial", rebuilding: true, meta: map[string]*Meta{}, coverage: map[string]*Coverage{}, inventory: map[string][]string{}, userPersonIDs: map[string]bool{}, derivedEdges: map[Edge]bool{}, cachedEdges: map[Edge]CacheEvidence{}}
	r.manifest = original
	r.legacySignalPrivacy = original.SignalPrivacyVersion < currentSignalPrivacyVersion && o.Mode == "filtered" && o.Scanner.SourceFiltering()
	r.manifest.PDFLimits = nil // New rendering uses this invocation's budgets.
	digest := sha256.Sum256(manifestBytes)
	r.manifest.Rebuild = &RebuildInfo{SourceManifestSHA256: hex.EncodeToString(digest[:]), SourceToolVersion: original.ToolVersion, SourceStarted: original.Started, SourceFinished: original.Finished, SourceStatus: original.Status, SourceCoverage: original.Coverage, SourcePeople: original.People, SourcePerformance: original.Performance, LegacyEvidence: original.ArchiveState == nil}
	r.manifest.Rebuild.OriginalReadStarted, r.manifest.Rebuild.OriginalReadFinished = original.Started, original.Finished
	if original.Rebuild != nil {
		if original.Rebuild.OriginalReadStarted.IsZero() || original.Rebuild.OriginalReadFinished.Before(original.Rebuild.OriginalReadStarted) {
			return Manifest{}, errConfig("source rebuild has an invalid original read interval")
		}
		r.manifest.Rebuild.LegacyEvidence = r.manifest.Rebuild.LegacyEvidence || original.Rebuild.LegacyEvidence
		r.manifest.Rebuild.OriginalReadStarted = original.Rebuild.OriginalReadStarted
		r.manifest.Rebuild.OriginalReadFinished = original.Rebuild.OriginalReadFinished
	}
	r.manifest.Coverage = nil
	r.manifest.ArchiveState = nil
	r.manifest.ToolVersion, r.manifest.FormatVersion = o.Version, 5
	r.manifest.Format, r.manifest.Timezone = o.Format, o.Timezone
	r.manifest.Naming, r.manifest.Relationships, r.manifest.Metadata = o.Naming, o.Relationships, o.Metadata
	r.manifest.Started, r.manifest.Finished = time.Now().UTC(), time.Time{}
	r.manifest.Status, r.manifest.Performance = "running", PerformanceStats{}
	r.manifest.RelatedOrder, r.manifest.RelatedLimit = o.RelatedOrder, o.RelatedLimit
	if !o.RelatedSince.IsZero() {
		r.manifest.RelatedSince = o.RelatedSince.UTC().Format(time.RFC3339Nano)
	}
	r.manifest.SDKCache.RetainedEdges = 0
	r.manifest.Limitations = append(r.manifest.Limitations, "Offline rebuild of already exported records. No OS requests or current-source reconciliation. Original omissions and coverage issues persist; missing bodies cannot be fetched offline. Input checksums detect accidental alteration, not authenticated provenance.")
	r.opts.Materials, r.opts.ReferenceOnly = nil, map[string]bool{}
	expected := 0
	for _, cov := range original.Coverage {
		if cov == nil {
			return Manifest{}, errConfig("source coverage contains a null material entry")
		}
		material, ok := materialByType(cov.Material)
		if !ok || r.coverage[cov.Material] != nil || cov.Included < 0 || cov.Excluded < 0 || cov.Withheld < 0 || cov.Omitted < 0 {
			return Manifest{}, errConfig("source coverage has invalid or duplicate material entries")
		}
		c := *cov
		c.Included, c.Redacted, c.Fetched = 0, 0, 0
		r.coverage[c.Material] = &c
		r.manifest.Coverage = append(r.manifest.Coverage, &c)
		r.opts.Materials = append(r.opts.Materials, material)
		r.opts.ReferenceOnly[c.Material] = c.InventoryMode == "references"
		expected += cov.Included
	}
	if len(o.SDKCaches) > 0 {
		if err := ValidateSDKCacheMaterials(r.opts.Materials); err != nil {
			return Manifest{}, err
		}
	}
	if r.manifest.Scope.Name == "" {
		r.manifest.Scope = scopeFor(r.opts)
	}
	if err := os.MkdirAll(filepath.Dir(r.stage), 0700); err != nil {
		return Manifest{}, errConfig("cannot create rebuild destination parent")
	}
	if err := os.Mkdir(r.stage, 0700); err != nil {
		return Manifest{}, errConfig("rebuild partial directory exists or cannot be created")
	}
	r.progress = startProgress(o.Progress, nil)
	defer r.progress.Close()
	if r.progress != nil {
		r.opts.Progress = r.progress.out
	}
	if original.ArchiveState != nil {
		f, err := archiveOpen(root, "link-map.json")
		if err != nil {
			return r.manifest, errConfig("archive link map is missing or unsafe")
		}
		hash, err := readerDigest(ctx, f)
		f.Close()
		if err != nil || hash != original.ArchiveState.LinkMapSHA256 {
			return r.manifest, errConfig("archive link map checksum differs")
		}
	}
	links, err := archiveLinkMap(ctx, root, expected)
	if err != nil {
		return r.manifest, err
	}
	byRef, byPath := map[string]*Meta{}, map[string]*Meta{}
	r.progress.Stage("Read included archive records", expected)
	prefix := "data"
	if o.Mode == "preserve" {
		prefix = "raw"
	}
	for _, cov := range original.Coverage {
		material, _ := materialByType(cov.Material)
		folder := prefix + "/" + material.Folder
		count := 0
		err := archiveDirectory(root, folder, func(entry fs.DirEntry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			ref := strings.TrimSuffix(entry.Name(), ".json")
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !validDigest(ref) || links[ref] == "" || byRef[ref] != nil {
				return errConfig("archive record directory contains unexpected or duplicate files")
			}
			b, err := archiveRead(root, folder+"/"+entry.Name(), 128<<20)
			if err != nil {
				return err
			}
			v := map[string]any{}
			if decodeArchiveJSON(b, &v) != nil || fieldString(v, "id") == "" || opaque(material.Type, fieldString(v, "id")) != ref {
				return errConfig("archive record identity does not match its canonical filename")
			}
			if err := r.store(material, v, false); err != nil {
				return err
			}
			m := r.meta[material.Type+"\x00"+fieldString(v, "id")]
			// The exported public graph is the authoritative retained edge set.
			// Pruned/embedded JSON must not silently restore private relationships.
			m.Edges = nil
			h := sha256.Sum256(b)
			m.ArchiveDataSHA256 = hex.EncodeToString(h[:])
			byRef[ref], byPath[links[ref]] = m, m
			count++
			r.progress.Add(1)
			return nil
		})
		if err != nil && !(errors.Is(err, fs.ErrNotExist) && cov.Included == 0) {
			if ctx.Err() != nil {
				return r.manifest, ctx.Err()
			}
			return r.manifest, errConfig("archive record collection is unreadable or invalid; rebuild was not finalized")
		}
		if count != cov.Included {
			return r.manifest, errConfig("archive record count does not match the source manifest")
		}
	}
	if len(byRef) != len(links) {
		return r.manifest, errConfig("archive link map contains missing records")
	}
	if original.ArchiveState != nil {
		if err := r.restoreArchiveState(root, original.ArchiveState, byRef); err != nil {
			return r.manifest, err
		}
	} else {
		r.issue("ARCHIVE", "", "legacy_reconstruction_evidence_incomplete")
		for _, m := range byRef {
			// Legacy archives did not retain original eligibility for the newly
			// supported wrapped types. Pruned JSON cannot safely establish it.
			if m.Type != "WORKSTREAM_SUMMARIES" {
				m.SupplementableFields = nil
			}
			for field, state := range m.ProjectionStates {
				if state != "linked" {
					m.ProjectionStates[field] = "absent"
				}
			}
		}
		if err := r.restoreLegacyUsers(root, byPath); err != nil {
			return r.manifest, err
		}
		r.manifest.Warnings = append(r.manifest.Warnings, "Legacy archive has no reconstruction checksums, original projection states, or per-record redaction/selection evidence. Verified user labels were replayed only from its existing user profile links. Coverage remains partial; unknown person evidence is retained conservatively.")
	}
	if err := r.restoreArchiveGraph(root, original.ArchiveState, byPath); err != nil {
		return r.manifest, err
	}
	r.restoreLegacyPersonEvidence(original, manifestBytes, byRef)
	if o.Mode == "filtered" {
		if err := r.rescanKnownCredentials(); err != nil {
			return r.manifest, err
		}
	}
	if err := r.recoverSDKCacheRelationships(); err != nil {
		return r.manifest, err
	}
	r.reconcileAnnotationAttachments()
	if err := r.filterGraph(); err != nil {
		return r.manifest, err
	}
	r.reconcileEventPersonAssociationEdges()
	if err := r.preparePeople(); err != nil {
		return r.manifest, err
	}
	if err := r.filterAssociationRecords(); err != nil {
		return r.manifest, err
	}
	for _, m := range r.meta {
		if m.ArchivePlaceholder {
			continue
		}
		cov := r.coverage[m.Type]
		switch m.State {
		case "included":
			cov.Included++
			if m.Redactions > 0 {
				cov.Redacted++
			}
		case "excluded":
			cov.Excluded++
		case "withheld":
			cov.Withheld++
		case "omitted":
			cov.Omitted++
		}
	}
	for _, cov := range original.Coverage {
		r.coverage[cov.Material].Fetched = cov.Fetched
		if cov.Redacted < 0 || original.ArchiveState == nil && cov.Redacted > 0 {
			r.coverage[cov.Material].Redacted = -1
		}
	}
	if original.Status == "partial" && len(r.manifest.Issues) == 0 {
		r.issue("ARCHIVE", "", "source_archive_partial")
	}
	return r.finish(abs)
}

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && strings.ToLower(s) == s
}

func safeArchivePath(name string) bool {
	return name != "." && fs.ValidPath(name) && !strings.ContainsAny(name, "\\:\x00")
}

func archiveOpen(root *os.Root, name string) (*os.File, error) {
	if !safeArchivePath(name) {
		return nil, errConfig("archive contains an unsafe relative path")
	}
	current := ""
	for _, component := range strings.Split(name, "/") {
		current = path.Join(current, component)
		st, err := root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, errConfig("archive symlinks are not supported")
		}
		if current == name && !st.Mode().IsRegular() {
			return nil, errConfig("archive input must be a regular file")
		}
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, errConfig("archive file could not be opened")
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, errConfig("archive input must be a regular file")
	}
	return f, nil
}

func archiveRead(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := archiveOpen(root, name)
	if err != nil {
		return nil, errConfig("archive file is missing, unsafe, or unreadable")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errConfig("archive file exceeds its read bound or could not be read")
	}
	return b, nil
}

func decodeArchiveJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errConfig("archive JSON has trailing content")
	}
	return nil
}

func archiveDirectory(root *os.Root, name string, visit func(fs.DirEntry) error) error {
	st, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errConfig("archive collection must be a real directory")
	}
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		entries, err := f.ReadDir(128)
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return errConfig("archive symlinks are not supported")
			}
			if err := visit(entry); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func archiveLinkMap(ctx context.Context, root *os.Root, expected int) (map[string]string, error) {
	f, err := archiveOpen(root, "link-map.json")
	if err != nil {
		return nil, errConfig("archive link map is missing or unsafe")
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<30))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, errConfig("invalid archive link map")
	}
	links, used := map[string]string{}, map[string]bool{}
	for d.More() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tok, err := d.Token()
		key, ok := tok.(string)
		var value string
		if err != nil || !ok || !validDigest(key) || d.Decode(&value) != nil || !safeArchivePath(value) || !strings.HasSuffix(value, ".md") || links[key] != "" || used[strings.ToLower(value)] || len(links) >= expected {
			return nil, errConfig("invalid, duplicate, or unsafe archive link map entry")
		}
		links[key], used[strings.ToLower(value)] = value, true
	}
	if tok, err = d.Token(); err != nil || tok != json.Delim('}') || len(links) != expected {
		return nil, errConfig("archive link map count does not match coverage")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errConfig("archive link map has trailing content")
	}
	return links, nil
}

func archiveLines[T any](ctx context.Context, root *os.Root, name, expectedHash string, visit func(T) error) error {
	f, err := archiveOpen(root, name)
	if err != nil {
		return errConfig("archive graph or reconstruction evidence is missing or unsafe")
	}
	defer f.Close()
	h := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(f, h))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var row T
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return errConfig("invalid archive JSONL record")
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	if scanner.Err() != nil || expectedHash != "" && (!validDigest(expectedHash) || expectedHash != hex.EncodeToString(h.Sum(nil))) {
		return errConfig("archive evidence checksum differs or JSONL exceeds the row bound")
	}
	return nil
}

func rebuildDestination(source, output string) (string, error) {
	if output == "" {
		return "", errConfig("rebuild requires a new output directory")
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return "", errConfig("invalid rebuild destination")
	}
	// Resolve the nearest existing ancestor, including symlinked parents, before
	// allowing a destination. Never write inside or on top of the source archive.
	parent, suffix := abs, ""
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return "", errConfig("source archive cannot be inspected")
	}
	for {
		resolved, e := filepath.EvalSymlinks(parent)
		if e == nil {
			// SameFile also catches case-insensitive aliases and bind-mount paths
			// that a lexical relative-path comparison cannot identify.
			for ancestor := resolved; ; ancestor = filepath.Dir(ancestor) {
				info, e := os.Stat(ancestor)
				if e != nil {
					return "", errConfig("rebuild destination ancestry cannot be inspected")
				}
				if os.SameFile(sourceInfo, info) {
					return "", errConfig("rebuild destination must be outside the source archive")
				}
				if filepath.Dir(ancestor) == ancestor {
					break
				}
			}
			abs = filepath.Join(resolved, suffix)
			break
		}
		if !os.IsNotExist(e) || filepath.Dir(parent) == parent {
			return "", errConfig("rebuild destination parent cannot be inspected")
		}
		suffix = filepath.Join(filepath.Base(parent), suffix)
		parent = filepath.Dir(parent)
	}
	if relative, err := filepath.Rel(source, abs); err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return "", errConfig("rebuild destination must be outside the source archive")
	}
	for _, target := range []string{abs, abs + ".partial"} {
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			return "", errConfig("rebuild output or partial directory already exists or cannot be inspected")
		}
	}
	return abs, nil
}

func (r *run) restoreLegacyUsers(root *os.Root, byPath map[string]*Meta) error {
	const folder = "workstream_summaries/personas/users"
	if _, err := root.Lstat(folder); os.IsNotExist(err) {
		return nil
	}
	return fs.WalkDir(root.FS(), folder, func(name string, entry fs.DirEntry, err error) error {
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		if err != nil || entry.Type()&os.ModeSymlink != 0 {
			return errConfig("legacy user profile navigation is unsafe or unreadable")
		}
		if entry.IsDir() || path.Base(name) != "profile.md" {
			return nil
		}
		b, err := archiveRead(root, name, 16<<20)
		if err != nil {
			return err
		}
		rootNode := goldmark.DefaultParser().Parse(text.NewReader(b))
		return ast.Walk(rootNode, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if link, ok := node.(*ast.Link); ok && entering {
				resolved := resolveArchiveLink(name, string(link.Destination))
				if m := byPath[resolved]; m != nil && m.Type == "PERSONS" {
					r.userPersonIDs[m.ID] = true
				}
			}
			return ast.WalkContinue, nil
		})
	})
}
