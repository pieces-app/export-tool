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
	"sort"
	"strings"
	"sync"
	"time"
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
		stats.Redactions++
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
			stats.Redactions++
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
	for _, raw := range urlPattern.FindAllString(value, -1) {
		u, err := url.Parse(strings.TrimRight(raw, ".,;)]}"))
		if err == nil && u.Hostname() != "" && s.HostDenied(u.Hostname()) {
			stats.Denied = true
		}
	}
	// Canonical URL fields may store a hostname without a scheme.
	if strings.Contains(strings.ToLower(key), "url") || strings.EqualFold(key, "hostname") || strings.EqualFold(key, "domain") {
		candidate := value
		if !strings.Contains(candidate, "://") {
			candidate = "https://" + candidate
		}
		if u, err := url.Parse(candidate); err == nil && u.Hostname() != "" && s.HostDenied(u.Hostname()) {
			stats.Denied = true
		}
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	findings := s.detector.DetectContext(deadline, detect.Fragment{Raw: key + "=" + value})
	if deadline.Err() != nil {
		return "", errConfig("secret scan did not finish")
	}
	secrets := map[string]bool{}
	for _, f := range findings {
		if f.Secret == "" {
			continue
		}
		if !strings.Contains(value, f.Secret) {
			stats.Redactions++
			return "[WITHHELD:ENCODED_SECRET]", nil
		}
		secrets[f.Secret] = true
	}
	for secret := range s.known {
		if strings.Contains(value, secret) {
			secrets[secret] = true
		}
	}
	ordered := []string{}
	for secret := range secrets {
		ordered = append(ordered, secret)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		if strings.Contains(value, secret) {
			stats.Redactions++
			value = strings.ReplaceAll(value, secret, "[REDACTED:SECRET]")
		}
	}
	value = urlPattern.ReplaceAllStringFunc(value, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		changed := false
		if u.User != nil {
			u.User = nil
			changed = true
		}
		q := u.Query()
		for k := range q {
			if credentialField(k) || strings.EqualFold(k, "key") || strings.Contains(strings.ToLower(k), "signature") {
				if len(q[k]) == 1 && q[k][0] == "REDACTED" {
					continue
				}
				q.Set(k, "REDACTED")
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
			stats.Redactions++
			return u.String()
		}
		return raw
	})
	if s.Policy.Financial {
		value = redactPaymentCards(value, stats)
		value = ibanPattern.ReplaceAllStringFunc(value, func(v string) string {
			if validIBAN(v) {
				stats.Redactions++
				return "[REDACTED:IBAN]"
			}
			return v
		})
		value = ssnPattern.ReplaceAllStringFunc(value, func(v string) string {
			if v[:3] != "000" && v[:3] != "666" && v[0] < '9' && v[4:6] != "00" && v[7:] != "0000" {
				stats.Redactions++
				return "[REDACTED:SSN]"
			}
			return v
		})
	}
	if s.Policy.Emails {
		value = emailPattern.ReplaceAllStringFunc(value, func(string) string { stats.Redactions++; return "[REDACTED:EMAIL]" })
	}
	return value, nil
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
		stats.Redactions++
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
