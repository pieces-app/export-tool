package exporter

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/logging"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

type DomainRule struct {
	Domain     string `json:"domain"`
	Subdomains bool   `json:"include_subdomains"`
}
type DomainList struct {
	Path     string `json:"path"`
	Category string `json:"category"`
}
type Policy struct {
	Version       int          `json:"version"`
	Financial     bool         `json:"redact_financial"`
	Emails        bool         `json:"redact_emails"`
	SourceMode    string       `json:"source_mode"`
	Deny          []DomainRule `json:"deny"`
	Allow         []DomainRule `json:"allow"`
	Lists         []DomainList `json:"domain_lists"`
	StrictDerived bool         `json:"withhold_unproven_generated_content"`
}

func DefaultPolicy() Policy {
	return Policy{Version: 1, Financial: true, SourceMode: "denylist", Deny: []DomainRule{}, Allow: []DomainRule{}, Lists: []DomainList{}}
}

type Scanner struct {
	Policy     Policy
	detector   *detect.Detector
	domains    [][]string
	Hash       string
	ListHashes map[string]string
	known      map[string]bool
}

var quietOnce sync.Once

// Auditing never discovers credentials or changes policy. Workers share these
// immutable inputs, but each owns detector state. Build from the configured
// rules, not NewDetectorDefaultConfig (which uses process-global Viper state).
// Scanner mutation and traversal are sequential phases of a run.
func (s *Scanner) forkForAudit() *Scanner {
	fork := *s
	d := detect.NewDetector(s.detector.Config)
	d.IgnoreGitleaksAllow = s.detector.IgnoreGitleaksAllow
	d.MaxDecodeDepth = s.detector.MaxDecodeDepth
	d.MaxTargetMegaBytes = s.detector.MaxTargetMegaBytes
	d.Redact = s.detector.Redact
	d.Verbose = false
	fork.detector = d
	return &fork
}

func NewScanner(p Policy, baseDir string) (*Scanner, error) {
	if p.Version != 1 {
		return nil, errConfig("unsupported policy version")
	}
	if p.SourceMode != "denylist" && p.SourceMode != "allow_only" {
		return nil, errConfig("source_mode must be denylist or allow_only")
	}
	if p.SourceMode == "allow_only" && len(p.Allow) == 0 {
		return nil, errConfig("allow_only policy needs at least one explicit allow rule")
	}
	for _, rules := range [][]DomainRule{p.Deny, p.Allow} {
		for i := range rules {
			h, err := normalizeHost(rules[i].Domain)
			if err != nil {
				return nil, errConfig("invalid domain rule")
			}
			suffix, _ := publicsuffix.PublicSuffix(h)
			if h == suffix {
				return nil, errConfig("domain rules must not target a public suffix")
			}
			rules[i].Domain = h
		}
	}
	quietOnce.Do(func() { logging.Logger = zerolog.Nop() })
	d, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, errConfig("cannot initialize embedded secret detector")
	}
	d.IgnoreGitleaksAllow = true
	d.MaxDecodeDepth = 2
	d.Verbose = false
	s := &Scanner{Policy: p, detector: d, ListHashes: map[string]string{}, known: map[string]bool{}}
	for i, list := range p.Lists {
		path := list.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		entries, hash, err := loadDomains(path)
		if err != nil {
			return nil, fmt.Errorf("domain list %d: %w", i+1, err)
		}
		s.domains = append(s.domains, entries)
		s.ListHashes[fmt.Sprintf("list_%d", i+1)] = hash
	}
	b, _ := json.Marshal(struct {
		Policy Policy
		Lists  map[string]string
	}{p, s.ListHashes})
	h := sha256.Sum256(b)
	s.Hash = hex.EncodeToString(h[:])
	return s, nil
}

