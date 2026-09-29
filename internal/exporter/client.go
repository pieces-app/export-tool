package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type APIError struct{ Status int }

func (e *APIError) Error() string {
	return fmt.Sprintf("OS returned HTTP %d (response body omitted)", e.Status)
}
func errConfig(s string) error { return errors.New(s) }

type Client struct {
	base     string
	http     *http.Client
	maxBytes int64
	pacer    *Pacer
}

func NewClient(base string, timeout time.Duration, maxBytes int64) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errConfig("base URL must be an http loopback origin, for example http://127.0.0.1:39300")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errConfig("only local Pieces OS loopback endpoints are supported")
	}
	// Pin localhost to a literal loopback address; do not use environment proxies or redirects.
	if host == "localhost" {
		port := u.Port()
		if port == "" {
			port = "80"
		}
		u.Host = net.JoinHostPort("127.0.0.1", port)
	}
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: timeout}).DialContext, MaxIdleConnsPerHost: 2}
	return &Client{strings.TrimSuffix(u.String(), "/"), &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, maxBytes, nil}, nil
}

func (c *Client) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.pacer != nil {
			if err := c.pacer.before(ctx); err != nil {
				return nil, err
			}
		}
		started := time.Now()
		resp, err := c.http.Do(req)
		if err != nil {
			if c.pacer != nil {
				c.pacer.after(routeKey(path), time.Since(started), 0, 0, true, batchItemCount(body))
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if c.pacer != nil {
				return nil, c.stopBusy()
			}
			return nil, errConfig("cannot reach local Pieces OS; check its port and running state")
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
		resp.Body.Close()
		if c.pacer != nil && c.pacer.after(routeKey(path), time.Since(started), len(data), resp.StatusCode, readErr != nil, batchItemCount(body)) {
			return nil, ErrOSBusy
		}
		if int64(len(data)) > c.maxBytes {
			return nil, errConfig("OS response exceeds configured size limit; use smaller time windows or raise --max-response-mib")
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if c.pacer != nil {
				return nil, c.stopBusy()
			}
			return nil, errConfig("OS response interrupted")
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return data, nil
		}
		if attempt < 2 && (resp.StatusCode == 429 || (resp.StatusCode >= 500 && resp.StatusCode != 593 && resp.StatusCode != 501)) {
			delay := time.Duration(1<<attempt) * time.Second
			if secs, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && secs > 0 && secs <= 30 {
				delay = time.Duration(secs) * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			if c.pacer != nil {
				c.pacer.mu.Lock()
				c.pacer.stats.Retries++
				c.pacer.mu.Unlock()
			}
			continue
		}
		if c.pacer != nil && (resp.StatusCode == 429 || resp.StatusCode >= 500 && resp.StatusCode != 593 && resp.StatusCode != 501) {
			return nil, c.stopBusy()
		}
		return nil, &APIError{resp.StatusCode}
	}
	return nil, errConfig("OS request exhausted retries")
}

func (c *Client) JSON(ctx context.Context, method, path string, body any, out any) error {
	b, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(out) != nil {
		return errConfig("OS returned invalid JSON")
	}
	if d.Decode(new(any)) != io.EOF {
		return errConfig("OS returned trailing JSON data")
	}
	return nil
}

type ServerInfo struct {
	BaseURL     string `json:"base_url"`
	Version     string `json:"version"`
	Environment string `json:"environment"`
	Ready       bool   `json:"ready"`
}

var ErrNotFound = errors.New("Pieces OS not found on ports 39300–39333")
var ErrMigrating = errors.New("Pieces OS is migrating or opening its database")

func plain(b []byte) string {
	var s string
	if json.Unmarshal(b, &s) == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(b))
}
func (c *Client) Inspect(ctx context.Context) (ServerInfo, error) {
	info := ServerInfo{BaseURL: c.base}
	h, err := c.request(ctx, "GET", "/.well-known/health", nil)
	if err != nil {
		return info, err
	}
	health := plain(h)
	if health != "migrating" && !strings.HasPrefix(health, "ok:") {
		return info, errConfig("unrecognized Pieces OS health response")
	}
	b, err := c.request(ctx, "GET", "/.well-known/version", nil)
	if err != nil {
		return info, err
	}
	if len(b) > 256 {
		return info, errConfig("unexpected OS version response")
	}
	info.Version = plain(b)
	if info.Version == "" || strings.ContainsAny(info.Version, "\r\n\x1b") {
		return info, errConfig("invalid OS version")
	}
	info.Environment = "unknown"
	if strings.HasSuffix(info.Version, "-staging") {
		info.Environment = "staging"
	} else if productionVersion.MatchString(info.Version) {
		info.Environment = "production"
	}
	info.Ready = strings.HasPrefix(health, "ok:")
	return info, nil
}

var productionVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:\+[A-Za-z0-9.-]+)?$`)

func (c *Client) Probe(ctx context.Context) (string, error) {
	info, err := c.Inspect(ctx)
	if err != nil {
		return "", err
	}
	if !info.Ready {
		return "", ErrMigrating
	}
	return info.Version, nil
}
func Discover(ctx context.Context, timeout time.Duration, maxBytes int64) (*Client, error) {
	c, _, err := DiscoverEnvironment(ctx, timeout, maxBytes, "auto")
	return c, err
}
func DiscoverEnvironment(ctx context.Context, timeout time.Duration, maxBytes int64, environment string) (*Client, ServerInfo, error) {
	return discoverRange(ctx, timeout, maxBytes, environment, 39300, 39333)
}
func discoverRange(ctx context.Context, timeout time.Duration, maxBytes int64, environment string, first, last int) (*Client, ServerInfo, error) {
	type candidate struct {
		client *Client
		info   ServerInfo
	}
	results := make(chan candidate, last-first+1)
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for port := first; port <= last; port++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			c, _ := NewClient(fmt.Sprintf("http://127.0.0.1:%d", port), timeout, maxBytes)
			probe, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
			defer cancel()
			info, err := c.Inspect(probe)
			if err == nil && (environment == "auto" || info.Environment == environment) {
				results <- candidate{c, info}
			}
		}(port)
	}
	wg.Wait()
	close(results)
	if ctx.Err() != nil {
		return nil, ServerInfo{}, ctx.Err()
	}
	found := []candidate{}
	for candidate := range results {
		found = append(found, candidate)
	}
	if len(found) > 1 {
		return nil, ServerInfo{}, errConfig("multiple matching Pieces OS instances; select --environment or --base-url")
	}
	if len(found) == 0 {
		return nil, ServerInfo{}, ErrNotFound
	}
	if !found[0].info.Ready {
		return found[0].client, found[0].info, ErrMigrating
	}
	return found[0].client, found[0].info, nil
}

type Window struct{ From, To *time.Time }

func (w Window) fields() map[string]any {
	if w.From == nil && w.To == nil {
		return nil
	}
	r := map[string]any{}
	if w.From != nil {
		r["from"] = map[string]any{"value": w.From.UTC().Format(time.RFC3339Nano)}
	}
	if w.To != nil {
		r["to"] = map[string]any{"value": w.To.UTC().Format(time.RFC3339Nano)}
	}
	return map[string]any{"created": r}
}
func (c *Client) Count(ctx context.Context, m Material, w Window) (int, error) {
	body := map[string]any{"material_type": m.Type}
	if f := w.fields(); f != nil {
		body["filters"] = f
	}
	var out struct {
		Count *int `json:"total_count"`
	}
	if err := c.JSON(ctx, "POST", "/materials/metrics", body, &out); err != nil {
		return 0, err
	}
	if out.Count == nil || *out.Count < 0 {
		return 0, errConfig("invalid material count")
	}
	return *out.Count, nil
}
func (c *Client) IDs(ctx context.Context, m Material, w Window) ([]string, error) {
	body := map[string]any{"material_type": m.Type}
	for k, v := range w.fields() {
		body[k] = v
	}
	var out struct {
		IDs *[]string `json:"identifiers"`
	}
	if err := c.JSON(ctx, "POST", "/materials/identifiers", body, &out); err != nil {
		return nil, err
	}
	if out.IDs == nil {
		return nil, errConfig("missing identifiers response field")
	}
	return unique(*out.IDs), nil
}
func unique(ids []string) []string {
	sort.Strings(ids)
	result := []string{}
	for _, id := range ids {
		if id != "" && (len(result) == 0 || id != result[len(result)-1]) {
			result = append(result, id)
		}
	}
	return result
}

// WindowedIDs fetches every ID in overlapping inclusive windows; limits never act as cursors.
func (c *Client) WindowedIDs(ctx context.Context, m Material, w Window, target, depth int) ([]string, error) {
	n, err := c.Count(ctx, m, w)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return []string{}, nil
	}
	if n <= target || depth >= 64 {
		return c.IDs(ctx, m, w)
	}
	var mid time.Time
	switch {
	case w.From == nil && w.To == nil:
		mid = time.Now().UTC()
	case w.From == nil:
		mid = w.To.AddDate(-1, 0, 0)
	case w.To == nil:
		mid = w.From.AddDate(1, 0, 0)
	default:
		mid = w.From.Add(w.To.Sub(*w.From) / 2)
	}
	if (w.From != nil && !mid.After(*w.From)) || (w.To != nil && !mid.Before(*w.To)) {
		return c.IDs(ctx, m, w)
	}
	a, err := c.WindowedIDs(ctx, m, Window{w.From, &mid}, target, depth+1)
	if err != nil {
		return nil, err
	}
	b, err := c.WindowedIDs(ctx, m, Window{&mid, w.To}, target, depth+1)
	if err != nil {
		return nil, err
	}
	return unique(append(a, b...)), nil
}
