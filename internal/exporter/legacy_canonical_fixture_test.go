package exporter

// Produce real per-record canonical files before rendering legacy fixtures.
// Merely changing the manifest version of a grouped archive is invalid.
func useLegacyCanonicalFiles(r *run) error {
	files := &fileCanonicalRecords{run: r}
	for _, m := range r.meta {
		if m.State != "included" {
			continue
		}
		v, err := r.readCanonical(m)
		if err != nil {
			return err
		}
		if err := files.Write(r.ctx, m, v, false); err != nil {
			return err
		}
	}
	r.records = files
	return nil
}
