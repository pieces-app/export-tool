package exporter

import "time"

// Display dates separately from the exact source timestamps retained in JSON.
func displayTimestamp(value string, zone *time.Location) string {
	if value == "" {
		return "undated"
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	if zone == nil {
		zone = time.UTC
	}
	t = t.In(zone)
	label := t.Format("January 2, 2006 at 3:04:05 PM MST")
	if zone != time.UTC {
		label += t.Format(" (UTC-07:00)")
	}
	return label
}
