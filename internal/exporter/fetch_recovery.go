package exporter

// Source recovery retains encrypted raw hydration batches. On resume, inventories,
// updated-ID queries and all relationship traversals run again. Only still-present
// records not reported updated since collection began are eligible for reuse.
import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/pieces-app/export-tool/internal/recovery"
)

type fetchState struct {
	Core         captureCore
	Domains      [][]string
	Secrets      [][]byte
	Health, User string
	Outputs      []string
	Complete     string
}

type fetchedRecord struct {
	Type string
	ID   string
	Data json.RawMessage
}

type fetchProgress struct {
	Version int
	Secrets [][]byte
}

type fetchCheckpoint struct {
	scanner                      *Scanner
	learned                      [][]byte
	root, records                *recovery.Store
	options                      RecoveryOptions
	state                        fetchState
	generation, recordGeneration uint64
	eligible                     map[[32]byte]bool
	pending                      []recovery.Record
	pendingBytes                 int
	reused, fresh                int
	resumed                      bool
}

var fetchStoreBinding = sha256.Sum256([]byte("pieces-export/source-hydration/2026-10-01/v1"))

func fetchStorage(o RecoveryOptions) recovery.Options {
	return recovery.Options{Directory: filepath.Join(o.Directory, "fetch"), KeyDirectory: filepath.Join(o.KeyDirectory, "fetch"), Binding: fetchStoreBinding}
}
func fetchRef(typ, id string) [32]byte {
	return sha256.Sum256([]byte("source-record/v1\x00" + typ + "\x00" + id))
}

func sourceIdentity(ctx context.Context, c *Client) (string, string, string, error) {
	health, err := c.request(ctx, "GET", "/.well-known/health", nil)
	if err != nil {
		return "", "", "", err
	}
	h := plain(health)
	if !strings.HasPrefix(h, "ok:") || len(h) <= 3 || len(h) > 512 {
		return "", "", "", errConfig("source recovery requires a ready OS installation identity")
	}
	version, err := c.Probe(ctx)
	if err != nil {
		return "", "", "", err
	}
	user, err := currentUserID(ctx, c)
	if err != nil && !isNotFound(err) {
		return "", "", "", err
	}
	return h, version, user, nil
}

func startFetchRecovery(r *run, root *recovery.Store, o RecoveryOptions) (_ *fetchCheckpoint, result error) {
	h, v, u, err := sourceIdentity(r.ctx, r.client)
	if err != nil {
		return nil, err
	}
	core := captureCore{CapturedAt: time.Now().UTC(), Options: r.opts, Manifest: r.manifest, Policy: r.opts.Scanner.Policy, PolicyHash: r.opts.Scanner.Hash, ListHashes: r.opts.Scanner.ListHashes}
	core.Options.Scanner, core.Options.Progress, core.Options.captureCheckpoint, core.Options.fetchCheckpoint = nil, nil, nil, nil
	core.Options.Recovery = nil
	core.Options.Output = strings.TrimSuffix(r.stage, ".partial")
	f := &fetchCheckpoint{root: root, options: o, scanner: r.opts.Scanner, state: fetchState{Core: core, Domains: r.opts.Scanner.domains, Health: h, User: u, Outputs: []string{core.Options.Output}}}
	f.state.Core.Manifest.OSVersion = v
	for value := range r.opts.Scanner.known {
		f.state.Secrets = append(f.state.Secrets, []byte(value))
	}
	f.records, err = recovery.CreateStore(r.ctx, fetchStorage(o), []byte(`{"Version":1}`))
	if err != nil {
		return nil, err
	}
	if err = f.saveState(r.ctx); err != nil {
		_ = f.close()
		return nil, err
	}
	return f, nil
}

func (f *fetchCheckpoint) saveState(ctx context.Context) error {
	b, err := json.Marshal(captureFrame{Version: captureVersion, Phase: "source-fetching", Parts: map[string]int{}, Fetch: &f.state})
	if err != nil {
		return errConfig("cannot encode source recovery state")
	}
	defer clear(b)
	if err = f.root.Commit(ctx, f.generation, nil, b); err != nil {
		return err
	}
	f.generation++
	return nil
}
func validateFetched(item recovery.Record) (fetchedRecord, error) {
	var row fetchedRecord
	if decodeCapture(item.Payload, &row) != nil || row.ID == "" || fetchRef(row.Type, row.ID) != item.Ref {
		return row, errConfig("invalid source recovery record identity")
	}
	if _, ok := materialByType(row.Type); !ok {
		return row, errConfig("unsupported source recovery material")
	}
	var v map[string]any
	if decodeArchiveJSON(row.Data, &v) != nil || fieldString(v, "id") != row.ID {
		return row, errConfig("invalid source recovery record body")
	}
	return row, nil
}

