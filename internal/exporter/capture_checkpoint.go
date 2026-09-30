package exporter

// This adapter checkpoints the completed source-read boundary. It is private
// until the CLI's workspace lifecycle and compatibility contract are wired.
// It cannot continue an interrupted source fetch.
// Private titles, excluded relationships and learned credentials are encrypted
// by recovery.Store, never copied into the shareable archive's evidence files.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/pieces-app/export-tool/internal/recovery"
)

const captureVersion = 1

type captureFrame struct {
	Version int
	Phase   string
	Parts   map[string]int
}

type captureCore struct {
	CapturedAt time.Time
	Options    Options
	Manifest   Manifest
	Policy     Policy
	PolicyHash string
	ListHashes map[string]string
	FontDigest string
}

type captureRecord struct {
	Meta Meta
	Data json.RawMessage `json:",omitempty"`
}

type captureEdge struct {
	Edge        Edge
	Derived     *bool
	Association *string
	Cache       *CacheEvidence
}

type capturePart struct {
	Kind    string
	Name    string
	Index   int
	Core    *captureCore   `json:",omitempty"`
	Record  *captureRecord `json:",omitempty"`
	Strings []string       `json:",omitempty"`
	Secrets [][]byte       `json:",omitempty"`
	Edges   []captureEdge  `json:",omitempty"`
	Issues  []Issue        `json:",omitempty"`
	Flag    *bool          `json:",omitempty"`
}

func captureRef(p capturePart) [32]byte {
	return sha256.Sum256([]byte("capture-v1\x00" + p.Kind + "\x00" + p.Name + "\x00" + strconv.Itoa(p.Index)))
}

func decodeCapture(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return errConfig("invalid encrypted export checkpoint")
	}
	if d.Decode(new(any)) != io.EOF {
		return errConfig("export checkpoint has trailing data")
	}
	return nil
}