func LoadPolicy(path string) (Policy, string, error) {
	p := DefaultPolicy()
	if path == "" {
		return p, ".", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return p, "", errConfig("cannot read policy file")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return p, "", errConfig("policy file is unreadable or exceeds 1 MiB")
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, "", errConfig("invalid policy JSON or unknown policy field")
	}
	if d.Decode(new(any)) != io.EOF {
		return p, "", errConfig("trailing policy data")
	}
	return p, filepath.Dir(path), nil
}

func normalizeHost(s string) (string, error) {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	if s == "" || strings.ContainsAny(s, "/:@?# \\*") {
		return "", errConfig("invalid hostname")
	}
	h, err := idna.Lookup.ToASCII(s)
	if err != nil || len(h) > 253 {
		return "", errConfig("invalid hostname")
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errConfig("invalid hostname")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", errConfig("invalid hostname")
			}
		}
	}
	return h, nil
}
func loadDomains(path string) ([]string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", errConfig("cannot open required domain list")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() > 256<<20 {
		return nil, "", errConfig("domain list exceeds 256 MiB")
	}
	h := sha256.New()
	scan := bufio.NewScanner(io.TeeReader(f, h))
	scan.Buffer(make([]byte, 4096), 64<<10)
	entries := []string{}
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domain, err := normalizeHost(line)
		if err != nil {
			return nil, "", errConfig("domain list must contain one hostname per line, not URLs or hosts-file entries")
		}
		entries = append(entries, domain)
		if len(entries) > 6000000 {
			return nil, "", errConfig("domain list has too many entries")
		}
	}
	if scan.Err() != nil {
		return nil, "", errConfig("domain list could not be fully read")
	}
	if len(entries) == 0 {
		return nil, "", errConfig("required domain list is empty")
	}
	return unique(entries), hex.EncodeToString(h.Sum(nil)), nil
}
func matches(host string, r DomainRule) bool {
	return host == r.Domain || r.Subdomains && strings.HasSuffix(host, "."+r.Domain)
}
func (s *Scanner) HostDenied(host string) bool {
	h, err := normalizeHost(host)
	if err != nil {
		return s.Policy.SourceMode == "allow_only"
	}
	for _, r := range s.Policy.Deny {
		if matches(h, r) {
			return true
		}
	}
	for _, r := range s.Policy.Allow {
		if matches(h, r) {
			return false
		}
	}
	for _, list := range s.domains {
		for candidate := h; candidate != ""; {
			i := sort.SearchStrings(list, candidate)
			if i < len(list) && list[i] == candidate {
				return true
			}
			_, rest, ok := strings.Cut(candidate, ".")
			if !ok {
				break
			}
			candidate = rest
		}
	}
	return s.Policy.SourceMode == "allow_only"
}
func (s *Scanner) SourceFiltering() bool {
	return len(s.Policy.Deny) > 0 || len(s.domains) > 0 || s.Policy.SourceMode == "allow_only"
}

var urlPattern = regexp.MustCompile(`(?i)https?://[^\s<>"\x60]+`)
var emailPattern = regexp.MustCompile(`[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
var cardPattern = regexp.MustCompile(`\b(?:[0-9][ -]?){12,18}[0-9]\b`)
var uuidTokenPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}\b`)
var ibanPattern = regexp.MustCompile(`\b[A-Z]{2}[0-9]{2}(?:[ ]?[A-Z0-9]){11,30}\b`)
var ssnPattern = regexp.MustCompile(`\b([0-9]{3})-([0-9]{2})-([0-9]{4})\b`)

func credentialField(key string) bool {
	n := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	switch n {
	case "apikey", "apikeys", "accesstoken", "refreshtoken", "idtoken", "clientsecret", "secretkey", "privatekey", "password", "passwd", "authorization", "cookie", "setcookie", "secret", "token", "sessiontoken":
		return true
	}
	return false
}

type ScanResult struct {
	Redactions, WithheldRepresentations int
	Denied                              bool
	TimeoutRetries                      int
	// Findings names the detector or rule behind each redaction or denial, in
	// first-seen order. It never contains a matched value.
	Findings []string
}