func openFetchRecovery(ctx context.Context, root *recovery.Store, o RecoveryOptions, frame captureFrame, generation uint64) (_ *fetchCheckpoint, result error) {
	if frame.Fetch == nil || frame.Phase != "source-fetching" || len(frame.Parts) != 0 {
		return nil, errConfig("invalid source recovery checkpoint")
	}
	f := &fetchCheckpoint{root: root, options: o, state: *frame.Fetch, generation: generation, resumed: true}
	if f.state.Core.Options.Scanner != nil || f.state.Core.Options.Progress != nil || (f.state.Core.Options.Mode != "filtered" && f.state.Core.Options.Mode != "preserve") || f.state.Core.Manifest.Mode != f.state.Core.Options.Mode || f.state.Core.Options.Recovery != nil || len(f.state.Core.Options.SDKCaches) > 0 || f.state.Core.Options.Format != "markdown" || len(f.state.Outputs) == 0 || f.state.Core.Options.Output != f.state.Outputs[0] || f.state.Core.Manifest.Started.IsZero() {
		return nil, errConfig("invalid source recovery configuration")
	}
	if err := validateCaptureOptions(f.state.Core.Options); err != nil {
		return nil, err
	}
	secrets := map[string]bool{}
	for _, b := range f.state.Secrets {
		secrets[string(b)] = true
	}
	if _, err := restoreCaptureScanner(f.state.Core, f.state.Domains, secrets); err != nil {
		return nil, err
	}
	var err error
	f.records, err = recovery.OpenStore(ctx, fetchStorage(o))
	if err != nil {
		return nil, err
	}
	defer func() {
		if result != nil {
			_ = f.close()
		}
	}()
	cp, err := f.records.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	var progress fetchProgress
	if decodeCapture(cp.State, &progress) != nil || progress.Version != 1 {
		return nil, errConfig("invalid hydration checkpoint")
	}
	for _, secret := range progress.Secrets {
		if len(secret) < 4 || len(secret) > 2<<20 {
			return nil, errConfig("invalid checkpoint credential")
		}
	}
	f.learned = progress.Secrets
	f.recordGeneration = cp.Generation
	if err = f.records.Visit(ctx, func(item recovery.Record) error { _, e := validateFetched(item); return e }); err != nil {
		return nil, err
	}
	return f, nil
}

// Reuse is conservative: a failed freshness query disables reuse for that material.
// No recorded snapshot is trusted merely because a count is unchanged.
func (f *fetchCheckpoint) prepare(ctx context.Context, c *Client) error {
	h, v, u, err := sourceIdentity(ctx, c)
	if err != nil {
		return err
	}
	if h != f.state.Health || v != f.state.Core.Manifest.OSVersion || u != f.state.User {
		return errConfig("recovery belongs to a different OS installation, version, or user; start a new export")
	}
	byType := map[string][]string{}
	if err = f.records.Visit(ctx, func(item recovery.Record) error {
		row, e := validateFetched(item)
		if e != nil {
			return e
		}
		var v map[string]any
		if e = decodeArchiveJSON(row.Data, &v); e != nil {
			return e
		}
		updated, e := time.Parse(time.RFC3339Nano, timestamp(v, "updated"))
		if e == nil && !updated.After(f.state.Core.Manifest.Started) {
			byType[row.Type] = append(byType[row.Type], row.ID)
		}
		return nil
	}); err != nil {
		return err
	}
	f.eligible = map[[32]byte]bool{}
	for _, m := range f.state.Core.Options.Materials {
		ids := byType[m.Type]
		if len(ids) == 0 {
			continue
		}
		live, e := c.IDs(ctx, m, Window{})
		if e != nil {
			if errors.Is(e, ErrOSBusy) || ctx.Err() != nil {
				return e
			}
			continue
		}
		body := map[string]any{"material_type": m.Type, "updated": map[string]any{"from": map[string]any{"value": f.state.Core.Manifest.Started.UTC().Format(time.RFC3339Nano)}}}
		var out struct {
			IDs *[]string `json:"identifiers"`
		}
		e = c.JSON(ctx, "POST", "/materials/identifiers", body, &out)
		if e != nil {
			if errors.Is(e, ErrOSBusy) || ctx.Err() != nil {
				return e
			}
			continue
		}
		if out.IDs == nil {
			continue
		}
		present, changed := map[string]bool{}, map[string]bool{}
		for _, id := range live {
			present[id] = true
		}
		for _, id := range *out.IDs {
			changed[id] = true
		}
		for _, id := range ids {
			if present[id] && !changed[id] {
				f.eligible[fetchRef(m.Type, id)] = true
			}
		}
	}
	return nil
}