func captureFontDigest(ctx context.Context, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := readPDFBytes(ctx, path, 32<<20, errConfig("checkpoint PDF font exceeds its bound"))
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errConfig("checkpoint PDF font exceeds its bound or is unreadable")
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// saveCapture requires a fresh store and the boundary immediately before local
// privacy reconciliation. Partial saves retain a writing marker and cannot be
// resumed. The final state-only commit authenticates the complete item counts.
func (r *run) saveCapture(s *recovery.Store) error {
	if r.rebuilding || len(r.priorDecisions) != 0 || len(r.people) != 0 || !r.manifest.Finished.IsZero() {
		return errConfig("export is not at the source-capture boundary")
	}
	cp, err := s.Snapshot(r.ctx)
	if err != nil {
		return err
	}
	if cp.Generation != 0 || cp.Records != 0 {
		return errConfig("capture checkpoint requires a fresh recovery store")
	}
	r.progress.Stage("Checkpoint source capture", len(r.meta))
	frame := captureFrame{Version: captureVersion, Phase: "writing", Parts: map[string]int{}}
	var pending []recovery.Record
	pendingBytes := 0
	generation := cp.Generation
	flush := func() error {
		state, err := json.Marshal(frame)
		if err != nil {
			return errConfig("could not encode export checkpoint state")
		}
		if err := s.Commit(r.ctx, generation, pending, state); err != nil {
			return err
		}
		generation++
		for _, item := range pending {
			clear(item.Payload)
		}
		pending, pendingBytes = nil, 0
		return nil
	}
	defer func() {
		for _, item := range pending {
			clear(item.Payload)
		}
	}()
	add := func(p capturePart) error {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		b, err := json.Marshal(p)
		if err != nil || len(b) > (128<<20)-(64<<10) {
			return errConfig("export checkpoint item exceeds its encoding bound")
		}
		if len(pending) > 0 && (len(pending) >= 50 || pendingBytes+len(b) > 7<<20) {
			if err := flush(); err != nil {
				clear(b)
				return err
			}
		}
		pending = append(pending, recovery.Record{Ref: captureRef(p), Payload: b})
		pendingBytes += len(b)
		frame.Parts[p.Kind]++
		return nil
	}
	core := captureCore{CapturedAt: time.Now().UTC(), Options: r.opts, Manifest: r.manifest, Policy: r.opts.Scanner.Policy, PolicyHash: r.opts.Scanner.Hash, ListHashes: r.opts.Scanner.ListHashes}
	core.Options.Scanner, core.Options.Progress, core.Options.captureCheckpoint = nil, nil, nil
	core.Options.Recovery = nil
	core.Manifest.Issues = nil
	if r.client != nil {
		core.Manifest.Performance = r.client.Performance()
	}
	if core.FontDigest, err = captureFontDigest(r.ctx, core.Options.PDFFont); err != nil {
		return err
	}
	if err := add(capturePart{Kind: "core", Core: &core}); err != nil {
		return err
	}
	root, err := os.OpenRoot(r.stage)
	if err != nil {
		return errConfig("capture staging directory is unavailable")
	}
	defer root.Close()
	for _, m := range r.sortedMeta() {
		row := captureRecord{Meta: *m}
		if m.State == "included" {
			row.Data, err = archiveRead(root, m.DataPath, 128<<20)
			if err != nil {
				return err
			}
		}
		if err := add(capturePart{Kind: "record", Name: m.Key, Record: &row}); err != nil {
			return err
		}
		r.progress.Add(1)
	}
	stringsParts := func(kind, name string, values []string) error {
		for start, index := 0, 0; start < len(values) || index == 0; index++ {
			end := min(start+128, len(values))
			if err := add(capturePart{Kind: kind, Name: name, Index: index, Strings: values[start:end]}); err != nil {
				return err
			}
			start = end
		}
		return nil
	}
	for typ, ids := range r.inventory {
		if err := stringsParts("inventory", typ, ids); err != nil {
			return err
		}
	}
	for i, domains := range r.opts.Scanner.domains {
		if err := stringsParts("domains", strconv.Itoa(i), domains); err != nil {
			return err
		}
	}
	// Individual credentials may contain arbitrary bytes. JSON strings would
	// replace invalid UTF-8 and could silently change the replay matcher.
	var secrets [][]byte
	secretBytes, secretIndex := 0, 0
	for value := range r.opts.Scanner.known {
		if len(secrets) > 0 && (len(secrets) >= 128 || secretBytes+len(value) > 1<<20) {
			if err := add(capturePart{Kind: "secrets", Index: secretIndex, Secrets: secrets}); err != nil {
				return err
			}
			secrets, secretBytes, secretIndex = nil, 0, secretIndex+1
		}
		secrets = append(secrets, []byte(value))
		secretBytes += len(value)
	}
	if len(secrets) > 0 {
		if err := add(capturePart{Kind: "secrets", Index: secretIndex, Secrets: secrets}); err != nil {
			return err
		}
	}
	for start := 0; start < len(r.manifest.Issues); start += 128 {
		if err := add(capturePart{Kind: "issues", Index: start / 128, Issues: r.manifest.Issues[start:min(start+128, len(r.manifest.Issues))]}); err != nil {
			return err
		}
	}
	for id, flag := range r.userPersonIDs {
		if err := add(capturePart{Kind: "user", Name: id, Flag: &flag}); err != nil {
			return err
		}
	}
	var edges []captureEdge
	edgeIndex := 0
	addEdge := func(edge captureEdge) error {
		edges = append(edges, edge)
		if len(edges) == 128 {
			if err := add(capturePart{Kind: "edges", Index: edgeIndex, Edges: edges}); err != nil {
				return err
			}
			edges, edgeIndex = nil, edgeIndex+1
		}
		return nil
	}
	for edge, flag := range r.derivedEdges {
		if err := addEdge(captureEdge{Edge: edge, Derived: &flag}); err != nil {
			return err
		}
	}
	for edge, proof := range r.associationEdges {
		if err := addEdge(captureEdge{Edge: edge, Association: &proof}); err != nil {
			return err
		}
	}
	for edge, proof := range r.cachedEdges {
		if err := addEdge(captureEdge{Edge: edge, Cache: &proof}); err != nil {
			return err
		}
	}
	if len(edges) > 0 {
		if err := add(capturePart{Kind: "edges", Index: edgeIndex, Edges: edges}); err != nil {
			return err
		}
	}
	if err := flush(); err != nil {
		return err
	}
	frame.Phase = "capture-complete"
	return flush()
}

func capturePolicyHash(p Policy, hashes map[string]string) string {
	b, _ := json.Marshal(struct {
		Policy Policy
		Lists  map[string]string
	}{p, hashes})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func restoreCaptureScanner(core captureCore, domains [][]string, secrets map[string]bool) (*Scanner, error) {
	if len(domains) != len(core.Policy.Lists) || capturePolicyHash(core.Policy, core.ListHashes) != core.PolicyHash {
		return nil, errConfig("checkpoint privacy configuration is inconsistent")
	}
	policy := core.Policy
	policy.Lists = nil
	s, err := NewScanner(policy, "") // Never reload changed/missing category files.
	if err != nil {
		return nil, err
	}
	s.Policy, s.Hash, s.ListHashes = core.Policy, core.PolicyHash, core.ListHashes
	s.domains, s.known = domains, secrets
	return s, nil
}

// loadCapture validates every semantic record before creating output files.
// Canonical bodies are validated one at a time and not retained in memory.
func loadCapture(ctx context.Context, s *recovery.Store) (*run, uint64, error) {
	cp, err := s.Snapshot(ctx)
	if err != nil {
		return nil, 0, err
	}
	var frame captureFrame
	if err := decodeCapture(cp.State, &frame); err != nil || frame.Version != captureVersion || frame.Phase != "capture-complete" || frame.Parts["core"] != 1 {
		return nil, 0, errConfig("recovery checkpoint does not contain a complete source capture")
	}
	r := &run{ctx: ctx, meta: map[string]*Meta{}, coverage: map[string]*Coverage{}, inventory: map[string][]string{}, userPersonIDs: map[string]bool{}, derivedEdges: map[Edge]bool{}, associationEdges: map[Edge]string{}, cachedEdges: map[Edge]CacheEvidence{}}
	var core *captureCore
	counts := map[string]int{}
	chunks := map[string]map[string]map[int][]string{}
	issues := map[int][]Issue{}
	indices := map[string]map[int]bool{}
	secrets := map[string]bool{}
	err = s.Visit(ctx, func(item recovery.Record) error {
		var p capturePart
		if err := decodeCapture(item.Payload, &p); err != nil || p.Index < 0 || uint64(p.Index) >= cp.Records || captureRef(p) != item.Ref {
			return errConfig("recovery checkpoint item identity is invalid")
		}
		want := capturePart{Kind: p.Kind, Name: p.Name, Index: p.Index}
		counts[p.Kind]++
		switch p.Kind {
		case "core":
			if core != nil || p.Core == nil || p.Name != "" || p.Index != 0 {
				return errConfig("recovery checkpoint core is invalid")
			}
			core, want.Core = p.Core, p.Core
		case "record":
			if p.Record == nil || p.Index != 0 || p.Name != p.Record.Meta.Key || r.meta[p.Name] != nil {
				return errConfig("recovery record metadata is invalid")
			}
			m := p.Record.Meta // Do not retain the sibling body allocation.
			if m.State == "included" {
				var v map[string]any
				if err := decodeArchiveJSON(p.Record.Data, &v); err != nil || fieldString(v, "id") != m.ID {
					return errConfig("recovery canonical record identity is invalid")
				}
			} else if (m.State != "excluded" && m.State != "withheld" && m.State != "missing") || len(p.Record.Data) != 0 {
				return errConfig("recovery record inclusion decision is invalid")
			}
			r.meta[m.Key], want.Record = &m, p.Record
		case "inventory", "domains":
			if len(p.Strings) > 128 {
				return errConfig("recovery string fragment exceeds its bound")
			}
			if chunks[p.Kind] == nil {
				chunks[p.Kind] = map[string]map[int][]string{}
			}
			if chunks[p.Kind][p.Name] == nil {
				chunks[p.Kind][p.Name] = map[int][]string{}
			}
			chunks[p.Kind][p.Name][p.Index], want.Strings = p.Strings, p.Strings
		case "secrets":
			if p.Name != "" || len(p.Secrets) == 0 || len(p.Secrets) > 128 {
				return errConfig("recovery scanner fragment is invalid")
			}
			for _, value := range p.Secrets {
				if len(value) < 4 || len(value) > 2<<20 || bytes.Contains(value, []byte("[REDACTED:")) || secrets[string(value)] {
					return errConfig("recovery credential matcher is invalid")
				}
				secrets[string(value)] = true
			}
			want.Secrets = p.Secrets
		case "issues":
			if p.Name != "" || len(p.Issues) == 0 || len(p.Issues) > 128 {
				return errConfig("recovery issue fragment is invalid")
			}
			issues[p.Index], want.Issues = p.Issues, p.Issues
		case "user":
			if p.Name == "" || p.Index != 0 || p.Flag == nil {
				return errConfig("recovery account mapping is invalid")
			}
			r.userPersonIDs[p.Name], want.Flag = *p.Flag, p.Flag
		case "edges":
			if p.Name != "" || len(p.Edges) == 0 || len(p.Edges) > 128 {
				return errConfig("recovery relationship fragment is invalid")
			}
			for _, e := range p.Edges {
				n := 0
				if e.Derived != nil {
					r.derivedEdges[e.Edge], n = *e.Derived, n+1
				}
				if e.Association != nil {
					r.associationEdges[e.Edge], n = *e.Association, n+1
				}
				if e.Cache != nil {
					r.cachedEdges[e.Edge], n = *e.Cache, n+1
				}
				if n != 1 {
					return errConfig("recovery relationship provenance is invalid")
				}
			}
			want.Edges = p.Edges
		default:
			return errConfig("unsupported recovery checkpoint item")
		}
		if !reflect.DeepEqual(p, want) {
			return errConfig("unexpected fields in recovery checkpoint item")
		}
		if p.Kind == "secrets" || p.Kind == "edges" || p.Kind == "issues" {
			if indices[p.Kind] == nil {
				indices[p.Kind] = map[int]bool{}
			}
			indices[p.Kind][p.Index] = true
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if core == nil || !reflect.DeepEqual(counts, frame.Parts) {
		return nil, 0, errConfig("recovery checkpoint item inventory is inconsistent")
	}
	for _, set := range indices {
		for i := range len(set) {
			if !set[i] {
				return nil, 0, errConfig("recovery checkpoint fragments are incomplete")
			}
		}
	}
	join := func(parts map[int][]string) ([]string, error) {
		all := []string{}
		for i := range len(parts) {
			values, ok := parts[i]
			if !ok || i < len(parts)-1 && len(values) != 128 {
				return nil, errConfig("recovery string fragments are incomplete")
			}
			all = append(all, values...)
		}
		return all, nil
	}
	for typ, parts := range chunks["inventory"] {
		if _, ok := materialByType(typ); !ok {
			return nil, 0, errConfig("recovery inventory material is unsupported")
		}
		if r.inventory[typ], err = join(parts); err != nil {
			return nil, 0, err
		}
	}
	domains := make([][]string, len(core.Policy.Lists))
	if len(chunks["domains"]) != len(domains) {
		return nil, 0, errConfig("recovery domain-list inventory is incomplete")
	}
	for i := range domains {
		parts, ok := chunks["domains"][strconv.Itoa(i)]
		if !ok {
			return nil, 0, errConfig("recovery domain list is missing")
		}
		domains[i], err = join(parts)
		if err != nil || !sort.StringsAreSorted(domains[i]) {
			return nil, 0, errConfig("recovery domain list is invalid")
		}
	}
	if core.CapturedAt.IsZero() || core.Options.Scanner != nil || core.Options.Progress != nil || core.Options.Recovery != nil || core.Manifest.Status != "running" || !core.Manifest.Finished.IsZero() || core.Manifest.ArchiveState != nil || core.Manifest.Rebuild != nil || core.Manifest.CaptureReplay != nil {
		return nil, 0, errConfig("recovery checkpoint is not an unprocessed source capture")
	}
	r.opts, r.manifest = core.Options, core.Manifest
	r.manifest.CaptureReplay = &CaptureReplayInfo{CapturedAt: core.CapturedAt, SourceToolVersion: core.Manifest.ToolVersion, SourcePerformance: core.Manifest.Performance}
	r.manifest.Performance = PerformanceStats{}
	r.opts.Scanner, err = restoreCaptureScanner(*core, domains, secrets)
	if err != nil {
		return nil, 0, err
	}
	if err := validateCaptureOptions(r.opts); err != nil {
		return nil, 0, err
	}
	if digest, err := captureFontDigest(ctx, r.opts.PDFFont); err != nil || digest != core.FontDigest {
		return nil, 0, errConfig("checkpoint PDF font changed or is unavailable")
	}
	for i := range len(issues) {
		r.manifest.Issues = append(r.manifest.Issues, issues[i]...)
	}
	for _, cov := range r.manifest.Coverage {
		if cov == nil || r.coverage[cov.Material] != nil || cov.Included != 0 || cov.Excluded != 0 || cov.Withheld != 0 || cov.Omitted != 0 {
			return nil, 0, errConfig("recovery coverage is not at the capture boundary")
		}
		r.coverage[cov.Material] = cov
	}
	prefix := "data/"
	if r.opts.Mode == "preserve" {
		prefix = "raw/"
	} else if r.opts.Mode != "filtered" {
		return nil, 0, errConfig("recovery privacy mode is invalid")
	}
	for _, m := range r.meta {
		material, ok := materialByType(m.Type)
		name := opaque(m.Type, m.ID)
		if !ok || m.ID == "" || m.Key != m.Type+"\x00"+m.ID || r.coverage[m.Type] == nil || m.ArchivePlaceholder {
			return nil, 0, errConfig("recovery record paths or material identity are invalid")
		}
		if m.State == "missing" {
			// Inverse relationship evidence can point from a failed read, but
			// it does not provide a body, title, or path for that identity.
			if !reflect.DeepEqual(*m, Meta{Key: m.Key, ID: m.ID, Type: m.Type, State: "missing", Edges: m.Edges}) {
				return nil, 0, errConfig("unavailable recovery record contains unexpected evidence")
			}
		} else if m.Folder != material.Folder || m.DataPath != prefix+material.Folder+"/"+name+".json" || m.Path != "markdown/"+material.Folder+"/"+name+".md" {
			return nil, 0, errConfig("recovery record paths or material identity are invalid")
		}
	}
	current, err := s.Snapshot(ctx)
	if err != nil || current.Generation != cp.Generation {
		return nil, 0, errConfig("recovery checkpoint changed during validation")
	}
	return r, cp.Generation, nil
}

// A capture contains normalized settings. Do not silently apply new defaults or
// consult the old SDK caches/category files during local replay.
func validateCaptureOptions(o Options) error {
	for _, check := range []func() error{
		func() error { return ValidateFileWorkers(o.FileWorkers) },
		func() error { return ValidateAssociations(o.Associations) },
		o.SignalDigest.Validate, o.PDFLimits.Validate,
		func() error { return validateScope(o) },
	} {
		if err := check(); err != nil {
			return errConfig("recovery export settings are invalid")
		}
	}
	for _, m := range o.Materials {
		expected, ok := materialByType(m.Type)
		if !ok || m != expected {
			return errConfig("recovery material schema is incompatible")
		}
	}
	if (o.PeopleMode != "all" && o.PeopleMode != "profiles" && o.PeopleMode != "connected") || o.MinPersonConnections < 1 || (o.RelatedOrder != "relevance" && o.RelatedOrder != "recent") || o.RelatedLimit < 1 || o.RelatedLimit > 500 || o.BatchSize < 1 || o.BatchSize > 50 || o.WindowIDs < 1 {
		return errConfig("recovery selection settings are invalid")
	}
	if (o.Format != "markdown" && o.Format != "pdf" && o.Format != "both") || (o.Naming != "readable" && o.Naming != "opaque") || (o.Metadata != "off" && o.Metadata != "auto") || (o.Relationships != "both" && o.Relationships != "inline" && o.Relationships != "sidecar") {
		return errConfig("recovery presentation settings are invalid")
	}
	if _, err := time.LoadLocation(o.Timezone); err != nil {
		return errConfig("recovery timezone is unavailable")
	}
	return nil
}

// replayCapture redoes local processing into an exclusive new destination. It
// never contacts OS or trusts files left in an interrupted output directory.
func replayCapture(ctx context.Context, s *recovery.Store, output string, progress io.Writer) (Manifest, error) {
	r, generation, err := loadCapture(ctx, s)
	if err != nil {
		return Manifest{}, err
	}
	return replayLoadedCapture(r, s, generation, output, progress)
}

func replayLoadedCapture(r *run, s *recovery.Store, generation uint64, output string, progress io.Writer) (Manifest, error) {
	ctx := r.ctx
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return Manifest{}, err
	}
	for _, name := range []string{abs, abs + ".partial"} {
		if _, err := os.Lstat(name); !os.IsNotExist(err) {
			return Manifest{}, errConfig("replay output or staging directory already exists")
		}
	}
	r.stage, r.opts.Output, r.opts.Progress = abs+".partial", abs, progress
	r.manifest.CaptureReplay.ResumedAt = time.Now().UTC()
	if err := os.MkdirAll(filepath.Dir(r.stage), 0700); err != nil {
		return Manifest{}, err
	}
	if err := os.Mkdir(r.stage, 0700); err != nil {
		return Manifest{}, err
	}
	r.local = newLocalMeasurements()
	r.progress = startMeasuredProgress(progress, nil, r.local)
	defer r.progress.Close()
	defer r.closeMeasurements()
	if r.progress != nil {
		r.opts.Progress = r.progress.out
	}
	r.progress.Stage("Restore captured records", len(r.meta))
	err = s.Visit(ctx, func(item recovery.Record) error {
		var p capturePart
		if err := decodeCapture(item.Payload, &p); err != nil {
			return err
		}
		if p.Kind != "record" {
			return nil
		}
		if p.Record == nil || p.Record.Meta.State != "included" {
			r.progress.Add(1)
			return nil
		}
		m := r.meta[p.Name]
		if m == nil || !reflect.DeepEqual(m, &p.Record.Meta) || captureRef(p) != item.Ref {
			return errConfig("capture record changed during restoration")
		}
		var v map[string]any
		if err := decodeArchiveJSON(p.Record.Data, &v); err != nil {
			return errConfig("captured canonical record is invalid")
		}
		if err := r.writeJSON(filepath.Join(r.stage, m.DataPath), v); err != nil {
			return err
		}
		r.progress.Add(1)
		return nil
	})
	if err != nil {
		return r.manifest, err
	}
	cp, err := s.Snapshot(ctx)
	if err != nil || cp.Generation != generation {
		return r.manifest, errConfig("recovery checkpoint changed during restoration")
	}
	return r.processCaptured(abs)
}