func (s *ScanResult) redact(kind string) {
	s.Redactions++
	s.note(kind)
}

func (s *ScanResult) deny() {
	s.Denied = true
	s.note("denied domain")
}

func (s *ScanResult) note(kind string) {
	if !slices.Contains(s.Findings, kind) {
		s.Findings = append(s.Findings, kind)
	}
}

func (s *Scanner) Sanitize(ctx context.Context, record map[string]any) (map[string]any, ScanResult, error) {
	stats := ScanResult{}
	value, err := s.walk(ctx, "", record, &stats, 0)
	if err != nil {
		return nil, stats, err
	}
	return value.(map[string]any), stats, nil
}

func (s *Scanner) walk(ctx context.Context, key string, v any, stats *ScanResult, depth int) (any, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if depth > 80 {
		return nil, errConfig("record nesting exceeds scanner limit")
	}
	if credentialField(key) {
		stats.redact("credential field")
		return "[REDACTED:CREDENTIAL]", nil
	}
	if strings.EqualFold(key, "bytes") {
		if marker, ok := v.(map[string]any); ok && fieldString(marker, "export_status") == "withheld_binary" {
			return v, nil
		}
		stats.WithheldRepresentations++
		return map[string]any{"export_status": "withheld_binary"}, nil
	}
	// Strip hydrated projections of related records; each is independently fetched and classified.
	if _, isReference := referenceTypes[key]; isReference {
		if ref, ok := v.(map[string]any); ok {
			minimal := map[string]any{}
			if id, ok := ref["id"].(string); ok {
				minimal["id"] = id
			}
			if indices, ok := ref["indices"].(map[string]any); ok {
				minimal["indices"] = indices
			}
			if items, ok := ref["iterable"].([]any); ok {
				ids := []any{}
				for _, item := range items {
					switch value := item.(type) {
					case string:
						ids = append(ids, value)
					case map[string]any:
						if id, ok := value["id"].(string); ok {
							ids = append(ids, map[string]any{"id": id})
						}
					}
				}
				minimal["iterable"] = ids
			}
			v = minimal
		}
	}
	switch value := v.(type) {
	case map[string]any:
		result := map[string]any{}
		for k, item := range value {
			// Embedded projections can carry excluded records. Canonical records are fetched independently.
			if k == "reference" {
				continue
			}
			cleanKey, err := s.cleanString(ctx, "", k, stats)
			if err != nil {
				return nil, err
			}
			if cleanKey != k {
				return nil, errConfig("sensitive object key requires withholding the record")
			}
			clean, err := s.walk(ctx, k, item, stats, depth+1)
			if err != nil {
				return nil, err
			}
			result[k] = clean
		}
		return result, nil
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			clean, err := s.walk(ctx, key, item, stats, depth+1)
			if err != nil {
				return nil, err
			}
			result[i] = clean
		}
		return result, nil
	case string:
		if !utf8.ValidString(value) {
			return nil, errConfig("invalid text encoding")
		}
		if key == "base64" || key == "base64_url" {
			enc := base64.StdEncoding
			if key == "base64_url" {
				enc = base64.URLEncoding
			}
			b, err := enc.DecodeString(value)
			if err != nil {
				b, err = enc.WithPadding(base64.NoPadding).DecodeString(value)
			}
			if err != nil || !utf8.Valid(b) {
				stats.WithheldRepresentations++
				return "[WITHHELD:ENCODED_CONTENT]", nil
			}
			clean, err := s.cleanString(ctx, "decoded", string(b), stats)
			if err != nil {
				return nil, err
			}
			return enc.EncodeToString([]byte(clean)), nil
		}
		if strings.HasPrefix(strings.ToLower(value), "data:") {
			stats.WithheldRepresentations++
			return "[WITHHELD:DATA_URL]", nil
		}
		return s.cleanString(ctx, key, value, stats)
	case json.Number:
		if s.Policy.Financial && validCard(string(value)) {
			stats.redact("payment card number")
			return "[REDACTED:PAYMENT_CARD]", nil
		}
		return value, nil
	default:
		return v, nil
	}
}

