package exporter

import (
	"fmt"
	"github.com/pieces-app/export-tool/internal/filemeta"
	"path/filepath"
	"sort"
)

func (r *run) applyMetadata() error {
	paths := []string{}
	for path := range r.documentMetadata {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	failures := 0
	for _, path := range paths {
		r.progress.Add(1)
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		meta := *r.documentMetadata[path]
		meta.Path = path
		if r.opts.Metadata == "auto" {
			meta.NativeStatus = filemeta.Write(filepath.Join(r.stage, path), meta.Title, meta.Description, meta.NativeTags)
			if meta.NativeStatus != "applied" {
				failures++
			}
		}
		if err := r.writeJSON(filepath.Join(r.stage, sidecarPath(path)), meta); err != nil {
			return err
		}
	}
	if failures > 0 {
		r.manifest.Warnings = append(r.manifest.Warnings, fmt.Sprintf("Native metadata was unavailable or failed readback on %d documents; portable metadata sidecars are included.", failures))
	}
	return nil
}
