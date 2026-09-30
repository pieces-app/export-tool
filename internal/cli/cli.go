package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/pieces-app/export-tool/internal/lifecycle"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pieces-app/export-tool/internal/exporter"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, version string) int {
	return RunWithInput(ctx, args, os.Stdin, stdout, stderr, version)
}
func RunWithInput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, help)
		return 0
	}
	fail := func(err error) int { fmt.Fprintln(stderr, "Error:", err); return 1 }
	switch args[0] {
	case "rebuild":
		return rebuild(ctx, args[1:], stdin, stdout, stderr, version)
	case "version", "--version":
		fmt.Fprintln(stdout, "pieces-export", version)
		return 0
	case "materials":
		for _, m := range exporter.Materials {
			fmt.Fprintln(stdout, m.Type)
		}
		return 0
	case "policy":
		if len(args) < 2 || args[1] != "init" {
			fmt.Fprintln(stderr, "Usage: pieces-export policy init --output policy.json")
			return 1
		}
		fs := flag.NewFlagSet("policy init", flag.ContinueOnError)
		fs.SetOutput(stderr)
		out := fs.String("output", "export-policy.json", "new JSON policy file")
		if err := fs.Parse(args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 1
		}
		if fs.NArg() != 0 {
			return fail(fmt.Errorf("unexpected positional arguments"))
		}
		b, _ := json.MarshalIndent(exporter.DefaultPolicy(), "", "  ")
		f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return fail(fmt.Errorf("cannot create policy; choose a new filename"))
		}
		_, err = f.Write(append(b, '\n'))
		closeErr := f.Close()
		if err != nil {
			return fail(err)
		}
		if closeErr != nil {
			return fail(closeErr)
		}
		fmt.Fprintln(stdout, "Policy created. Website categories are disabled until domain lists are configured.")
		return 0
	case "lists":
		if len(args) < 2 || args[1] != "fetch" {
			fmt.Fprintln(stderr, "Usage: pieces-export lists fetch --output lists --categories adult,bank")
			return 1
		}
		fs := flag.NewFlagSet("lists fetch", flag.ContinueOnError)
		fs.SetOutput(stderr)
		out := fs.String("output", "lists", "new local list directory")
		categories := fs.String("categories", "adult,bank", "UT1 categories to download")
		if err := fs.Parse(args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 1
		}
		if fs.NArg() != 0 {
			return fail(fmt.Errorf("unexpected positional arguments"))
		}
		if err := exporter.FetchLists(ctx, *out, strings.Split(*categories, ",")); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Category domain lists downloaded. Configure their paths in your policy JSON.")
		return 0
	case "doctor", "scan", "benchmark", "export":
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		fs.SetOutput(stderr)
		base := fs.String("base-url", "", "local Pieces OS URL; otherwise discover ports 39300–39333")
		timeout := fs.Duration("timeout", 60*time.Second, "timeout for each OS request")
		maxMiB := fs.Int64("max-response-mib", 64, "maximum OS response size in MiB")
		out := fs.String("output", "pieces-export-"+time.Now().Format("20060102-150405"), "new output directory (must not exist)")
		mode := fs.String("mode", "filtered", "filtered or preserve; preserve includes original sensitive content")
		policyPath := fs.String("policy", "", "JSON privacy policy; defaults to embedded secret and basic financial detection")
		scope := fs.String("scope", "", "all (default) or summaries; summaries skips events and reads supporting labels only when referenced")
		associations := fs.String("associations", "linked", "linked (default) reads observed-pair metadata and selected person/event pages; off skips these extra reads")
		materials := fs.String("materials", "", "custom comma-separated material types or all; cannot combine with --scope")
		batch := fs.Int("batch-size", 50, "maximum IDs per batch (1–50); pacing starts smaller")
		window := fs.Int("window-ids", 5000, "target ID count per adaptive time window")
		zone := fs.String("timezone", "UTC", "IANA timezone for chronological daily indexes")
		environment := fs.String("environment", "auto", "auto, production, or staging (verified using OS version)")
		launch := fs.Bool("launch-os", true, "launch an installed OS when discovery finds none")
		osPath := fs.String("os-path", "", "explicit OS executable or macOS app bundle; recommended for staging")
		startup := fs.Duration("startup-timeout", 2*time.Minute, "deadline for launch and database readiness")
		closeDesktop := fs.Bool("close-desktop", true, "gracefully close Pieces Desktop after approval")
		yes := fs.Bool("yes", false, "approve export without interactive prompts")
		fs.BoolVar(yes, "y", false, "approve export without interactive prompts")
		format := fs.String("format", "", "markdown, pdf (with Markdown companions), or both")
		pdfFont := fs.String("pdf-font", "", "optional local TrueType font for additional Unicode coverage")
		pdfLimits := pdfLimitFlags(fs)
		signalDigest := signalDigestFlags(fs, false)
		naming := fs.String("naming", "readable", "readable summary names or opaque")
		relationships := fs.String("relationships", "both", "inline, sidecar, or both")
		relatedOrder := fs.String("related-order", "relevance", "rank related summaries by relevance or recent")
		relatedLimit := fs.Int("related-limit", 50, "maximum related summaries per dimension (1–500)")
		relatedSince := fs.String("related-since", "", "related-list cutoff: YYYY-MM-DD (UTC) or RFC3339; all records still exported")
		performance := fs.String("performance", "adaptive", "adaptive batch sizing/pacing or conservative (one data request at a time)")
		targetLatency := fs.Duration("target-latency", 500*time.Millisecond, "request latency target; overload reduces batches and adds pauses")
		dryRun := fs.Bool("dry-run", false, "scan plus bounded read calibration; no export files or Desktop closure")
		benchmarkDuration := fs.Duration("benchmark-duration", 30*time.Second, "maximum time for bounded read calibration (up to 2m)")
		benchmarkReads := fs.Int("benchmark-reads", 40, "maximum calibration batch reads (1–100)")
		people := fs.String("people", "all", "all, profiles, or connected; focused selection does not merge identities")
		minConnections := fs.Int("min-person-connections", 10, "distinct content connections to qualify in connected mode; profiles and summary-linked people always qualify")
		peopleReport := fs.Bool("people-report", false, "read persons and referenced annotation types for an aggregate selection preview")
		metadata := fs.String("metadata", "auto", "auto native attributes or off; portable sidecars always included")
		var sdkCaches []string
		fs.Func("sdk-cache", "optional historical SDK SQLite cache; repeat for up to eight files; recovered links remain unverified", func(path string) error {
			if strings.TrimSpace(path) == "" {
				return fmt.Errorf("sdk-cache needs a local file path")
			}
			sdkCaches = append(sdkCaches, path)
			return nil
		})

		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 1
		}
		if fs.NArg() != 0 {
			return fail(fmt.Errorf("unexpected positional arguments"))
		}
		if err := exporter.ValidateSDKCaches(ctx, sdkCaches); err != nil {
			return fail(err)
		}
		if (*performance != "adaptive" && *performance != "conservative") || *targetLatency < 50*time.Millisecond || *targetLatency > 5*time.Second || *benchmarkDuration <= 0 || *benchmarkDuration > 2*time.Minute || *benchmarkReads < 1 || *benchmarkReads > 100 {
			return fail(fmt.Errorf("invalid performance mode, target-latency (50ms–5s), or benchmark budget"))
		}
		if (*people != "all" && *people != "profiles" && *people != "connected") || *minConnections < 1 {
			return fail(fmt.Errorf("people must be all, profiles, or connected; min-person-connections must be positive"))
		}

		if *timeout <= 0 || *maxMiB < 1 || *maxMiB > 1024 {
			return fail(fmt.Errorf("timeout must be positive and max-response-mib must be 1–1024"))
		}
		if *startup <= 0 || (*format != "" && *format != "markdown" && *format != "pdf" && *format != "both") {
			return fail(fmt.Errorf("invalid startup timeout or format"))
		}
		if *mode != "filtered" && *mode != "preserve" {
			return fail(fmt.Errorf("mode must be filtered or preserve"))
		}
		if *naming != "readable" && *naming != "opaque" {
			return fail(fmt.Errorf("naming must be readable or opaque"))
		}
		if *relationships != "inline" && *relationships != "sidecar" && *relationships != "both" {
			return fail(fmt.Errorf("relationships must be inline, sidecar, or both"))
		}
		if *metadata != "auto" && *metadata != "off" {
			return fail(fmt.Errorf("metadata must be auto or off"))
		}
		if (*relatedOrder != "relevance" && *relatedOrder != "recent") || *relatedLimit < 1 || *relatedLimit > 500 {
			return fail(fmt.Errorf("related-order must be relevance or recent; related-limit must be 1–500"))
		}
		var since time.Time
		if *relatedSince != "" {
			var err error
			since, err = time.Parse(time.RFC3339Nano, *relatedSince)
			if err != nil {
				since, err = time.Parse("2006-01-02", *relatedSince)
			}
			if err != nil {
				return fail(fmt.Errorf("related-since must be YYYY-MM-DD or RFC3339"))
			}
		}
		if err := signalDigest.Validate(); err != nil {
			return fail(err)
		}
		if err := pdfLimits.Validate(); err != nil {
			return fail(err)
		}
		if *pdfFont != "" {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			if err := exporter.ValidatePDFFont(ctx, *pdfFont); err != nil {
				return fail(err)
			}
		}
		if *batch < 1 || *batch > 50 || *window < 1 {
			return fail(fmt.Errorf("batch-size must be 1–50 and window-ids positive"))
		}
		if _, err := time.LoadLocation(*zone); err != nil {
			return fail(fmt.Errorf("invalid IANA timezone"))
		}
		if args[0] == "export" && !*dryRun {
			absolute, e := filepath.Abs(*out)
			if e != nil {
				return fail(e)
			}
			*out = absolute
			for _, path := range []string{*out, *out + ".partial"} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					return fail(fmt.Errorf("output or staging directory already exists or cannot be inspected"))
				}
			}
		}

		p, dir, err := exporter.LoadPolicy(*policyPath)
		if err != nil {
			return fail(err)
		}
		scanner, err := exporter.NewScanner(p, dir)
		if err != nil {
			return fail(err)
		}
		if err := exporter.ValidateAssociations(*associations); err != nil {
			return fail(err)
		}
		selection, err := exporter.SelectScope(*scope, *materials)
		if err != nil {
			return fail(err)
		}
		selected := selection.Materials
		inventoried := selection.InventoryMaterials()
		if len(sdkCaches) > 0 {
			if err := exporter.ValidateSDKCacheMaterials(selected); err != nil {
				return fail(err)
			}
		}
		if args[0] == "export" && !*dryRun && *people != "all" {
			has := map[string]bool{}
			for _, m := range selected {
				has[m.Type] = true
			}
			if !has["PERSONS"] || !has["ANNOTATIONS"] {
				return fail(fmt.Errorf("focused people selection requires PERSONS and ANNOTATIONS in selected materials"))
			}
		}
		client, info, err := lifecycle.Connect(ctx, lifecycle.Options{BaseURL: *base, Environment: *environment, OSPath: *osPath, Launch: *launch, Timeout: *timeout, StartupTimeout: *startup, MaxBytes: *maxMiB << 20})
		if err != nil {
			return fail(err)
		}
		if err := client.ConfigurePerformance(*performance, *batch, *targetLatency, stderr); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Connected: %s (%s, version %s)\n", info.BaseURL, info.Environment, info.Version)
		if args[0] == "doctor" {
			fmt.Fprintln(stdout, "Pieces OS is ready. Collection access is checked by scan/export.")
			return 0
		}
		selection.Print(stdout)
		if args[0] == "benchmark" || *dryRun {
			if *dryRun {
				fmt.Fprintln(stdout, "Dry run: inventory and bounded reads only; no export directory or Desktop closure.")
				preflight, e := exporter.Scan(ctx, client, inventoried, stdout)
				if e != nil {
					return fail(e)
				}
				preflight.Print(stdout, "markdown")
			}
			// Default calibration covers large bodies and graph/identity families.
			calibration := inventoried
			if selection.Name == "all" {
				calibration, _ = exporter.SelectMaterials("WORKSTREAM_SUMMARIES,WORKSTREAM_EVENTS,PERSONS,ANNOTATIONS,TAGS")
			}
			if _, e := exporter.Benchmark(ctx, client, calibration, *benchmarkDuration, *benchmarkReads, stdout); e != nil {
				return fail(e)
			}
			if *peopleReport {
				if _, e := exporter.PeopleReport(ctx, client, *people, *minConnections, stdout); e != nil {
					return fail(e)
				}
			}
			if _, e := client.Probe(ctx); e != nil {
				return fail(e)
			}
			fmt.Fprintln(stdout, "Pieces OS responds after calibration. No source records were modified.")
			return 0
		}
		fmt.Fprintln(stdout, "Scanning retained local collections...")
		preflight, err := exporter.Scan(ctx, client, inventoried, stdout)
		if err != nil {
			return fail(err)
		}
		input := bufio.NewReader(stdin)
		if *format == "" {
			*format = "markdown"
			if args[0] == "export" && !*yes {
				fmt.Fprint(stdout, "Format: 1 Markdown [default], 2 PDF + Markdown companions, 3 Both: ")
				answer, err := readAnswer(ctx, input)
				if err != nil {
					fmt.Fprintln(stdout, "Canceled; no export created.")
					return 0
				}
				switch strings.ToLower(answer) {
				case "", "1", "markdown":
				case "2", "pdf":
					*format = "pdf"
				case "3", "both":
					*format = "both"
				default:
					return fail(fmt.Errorf("unknown format choice; use --format"))
				}
			}
		}
		preflight.Print(stdout, *format)
		fmt.Fprintf(stdout, "Association metadata: %s. Observed-pair lookups and selected person/event pages add work not included in the inventory estimate; they do not enumerate all associations. Scan/dry-run does not fetch this metadata.\n", *associations)
		for _, material := range selected {
			if material.Type == "SIGNALS" {
				fmt.Fprintf(stdout, "Signals digest: %s; up to %d signals per split document and %d MiB per document. Approved sizes are measured after privacy filtering; PDF budgets apply separately.\n", signalDigest.Mode, signalDigest.RecordsPerPart, signalDigest.MaxPartMiB)
				break
			}
		}
		if len(sdkCaches) > 0 {
			fmt.Fprintf(stdout, "Historical SDK caches selected: %d. Export may recover stale links to current records; it will remain partial. Caches are not applied during scan or dry run.\n", len(sdkCaches))
		}
		if *peopleReport {
			if _, e := exporter.PeopleReport(ctx, client, *people, *minConnections, stdout); e != nil {
				return fail(e)
			}
		}
		if args[0] == "scan" {
			if preflight.Unknown > 0 {
				return 2
			}
			return 0
		}
		fmt.Fprintf(stdout, "\nFormat: %s | Privacy: %s | Metadata: %s | Relationships: %s\nDestination: %s\n", *format, *mode, *metadata, *relationships, *out)
		fmt.Fprintf(stdout, "Related lists: %s, up to %d per section", *relatedOrder, *relatedLimit)
		if !since.IsZero() {
			fmt.Fprintf(stdout, ", since %s", since.UTC().Format(time.RFC3339))
		}
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "Performance: %s, ≤1 outstanding data request, batch ceiling %d, target %s | People: %s\n", *performance, *batch, *targetLatency, *people)
		fmt.Fprintln(stdout, "Website categories apply only if domain lists are configured in the policy.")
		if *closeDesktop {
			fmt.Fprintln(stdout, "After approval, Pieces Desktop will close gracefully. Pieces OS stays running.")
		}
		if !*yes {
			approved, err := confirm(ctx, input, stdout)
			if err != nil || !approved {
				fmt.Fprintln(stdout, "Canceled; no export created.")
				return 0
			}
		}
		if *closeDesktop {
			fmt.Fprintln(stderr, "Closing Pieces Desktop...")
			if err := lifecycle.CloseDesktop(ctx); err != nil {
				return fail(err)
			}
			if _, err := client.Probe(ctx); err != nil {
				return fail(fmt.Errorf("Pieces OS stopped responding after desktop closure"))
			}
		}
		manifest, err := exporter.Export(ctx, client, exporter.Options{Associations: *associations, Scope: selection.Name, ReferenceOnly: selection.ReferenceOnly, SDKCaches: sdkCaches, PeopleMode: *people, MinPersonConnections: *minConnections, Output: *out, Mode: *mode, Timezone: *zone, Version: version, Materials: selected, BatchSize: *batch, WindowIDs: *window, Scanner: scanner, Progress: stderr, PDFFont: *pdfFont, PDFLimits: *pdfLimits, SignalDigest: *signalDigest, Format: *format, Naming: *naming, Relationships: *relationships, Metadata: *metadata, RelatedOrder: *relatedOrder, RelatedLimit: *relatedLimit, RelatedSince: since})

		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Export written: %s\nStatus: %s\nRead index.md and manifest.json for content and coverage.\n", *out, manifest.Status)
		included, excluded, withheld, fetched, omitted := 0, 0, 0, 0, 0
		for _, coverage := range manifest.Coverage {
			included += coverage.Included
			excluded += coverage.Excluded
			withheld += coverage.Withheld
			fetched += coverage.Fetched
			omitted += coverage.Omitted
		}
		fmt.Fprintf(stdout, "Fetched: %d | Included: %d | Excluded: %d | Withheld: %d | Intentionally omitted: %d | Coverage issues: %d | Warnings: %d\nElapsed: %s\n", fetched, included, excluded, withheld, omitted, len(manifest.Issues), len(manifest.Warnings), manifest.Finished.Sub(manifest.Started).Round(time.Second))
		if manifest.Status == "partial" {
			return 2
		}
		return 0
	default:
		fmt.Fprint(stderr, help)
		return 1
	}
}