func (s *Scanner) cleanString(ctx context.Context, key, value string, stats *ScanResult) (string, error) {
	if len(value) > 2<<20 {
		return "", errConfig("text field exceeds 2 MiB scanning limit")
	}
	scanURLs(value, func(link string, _ bool) (string, bool) {
		if u, err := url.Parse(markdownUnescape(link)); err == nil && u.Hostname() != "" && s.HostDenied(u.Hostname()) {
			stats.deny()
		}
		return link, false
	})
	// Canonical URL fields may store a hostname without a scheme.
	if strings.Contains(strings.ToLower(key), "url") || strings.EqualFold(key, "hostname") || strings.EqualFold(key, "domain") {
		candidate := value
		if !strings.Contains(candidate, "://") {
			candidate = "https://" + candidate
		}
		if u, err := url.Parse(candidate); err == nil && u.Hostname() != "" && s.HostDenied(u.Hostname()) {
			stats.deny()
		}
	}
	findings, err := s.detectSecrets(ctx, key+"="+value, stats)
	if err != nil {
		return "", err
	}
	// Each secret maps to the finding kind reported for it: the first detector
	// rule that matched it, otherwise the late-discovered credential list.
	secrets := map[string]string{}
	for _, f := range findings {
		if f.Secret == "" {
			continue
		}
		if !strings.Contains(value, f.Secret) {
			stats.redact("encoded secret pattern " + f.RuleID)
			return "[WITHHELD:ENCODED_SECRET]", nil
		}
		if _, seen := secrets[f.Secret]; !seen {
			secrets[f.Secret] = "secret pattern " + f.RuleID
		}
	}
	for secret := range s.known {
		if _, seen := secrets[secret]; !seen && strings.Contains(value, secret) {
			secrets[secret] = "known credential value"
		}
	}
	ordered := []string{}
	for secret := range secrets {
		ordered = append(ordered, secret)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) != len(ordered[j]) {
			return len(ordered[i]) > len(ordered[j])
		}
		return ordered[i] < ordered[j]
	})
	for _, secret := range ordered {
		if strings.Contains(value, secret) {
			stats.redact(secrets[secret])
			value = strings.ReplaceAll(value, secret, "[REDACTED:SECRET]")
		}
	}
	value, _ = scanURLs(value, func(link string, cut bool) (string, bool) {
		clean, kinds := redactURL(link, cut)
		if len(kinds) == 0 {
			return link, false
		}
		stats.Redactions++
		for _, kind := range kinds {
			stats.note(kind)
		}
		return clean, true
	})
	if s.Policy.Financial {
		value = redactPaymentCards(value, stats)
		value = ibanPattern.ReplaceAllStringFunc(value, func(v string) string {
			if validIBAN(v) {
				stats.redact("IBAN")
				return "[REDACTED:IBAN]"
			}
			return v
		})
		value = ssnPattern.ReplaceAllStringFunc(value, func(v string) string {
			if v[:3] != "000" && v[:3] != "666" && v[0] < '9' && v[4:6] != "00" && v[7:] != "0000" {
				stats.redact("US Social Security number")
				return "[REDACTED:SSN]"
			}
			return v
		})
	}
	if s.Policy.Emails {
		value = emailPattern.ReplaceAllStringFunc(value, func(string) string { stats.redact("email address"); return "[REDACTED:EMAIL]" })
	}
	return value, nil
}

// urlTrailingPunctuation lists characters that end prose or Markdown around a
// URL more often than they end the URL itself.
const urlTrailingPunctuation = `.,:;!?'*\…`

