package exporter

import "strings"

// displayPrefix returns the first limit runes of s for a shortened label.
// Generated labels are scanned again by the final output audit, so a cut must
// not end inside a URL's host or query: a partial host can change a domain
// decision, and a partial query can turn an approved REDACTED value or a
// parameter name into a new credential value. A cut inside the host keeps the
// whole host; a cut inside the query or fragment drops both. truncated reports
// whether any text was removed.
func displayPrefix(s string, limit int) (prefix string, truncated bool) {
	cut, runes := len(s), 0
	for i := range s {
		if runes == limit {
			cut = i
			break
		}
		runes++
	}
	if cut == len(s) {
		return s, false
	}
	for _, at := range urlPattern.FindAllStringIndex(s, -1) {
		if at[0] < cut && cut < at[1] {
			cut = at[0] + urlSafeCut(s[at[0]:at[1]], cut-at[0])
			break
		}
	}
	if cut >= len(s) {
		return s, false
	}
	return s[:cut], true
}

// urlSafeCut moves a cut inside the URL text u so that the kept prefix has a
// complete host and no query or fragment. Cuts inside the path stay put.
func urlSafeCut(u string, cut int) int {
	host := strings.Index(u, "://") + len("://")
	path := len(u)
	if i := strings.IndexAny(u[host:], "/?#"); i >= 0 {
		path = host + i
	}
	if cut <= path {
		return path
	}
	if i := strings.IndexAny(u[path:], "?#"); i >= 0 && cut > path+i {
		return path + i
	}
	return cut
}
