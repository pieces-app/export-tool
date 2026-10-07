package exporter

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Descriptions have already passed the archive's selection/privacy decisions.
// Show plain text rather than introduce additional links into navigation pages.
func compactSummaryDescription(root *os.Root, r *compactRecord, canonical map[string]any) error {
	description := fieldString(canonical, "description")
	name := sidecarPath(r.entry.Path)
	if _, err := root.Lstat(name); err == nil {
		b, err := archiveRead(root, name, 16<<20)
		var metadata DocumentMetadata
		if err != nil || json.Unmarshal(b, &metadata) != nil || metadata.Path != r.entry.Path || metadata.ID != r.entry.ID {
			return errConfig("invalid summary preview metadata")
		}
		description = metadata.Description
	} else if !os.IsNotExist(err) {
		return err
	}
	description = compactDisplayTitle(description)
	chars := []rune(description)
	if len(chars) > 360 {
		description = string(chars[:360])
		if at := strings.LastIndex(description, " "); at > len(description)*2/3 {
			description = description[:at]
		}
		description = strings.TrimSpace(description) + "…"
	}
	if description == "" {
		description = "Description unavailable."
	}
	r.description = description
	return nil
}

func compactActivityRange(from, to time.Time, zone *time.Location) string {
	from, to = from.In(zone), to.In(zone)
	_, a := from.Zone()
	_, b := to.Zone()
	if from.Format("2006-01-02") == to.Format("2006-01-02") && a == b {
		return from.Format("January 2, 2006 · 3:04 PM") + " – " + to.Format("3:04 PM MST (UTC-07:00)")
	}
	return from.Format("January 2, 2006 at 3:04 PM MST (UTC-07:00)") + " – " + to.Format("January 2, 2006 at 3:04 PM MST (UTC-07:00)")
}

func compactSummaryEntry(r *compactRecord, zone *time.Location) navigationEntry {
	if zone == nil {
		zone = time.UTC
	}
	created, _ := time.Parse(time.RFC3339Nano, r.entry.Created)
	if !created.IsZero() {
		created = created.In(zone)
	}
	e := navigationEntry{label: r.entry.Title, path: r.destination, preview: r.description, dateFrom: created, dateTo: created}
	if e.preview == "" {
		e.preview = "Description unavailable."
	}
	var from, to time.Time
	seen := map[string]bool{}
	valid, invalid, historical := 0, 0, false
	for _, edge := range r.edges {
		if edge.relation != "ranges" || edge.target.kind != "range" {
			continue
		}
		x := edge.target
		if seen[x.entry.ID] {
			continue
		}
		seen[x.entry.ID] = true
		if x.rangeFrom.IsZero() || x.rangeTo.IsZero() || x.rangeTo.Before(x.rangeFrom) {
			invalid++
			continue
		}
		valid++
		historical = historical || strings.Contains(edge.provenance, "historical_client_cache")
		if from.IsZero() || x.rangeFrom.Before(from) {
			from = x.rangeFrom
		}
		if to.IsZero() || x.rangeTo.After(to) {
			to = x.rangeTo
		}
	}
	if valid == 0 {
		e.detail = "Activity range unavailable."
		if !created.IsZero() {
			e.detail = "Created: " + displayTimestamp(r.entry.Created, zone) + " · Activity range unavailable."
		}
		return e
	}
	e.detail = "Activity: " + compactActivityRange(from, to, zone)
	if valid > 1 {
		e.detail = fmt.Sprintf("Activity span: %s (%d recorded ranges)", compactActivityRange(from, to, zone), valid)
	}
	if invalid > 0 {
		e.detail += "; additional range bounds unavailable"
	}
	if historical {
		e.detail += "; historical relationship evidence"
	}
	return e
}
