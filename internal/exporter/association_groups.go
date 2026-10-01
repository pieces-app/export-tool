package exporter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const associationChunkRecords = 50
const associationChunkBytes = 7 << 20

type associationGroup struct {
	path, dataPath string
	members        []*Meta
	markdown       strings.Builder
}

// Selection and dependency filtering have finished. Apply the last reference
// pruning before publishing; every public row is compact JSON plus one LF.
// Chunks have at most 50 rows / 7 MiB, except one bounded oversized row alone.
// No key/database/checkpoint is placed in the shareable archive.
func (r *run) prepareAssociationGroups() error {
	if r.canonicalStage == nil || r.records != r.canonicalStage {
		return nil
	}
	if err := r.canonicalRecords().Flush(r.ctx); err != nil {
		return err
	}
	count := 0
	for _, m := range r.meta {
		if _, ok := associationFamilyByType(m.Type); ok && m.State == "included" {
			count++
		}
	}
	if count > 0 {
		r.progress.Stage("Group association evidence", count)
	}
	var data bytes.Buffer
	var members []*Meta
	var typ string
	number := 0
	flush := func() error {
		if len(members) == 0 {
			return nil
		}
		prefix := "data/"
		if r.opts.Mode == "preserve" {
			prefix = "raw/"
		}
		name := fmt.Sprintf("group-%06d", number)
		g := &associationGroup{path: "markdown/" + members[0].Folder + "/" + name + ".md", dataPath: prefix + members[0].Folder + "/" + name + ".jsonl", members: members}
		if err := r.writeFile(filepath.Join(r.stage, g.dataPath), data.Bytes()); err != nil {
			return err
		}
		for _, m := range members {
			m.Path, m.DataPath = g.path, g.dataPath
		}
		fmt.Fprintf(&g.markdown, "# Association evidence: %s\n\n%d records. Each record retains its identity and metadata in [canonical JSONL](%s).\n\n[Export index](%s)\n\n", md(typ), len(members), relative(g.path, g.dataPath), relative(g.path, "index.md"))
		r.associationGroups = append(r.associationGroups, g)
		r.manifest.FormatVersion = 6
		number++
		members, data = nil, bytes.Buffer{}
		return nil
	}
	for _, m := range r.sortedMeta() {
		if _, ok := associationFamilyByType(m.Type); !ok || m.State != "included" {
			continue
		}
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if m.Type != typ {
			if err := flush(); err != nil {
				return err
			}
			typ, number = m.Type, 0
		}
		v, err := r.readCanonical(m)
		if err != nil {
			return err
		}
		if r.opts.Mode == "filtered" {
			pruneReferences(v, r.meta)
		}
		row, err := json.Marshal(v)
		if err != nil {
			return err
		}
		row = append(row, '\n')
		if len(row) > canonicalPayloadLimit {
			return errConfig("association row exceeds its public storage bound")
		}
		if len(members) > 0 && (len(members) == associationChunkRecords || data.Len()+len(row) > associationChunkBytes) {
			if err := flush(); err != nil {
				return err
			}
		}
		m.DataOffset, m.DataLength = int64(data.Len()), int64(len(row))
		data.Write(row)
		members = append(members, m)
		r.progress.Add(1)
	}
	return flush()
}
