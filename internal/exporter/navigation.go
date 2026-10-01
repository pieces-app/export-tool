package exporter

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Bound generated lists independently of record bodies. Very large Markdown
// indexes are difficult to navigate and can exhaust a detector's scan deadline.
// The complete title remains in its canonical document; lists use short labels.
const navigationPageEntries = 250
const navigationPageBytes = 64 << 10

type navigationEntry struct {
	label, path, detail string
}

func navigationLabel(label string) string {
	runes := []rune(label)
	if len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return label
}

func (entry navigationEntry) markdown(from string) string {
	line := fmt.Sprintf("- [%s](%s)", md(navigationLabel(entry.label)), relative(from, entry.path))
	if entry.detail != "" {
		line += " — " + md(entry.detail)
	}
	return line + "\n"
}

// Each page links back to its immediate index. Indexes of pages are themselves
// paginated if necessary, so millions of records cannot make an unbounded root.
// Small lists keep their established path and inline links.
func (r *run) writeNavigationIndex(path, preamble string, entries []navigationEntry) error {
	if r.ctx != nil {
		if err := r.ctx.Err(); err != nil {
			return err
		}
	}
	if len(preamble) > navigationPageBytes/2 {
		return errConfig("navigation heading exceeds page size bound")
	}
	var direct strings.Builder
	direct.WriteString(preamble)
	fits := len(entries) <= navigationPageEntries
	if fits {
		for _, entry := range entries {
			direct.WriteString(entry.markdown(path))
			if direct.Len() > navigationPageBytes {
				fits = false
				break
			}
		}
	}
	if fits {
		return r.writeFile(filepath.Join(r.stage, path), []byte(direct.String()))
	}
	direct.Reset()
	pages := []navigationEntry{}
	folder := strings.TrimSuffix(path, ".md") + ".pages"
	for start := 0; start < len(entries); {
		if r.ctx != nil {
			if err := r.ctx.Err(); err != nil {
				return err
			}
		}
		page := fmt.Sprintf("%s/page-%06d.md", folder, len(pages)+1)
		var b strings.Builder
		fmt.Fprintf(&b, "# Navigation page %d\n\n[Back to index](%s) · [Export index](%s)\n\n", len(pages)+1, relative(page, path), relative(page, "index.md"))
		end := start
		for end < len(entries) && end-start < navigationPageEntries {
			line := entries[end].markdown(page)
			if b.Len()+len(line) > navigationPageBytes {
				if end == start {
					return errConfig("navigation entry exceeds page size bound")
				}
				break
			}
			b.WriteString(line)
			end++
		}
		if err := r.writeFile(filepath.Join(r.stage, page), []byte(b.String())); err != nil {
			return err
		}
		pages = append(pages, navigationEntry{label: fmt.Sprintf("Entries %d–%d", start+1, end), path: page})
		start = end
	}
	// Use another directory level for a large page directory. It must not
	// overwrite leaf pages while recursively shortening its own index.
	pageListingBytes := len(preamble) + 256
	for _, page := range pages {
		pageListingBytes += len(page.markdown(path))
	}
	if len(pages) > navigationPageEntries || pageListingBytes > navigationPageBytes {
		directory := folder + "/index.md"
		if err := r.writeNavigationIndex(directory, "# Navigation pages\n\n[Back to index]("+relative(directory, path)+")\n\n", pages); err != nil {
			return err
		}
		pages = []navigationEntry{{label: fmt.Sprintf("Browse all %d entries", len(entries)), path: directory}}
	}
	var b strings.Builder
	b.WriteString(preamble)
	fmt.Fprintf(&b, "%d entries. Open a page to browse the complete list.\n\n", len(entries))
	for _, page := range pages {
		b.WriteString(page.markdown(path))
	}
	if b.Len() > navigationPageBytes {
		return errConfig("navigation index exceeds page size bound")
	}
	return r.writeFile(filepath.Join(r.stage, path), []byte(b.String()))
}