func (f *fetchCheckpoint) get(ctx context.Context, m Material, id string) (map[string]any, error) {
	if f == nil || !f.eligible[fetchRef(m.Type, id)] {
		return nil, nil
	}
	b, err := f.records.Get(ctx, fetchRef(m.Type, id))
	if err != nil {
		return nil, err
	}
	defer clear(b)
	row, err := validateFetched(recovery.Record{Ref: fetchRef(m.Type, id), Payload: b})
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err = decodeArchiveJSON(row.Data, &v); err != nil {
		return nil, err
	}
	f.reused++
	return v, nil
}
func (f *fetchCheckpoint) put(ctx context.Context, m Material, v map[string]any) error {
	if f == nil {
		return nil
	}
	id := fieldString(v, "id")
	if id == "" {
		return errConfig("cannot checkpoint source without identity")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	defer clear(raw)
	b, err := json.Marshal(fetchedRecord{Type: m.Type, ID: id, Data: raw})
	if err != nil {
		return err
	}
	if len(b) > canonicalPayloadLimit {
		clear(b)
		return errConfig("source recovery record exceeds its bound")
	}
	if len(f.pending) > 0 && (len(f.pending) >= 50 || f.pendingBytes+len(b) > 7<<20) {
		if err = f.flush(ctx); err != nil {
			clear(b)
			return err
		}
	}
	f.pending = append(f.pending, recovery.Record{Ref: fetchRef(m.Type, id), Payload: b})
	f.pendingBytes += len(b)
	f.fresh++
	return nil
}
func (f *fetchCheckpoint) flush(ctx context.Context) error {
	if f == nil || len(f.pending) == 0 {
		return ctx.Err()
	}
	progress := fetchProgress{Version: 1}
	for secret := range f.scanner.known {
		progress.Secrets = append(progress.Secrets, []byte(secret))
	}
	state, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	defer clear(state)
	for len(f.pending) > 0 {
		count, total := 0, len(state)+1024
		for count < len(f.pending) && (count == 0 || total+len(f.pending[count].Payload) <= 8<<20) {
			total += len(f.pending[count].Payload)
			count++
		}
		if err = f.records.Commit(ctx, f.recordGeneration, f.pending[:count], state); err != nil {
			return err
		}
		f.recordGeneration++
		for _, row := range f.pending[:count] {
			f.pendingBytes -= len(row.Payload)
			clear(row.Payload)
		}
		f.pending = f.pending[count:]
	}
	return nil
}

func (f *fetchCheckpoint) close() error {
	if f == nil {
		return nil
	}
	for _, row := range f.pending {
		clear(row.Payload)
	}
	f.pending = nil
	if f.records == nil {
		return nil
	}
	err := f.records.Close()
	f.records = nil
	return err
}
func (f *fetchCheckpoint) capture(r *run) (result error) {
	if err := f.flush(r.ctx); err != nil {
		return err
	}
	// Recheck identity before declaring source collection complete.
	h, v, u, err := sourceIdentity(r.ctx, r.client)
	if err != nil {
		return err
	}
	if h != f.state.Health || v != f.state.Core.Manifest.OSVersion || u != f.state.User {
		return errConfig("source identity changed during recovery-enabled export")
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return err
	}
	name := "capture-" + hex.EncodeToString(token)
	cfg := RecoveryOptions{Directory: filepath.Join(f.options.Directory, name), KeyDirectory: filepath.Join(f.options.KeyDirectory, name)}
	// Root ownership remains held; each attempt uses a new, exclusively created child.
	store, err := recovery.CreateStore(r.ctx, recoveryStorage(cfg), []byte(`{"Version":1,"Phase":"fetching","Parts":{}}`))
	if err != nil {
		return err
	}
	defer func() {
		if e := store.Close(); result == nil {
			result = e
		}
	}()
	if err = r.saveCapture(store); err != nil {
		return err
	}
	f.state.Complete = name
	return f.saveState(r.ctx)
}
func (f *fetchCheckpoint) completeOptions() (RecoveryOptions, error) {
	name := f.state.Complete
	if len(name) != 40 || !strings.HasPrefix(name, "capture-") {
		return RecoveryOptions{}, errConfig("invalid completed capture location")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(name, "capture-")); err != nil {
		return RecoveryOptions{}, errConfig("invalid completed capture location")
	}
	return RecoveryOptions{Directory: filepath.Join(f.options.Directory, name), KeyDirectory: filepath.Join(f.options.KeyDirectory, name)}, nil
}
func (s *RecoverySession) Continue(ctx context.Context, c *Client, output string, progress io.Writer, version string) (Manifest, error) {
	if s.fetch == nil || s.fetch.state.Complete != "" {
		return Manifest{}, errConfig("checkpoint requires offline replay")
	}
	if err := s.ValidateOutput(output); err != nil {
		return Manifest{}, err
	}
	s.used = true
	f := s.fetch
	if err := f.prepare(ctx, c); err != nil {
		return Manifest{}, err
	}
	if progress != nil {
		fmt.Fprintf(progress, "Freshness checks complete: %d saved record snapshots eligible for reuse. Relationships and inventories will be read again.\n", len(f.eligible))
	}
	o := f.state.Core.Options
	secrets := map[string]bool{}
	for _, b := range append(append([][]byte{}, f.state.Secrets...), f.learned...) {
		secrets[string(b)] = true
	}
	var err error
	o.Scanner, err = restoreCaptureScanner(f.state.Core, f.state.Domains, secrets)
	if err != nil {
		return Manifest{}, err
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return Manifest{}, err
	}
	f.state.Outputs = append(f.state.Outputs, abs)
	if err = f.saveState(ctx); err != nil {
		return Manifest{}, err
	}
	f.scanner = o.Scanner
	o.Output, o.Progress, o.Version = abs, progress, version
	o.fetchCheckpoint = f
	o.captureCheckpoint = f.capture
	o.Recovery = nil
	return Export(ctx, c, o)
}
