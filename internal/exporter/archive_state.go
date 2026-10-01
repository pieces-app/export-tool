package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const archiveStateFile = "rebuild-state.jsonl"

type ArchiveState struct {
	Version       int    `json:"version"`
	StateSHA256   string `json:"state_sha256"`
	GraphSHA256   string `json:"graph_sha256"`
	LinkMapSHA256 string `json:"link_map_sha256"`
}

// Excluded/withheld identities remain opaque. Original body/projection evidence
// cannot be inferred from JSON after the renderer has pruned private references.
type archiveRecord struct {
	JunctionFields                map[string]bool   `json:"junction_fields,omitempty"`
	Ref                           string            `json:"record_ref"`
	Material                      string            `json:"material"`
	State                         string            `json:"state"`
	DataSHA256                    string            `json:"data_sha256,omitempty"`
	Redactions                    int               `json:"redactions,omitempty"`
	ProjectionStates              map[string]string `json:"projection_states,omitempty"`
	SupplementableFields          map[string]bool   `json:"supplementable_fields,omitempty"`
	RelationshipProjectionUnknown bool              `json:"relationship_projection_unknown,omitempty"`
	PersonProjection              bool              `json:"person_projection,omitempty"`
	PersonEvidence                *archivePerson    `json:"person_evidence,omitempty"`
	VerifiedUser                  bool              `json:"verified_user,omitempty"`
}

type archivePerson struct {
	UnknownAnnotations     bool `json:"unknown_annotations"`
	UnknownConnections     bool `json:"unknown_connections"`
	SourceEventConnections int  `json:"source_event_connections"`
}

func fileDigest(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return readerDigest(ctx, f)
}

func readerDigest(ctx context.Context, input io.Reader) (string, error) {
	h := sha256.New()
	b := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := input.Read(b)
		h.Write(b[:n])
		if err == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if err != nil {
			return "", err
		}
	}
}

func (r *run) writeArchiveState() (result error) {
	r.progress.Stage("Record archive reconstruction evidence", len(r.meta))
	started := time.Now()
	w := &countedWriter{}
	defer func() { r.local.record("artifact_write", time.Since(started), w.bytes, result) }()
	f, err := os.OpenFile(filepath.Join(r.stage, archiveStateFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	w.Writer = f
	enc := json.NewEncoder(io.MultiWriter(w, h))
	// Prior decisions have no IDs or bodies and cannot be restored by rebuilding.
	for _, decision := range r.priorDecisions {
		if err := enc.Encode(decision); err != nil {
			return err
		}
	}
	version := 1
	for _, m := range r.sortedMeta() {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if m.ArchivePlaceholder {
			continue
		}
		rec := archiveRecord{Ref: opaque(m.Type, m.ID), Material: m.Type, State: m.State}
		if m.State == "included" {
			rec.DataSHA256, err = fileDigest(r.ctx, filepath.Join(r.stage, m.DataPath))
			if err != nil {
				return err
			}
			rec.Redactions = m.Redactions
			rec.ProjectionStates = m.ProjectionStates
			rec.JunctionFields = m.JunctionFields
			if len(m.JunctionFields) > 0 {
				// Older rebuilders must refuse this archive instead of dropping
				// current-empty evidence and reviving historical cache links.
				version = 2
			}
			rec.SupplementableFields = m.SupplementableFields
			rec.RelationshipProjectionUnknown = m.RelationshipProjectionUnknown
			rec.PersonProjection = m.PersonProjection
			rec.VerifiedUser = r.userPersonIDs[m.ID] && m.Type == "PERSONS"
			if p := m.PersonEvidence; p != nil {
				rec.PersonEvidence = &archivePerson{p.UnknownAnnotations, p.UnknownConnections, p.SourceEventConnections}
			}
		}
		if err := enc.Encode(rec); err != nil {
			return err
		}
		r.progress.Add(1)
	}
	if err := r.local.syncFile(f); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	graph, err := fileDigest(r.ctx, filepath.Join(r.stage, "relationships.jsonl"))
	if err != nil {
		return err
	}
	links, err := fileDigest(r.ctx, filepath.Join(r.stage, "link-map.json"))
	if err != nil {
		return err
	}
	r.manifest.ArchiveState = &ArchiveState{Version: version, StateSHA256: hex.EncodeToString(h.Sum(nil)), GraphSHA256: graph, LinkMapSHA256: links}
	return nil
}