const help = `pieces-export — local Pieces OS export

Usage:
  pieces-export doctor [--base-url http://127.0.0.1:39300]
  pieces-export scan [--environment production|staging] [--launch-os=false]
  pieces-export benchmark [--benchmark-duration 30s] [--people-report]
  pieces-export export --dry-run [--people connected --people-report]
  pieces-export export --scope summaries --output ./my-summaries [--yes]
  pieces-export export --scope all --output ./my-export [--format markdown|pdf|both] [--yes]
  pieces-export export --output ./private-originals --mode preserve
  pieces-export rebuild --source ./finished-export --output ./rebuilt --format both
  pieces-export policy init --output policy.json
  pieces-export lists fetch --output lists --categories adult,bank
  pieces-export materials
  pieces-export version

Use a command followed by -h for flags. Export reads only loopback OS APIs.
Filtered mode embeds secret detection; no Python or separate scanner is required.
Website categories require local domain lists; they are not enabled by default.
Exit codes: 0 completed within implemented scope, 1 failed, 2 partial export.
Known coverage limits are always recorded in manifest.json.
`

func readAnswer(ctx context.Context, input *bufio.Reader) (string, error) {
	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)
	go func() { s, e := input.ReadString('\n'); ch <- result{strings.TrimSpace(s), e} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		return r.text, r.err
	}
}
func confirm(ctx context.Context, input *bufio.Reader, w io.Writer) (bool, error) {
	for {
		fmt.Fprint(w, "Export now? [Y/n] ")
		s, err := readAnswer(ctx, input)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(w, "Enter Y or n.")
		}
	}
}
