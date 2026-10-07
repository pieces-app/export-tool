package exporter

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Bound generated lists independently of record bodies. Very large Markdown
// indexes are difficult to navigate and can exhaust a detector's scan deadline.
// The complete title remains in its canonical document; lists use short labels.
const navigationPageEntries = 250
const navigationPageBytes = 64 << 10

type navigationEntry struct {
	label, path, detail string
	preview             string
	dateFrom, dateTo    time.Time // Creation dates, used only by summary browsing pages.
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
	if entry.preview != "" {
		return line + "\n\n  " + md(entry.detail) + "\n\n  " + md(entry.preview) + "\n\n"
	}
	if entry.detail != "" {
		line += " — " + md(entry.detail)
	}
	return line + "\n"
}

func navigationDates(entries []navigationEntry) (from, to time.Time, label string) {
	for _, entry := range entries {
		if !entry.dateFrom.IsZero() && (from.IsZero() || entry.dateFrom.Before(from)) {
			from = entry.dateFrom
		}
		if !entry.dateTo.IsZero() && (to.IsZero() || entry.dateTo.After(to)) {
			to = entry.dateTo
		}
	}
	if from.IsZero() || to.IsZero() {
		return
	}
	label = "Summary dates: " + from.Format("January 2, 2006")
	if from.Format("2006-01-02") != to.Format("2006-01-02") {
		label += " – " + to.Format("January 2, 2006")
	}
	return
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
	// Include generated properties inside the page budget, rather than adding
	// them after pagination and letting a full page exceed its byte limit.
	pageProperties := ""
	if strings.HasPrefix(preamble, "---\n") {
		if end := strings.Index(preamble[4:], "\n---\n"); end >= 0 {
			pageProperties = preamble[:4+end+5] + "\n"
		}
	}
	var direct strings.Builder
	direct.WriteString(preamble)
	pageLimit, dateReserve := navigationPageEntries, 0
	for _, entry := range entries {
		if entry.preview != "" {
			pageLimit = 50
		}
		if !entry.dateFrom.IsZero() {
			dateReserve = 256
		}
	}
	fits := len(entries) <= pageLimit
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
		b.WriteString(pageProperties)
		fmt.Fprintf(&b, "# Navigation page %d\n\n[Back to index](%s) · [Export index](%s)\n\n", len(pages)+1, relative(page, path), relative(page, "index.md"))
		end := start
		for end < len(entries) && end-start < pageLimit {
			line := entries[end].markdown(page)
			if b.Len()+len(line)+dateReserve > navigationPageBytes {
				if end == start {
					return errConfig("navigation entry exceeds page size bound")
				}
				break
			}
			b.WriteString(line)
			end++
		}
		from, to, dates := navigationDates(entries[start:end])
		body := b.String()
		if dates != "" {
			at := len(pageProperties) + strings.Index(body[len(pageProperties):], "\n\n") + 2
			body = body[:at] + dates + "\n\n" + body[at:]
		}
		if err := r.writeFile(filepath.Join(r.stage, page), []byte(body)); err != nil {
			return err
		}
		pages = append(pages, navigationEntry{label: fmt.Sprintf("Entries %d–%d", start+1, end), path: page, detail: dates, dateFrom: from, dateTo: to})
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
		if err := r.writeNavigationIndex(directory, pageProperties+"# Navigation pages\n\n[Back to index]("+relative(directory, path)+")\n\n", pages); err != nil {
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
