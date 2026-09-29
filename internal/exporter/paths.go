package exporter

import (
	"fmt"
	"golang.org/x/text/unicode/norm"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var uuidName = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func safeTitle(value string, maxBytes int) string {
	value = norm.NFC.String(value)
	var b strings.Builder
	separator := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			separator = false
		} else if !separator && b.Len() > 0 {
			b.WriteByte('_')
			separator = true
		}
	}
	result := strings.Trim(b.String(), "_. ")
	if len(result) > maxBytes {
		result = result[:maxBytes]
		for !utf8.ValidString(result) {
			result = result[:len(result)-1]
		}
		result = strings.TrimRight(result, "_ .")
	}
	if result == "" {
		return "untitled"
	}
	upper := strings.ToUpper(result)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || windowsDeviceName.MatchString(upper) {
		return "_" + result
	}
	return result
}

var windowsDeviceName = regexp.MustCompile(`^(?:COM|LPT)[1-9¹²³]$`)

func newer(a, b *Meta) bool {
	ta, ea := time.Parse(time.RFC3339Nano, a.Created)
	tb, eb := time.Parse(time.RFC3339Nano, b.Created)
	if (ea == nil) != (eb == nil) {
		return ea == nil
	}
	if ea == nil && !ta.Equal(tb) {
		return ta.After(tb)
	}
	return a.ID < b.ID
}

func descriptorKey(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return strings.ToLower(safeTitle(value, 64))
}

// Built-ins from the SDK WorkstreamSummaryGenerationType enum. Exact keys are
// required: arbitrary custom descriptors that sanitize alike are not aliases.
var builtInDescriptors = map[string]struct{ folder, label string }{
	"standup":                {"daily_standups", "Daily standups"},
	"morning_brief":          {"morning_briefs", "Morning briefs"},
	"end_of_day_recap":       {"end_of_day_recaps", "End of day recaps"},
	"week_recap":             {"week_recaps", "Week recaps"},
	"todays_headlines":       {"todays_headlines", "Today's headlines"},
	"time_tracker":           {"time_tracker", "Time tracker"},
	"collaboration_patterns": {"collaboration_patterns", "Collaboration patterns"},
	"ai_habits":              {"ai_habits", "AI habits"},
	"top_of_mind":            {"top_of_mind", "Top of mind"},
	"meeting_prep":           {"meeting_prep", "Meeting prep"},
	"custom_summary":         {"custom_summaries", "Custom summaries"},
	"temporal":               {"temporal", "Temporal single-click summaries"},
	"persona":                {"persona", "Persona pipeline outputs (ownership unverified)"},
}

func (r *run) summaryDescriptorFolder(m *Meta) string {
	key := descriptorKey(m.SummaryDescriptor)
	if key == "" {
		return "unclassified"
	}
	if builtIn, ok := builtInDescriptors[m.SummaryDescriptor]; ok {
		return builtIn.folder
	}
	if r.opts.Naming == "opaque" {
		return "pipeline." + opaque("SUMMARY_DESCRIPTOR", m.SummaryDescriptor)[:16]
	}
	if pipeline := r.descriptorPipeline(m); pipeline != nil {
		key = strings.ToLower(safeTitle(pipeline.Title, 64))
	}
	// Descriptors are structured client keys, but can be custom. Preserve their
	// sanitized readability and add a stable suffix so equivalent folder labels
	// never merge different pipeline definitions.
	return key + "." + opaque("SUMMARY_DESCRIPTOR", m.SummaryDescriptor)[:12]
}

// This lookup supplies an approved display label, not an association edge.
// The server constructs this exact descriptor from the pipeline record ID.
func (r *run) descriptorPipeline(m *Meta) *Meta {
	id, ok := strings.CutPrefix(m.SummaryDescriptor, "custom_pipeline_")
	if !ok || id == "" {
		return nil
	}
	pipeline := r.meta["PIPELINES\x00"+id]
	if pipeline == nil || pipeline.State != "included" || strings.TrimSpace(pipeline.Title) == "" {
		return nil
	}
	return pipeline
}

func (r *run) summaryDescriptorLabel(m *Meta) string {
	if descriptorKey(m.SummaryDescriptor) == "" {
		return "Unclassified single-click summaries"
	}
	if builtIn, ok := builtInDescriptors[m.SummaryDescriptor]; ok {
		return builtIn.label
	}
	if pipeline := r.descriptorPipeline(m); pipeline != nil {
		return pipeline.Title
	}
	return m.SummaryDescriptor
}

func (r *run) summaryFolder(m *Meta) string {
	kind := m.SummaryKind
	switch {
	case kind == "SPECIFIC_HIERARCHICAL_SUMMARY":
		return workstreamRoot + "/single_click_summaries/" + r.summaryDescriptorFolder(m)
	case strings.HasPrefix(kind, "TEMPORAL_") || kind == "" || kind == "UNKNOWN":
		// The OS uses UNKNOWN as its enum default. It remains in the chronological
		// timeline and carries its original enum in JSON/Markdown; no title-based
		// claim of automatic generation is made.
		return workstreamRoot + "/timeline"
	default:
		return workstreamRoot + "/hierarchical_summaries/" + safeTitle(strings.ToLower(kind), 64)
	}
}

func (r *run) assignPaths() {
	summaries := []*Meta{}
	for _, m := range r.meta {
		if m.State == "included" && m.Type == "WORKSTREAM_SUMMARIES" {
			summaries = append(summaries, m)
		}
	}
	sort.Slice(summaries, func(i, j int) bool { return newer(summaries[i], summaries[j]) })
	width := max(6, len(fmt.Sprint(max(0, len(summaries)-1))))
	zone, _ := time.LoadLocation(r.opts.Timezone)
	for i, m := range summaries {
		folder := r.summaryFolder(m)
		if r.opts.Naming == "opaque" {
			m.Path = folder + "/" + opaque(m.Type, m.ID) + ".md"
			continue
		}
		date := "undated"
		if t, err := time.Parse(time.RFC3339Nano, m.Created); err == nil {
			date = t.In(zone).Format("2006-01-02")
		}
		id := m.ID
		if !uuidName.MatchString(id) {
			id = opaque(m.Type, id)
		}
		m.Path = fmt.Sprintf("%s/%0*d.%s.%s.%s.md", folder, width, i, safeTitle(m.Title, 90), date, id)
	}
	r.assignPersonaHistoryPaths()
}
func relationshipPath(path string) string {
	return strings.TrimSuffix(path, ".md") + ".relationships_graph.md"
}
func sidecarPath(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ".metadata.json"
}
func uriPath(path string) string { return (&url.URL{Path: path}).EscapedPath() }