// urlFromMatch returns the URL at the start of a urlPattern match. The pattern
// stops only at whitespace and a few delimiters, so a match can absorb sentence
// punctuation, Markdown emphasis and escapes, an ellipsis, unmatched closing
// brackets, or a link label's "](" followed by its destination. The URL ends at
// the first "](" whose remainder hides none of this URL's credential query
// parameters, or else at the end of the match; trailing punctuation is then
// left out. Record scanning and the final output audit read URLs the same way,
// so a sanitized URL placed next to generated Markdown is parsed exactly as it
// was approved. Generated titles escape "]", so a "](" that ended a URL in the
// record reaches the audit as "\](", and the backslash is trimmed as well.
func urlFromMatch(raw string) string {
	floor := strings.Index(raw, "://") + len("://")
	for from := floor; ; {
		i := strings.Index(raw[from:], "](")
		if i < 0 {
			break
		}
		if link := trimURLEnd(raw[:from+i], floor); !hidesCredentialKey(raw, link) {
			return link
		}
		from += i + len("](")
	}
	if link := trimURLEnd(raw, floor); !hidesCredentialKey(raw, link) {
		return link
	}
	return raw
}

func trimURLEnd(s string, floor int) string {
	end := len(s)
	for end > floor {
		last, size := utf8.DecodeLastRuneInString(s[:end])
		if !strings.ContainsRune(urlTrailingPunctuation, last) && !unmatchedCloser(s[:end], last) {
			break
		}
		end -= size
	}
	return s[:end]
}

// unmatchedCloser reports whether s ends with a closing bracket that has no
// opening partner inside s, as when a URL is wrapped in parentheses.
func unmatchedCloser(s string, last rune) bool {
	switch last {
	case ')':
		return strings.Count(s, ")") > strings.Count(s, "(")
	case ']':
		return strings.Count(s, "]") > strings.Count(s, "[")
	case '}':
		return strings.Count(s, "}") > strings.Count(s, "{")
	}
	return false
}

// hidesCredentialKey reports whether the whole match parses with a credential
// query parameter that the shorter link lacks, meaning the cut text belongs to
// the URL's query rather than to Markdown.
func hidesCredentialKey(raw, link string) bool {
	if link == raw {
		return false
	}
	full, err := url.Parse(markdownUnescape(raw))
	if err != nil {
		return false
	}
	var kept url.Values
	if short, err := url.Parse(markdownUnescape(link)); err == nil {
		kept = short.Query()
	}
	for k := range full.Query() {
		if credentialQueryKey(k) && !kept.Has(k) {
			return true
		}
	}
	return false
}

// scanURLs calls visit for each URL in text, in order, and replaces a URL with
// visit's result when visit reports a change. Scanning resumes right after each
// URL, so a URL that the pattern ran into (such as the destination of a
// Markdown link whose label is also a URL) is visited separately. cut reports
// that an ellipsis follows the URL, as when a long title was shortened.
func scanURLs(text string, visit func(link string, cut bool) (string, bool)) (string, bool) {
	var b strings.Builder
	copied, changed := 0, false
	for at := 0; at < len(text); {
		loc := urlPattern.FindStringIndex(text[at:])
		if loc == nil {
			break
		}
		start := at + loc[0]
		link := urlFromMatch(text[start : at+loc[1]])
		if clean, ok := visit(link, strings.HasPrefix(text[start+len(link):], "…")); ok {
			b.WriteString(text[copied:start])
			b.WriteString(clean)
			copied, changed = start+len(link), true
		}
		at = start + len(link)
	}
	if !changed {
		return text, false
	}
	b.WriteString(text[copied:])
	return b.String(), true
}

func credentialQueryKey(k string) bool {
	return credentialField(k) || strings.EqualFold(k, "key") || strings.Contains(strings.ToLower(k), "signature")
}

// redactURL removes user information and credential query values from one URL
// and returns the finding kinds it applied. Values already set to REDACTED are
// left alone, so redacting an approved URL again changes nothing. When cut is
// set, an ellipsis ended the text right after the URL, so the final query value
// may be a REDACTED marker shortened along with its title.
func redactURL(link string, cut bool) (string, []string) {
	plain := markdownUnescape(link)
	u, err := url.Parse(plain)
	if err != nil {
		return link, nil
	}
	var kinds []string
	if u.User != nil {
		u.User = nil
		kinds = append(kinds, "URL user information")
	}
	final := ""
	if cut && !strings.Contains(plain, "#") {
		pair := u.RawQuery[strings.LastIndexByte(u.RawQuery, '&')+1:]
		key, _, _ := strings.Cut(pair, "=")
		final, _ = url.QueryUnescape(key)
	}
	q := u.Query()
	for k := range q {
		if !credentialQueryKey(k) || len(q[k]) == 1 && (q[k][0] == "REDACTED" || k == final && strings.HasPrefix("REDACTED", q[k][0])) {
			continue
		}
		q.Set(k, "REDACTED")
		if !slices.Contains(kinds, "URL credential parameter") {
			kinds = append(kinds, "URL credential parameter")
		}
	}
	if len(kinds) == 0 {
		return link, nil
	}
	u.RawQuery = q.Encode()
	return u.String(), kinds
}

// markdownUnescape removes the backslash from Markdown escapes of ASCII
// punctuation, such as the "\#" and "\_" in generated titles, so a URL is
// parsed the way a reader sees it.
func markdownUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '\\' && i+1 < len(s) && asciiPunctuation(s[i+1]) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func asciiPunctuation(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}

func (s *Scanner) remember(value string) {
	if len(value) >= 4 && len(value) <= 2<<20 && !strings.Contains(value, "[REDACTED:") {
		s.known[value] = true
	}
}
func (s *Scanner) ObserveCredentials(v any) {
	switch x := v.(type) {
	case map[string]any:
		for key, value := range x {
			if credentialField(key) {
				switch a := value.(type) {
				case string:
					s.remember(a)
				case []any:
					for _, item := range a {
						if str, ok := item.(string); ok {
							s.remember(str)
						}
					}
				}
			}
			s.ObserveCredentials(value)
		}
	case []any:
		for _, item := range x {
			s.ObserveCredentials(item)
		}
	}
}

// A UUID can contain a Luhn-valid numeric prefix before its next hyphen. It is
// one identifier, not a payment-card value. Exempt only matches wholly inside
// a complete UUID token; adjacent cards and numeric JSON values still scan.
func redactPaymentCards(value string, stats *ScanResult) string {
	matches := cardPattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	uuids := uuidTokenPattern.FindAllStringIndex(value, -1)
	var b strings.Builder
	last, uuid := 0, 0
	for _, match := range matches {
		for uuid < len(uuids) && uuids[uuid][1] <= match[0] {
			uuid++
		}
		identifier := uuid < len(uuids) && uuids[uuid][0] <= match[0] && match[1] <= uuids[uuid][1]
		if identifier || !validCard(value[match[0]:match[1]]) {
			continue
		}
		b.WriteString(value[last:match[0]])
		b.WriteString("[REDACTED:PAYMENT_CARD]")
		last = match[1]
		stats.redact("payment card number")
	}
	if last == 0 {
		return value
	}
	b.WriteString(value[last:])
	return b.String()
}

func validCard(s string) bool {
	s = strings.NewReplacer(" ", "", "-", "").Replace(s)
	if len(s) < 13 || len(s) > 19 || strings.Trim(s, "0") == "" {
		return false
	}
	sum := 0
	double := false
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
		n := int(s[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}
func validIBAN(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	s = s[4:] + s[:4]
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = (n*10 + int(c-'0')) % 97
		} else if c >= 'A' && c <= 'Z' {
			n = (n*100 + int(c-'A') + 10) % 97
		} else {
			return false
		}
	}
	return n == 1
}
