package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"time"

	"github.com/pieces-app/export-tool/internal/exporter"
)

func rebuild(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	fail := func(err error) int { fmt.Fprintln(stderr, "Error:", err); return 1 }
	fs := flag.NewFlagSet("rebuild", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", "finalized archive to read; active .partial directories are rejected")
	out := fs.String("output", "pieces-rebuilt-"+time.Now().Format("20060102-150405"), "new output folder outside the source archive")
	policy := fs.String("policy", "", "original privacy policy; category files must match the source hashes")
	yes := fs.Bool("yes", false, "approve the offline rebuild without a prompt")
	fs.BoolVar(yes, "y", false, "approve the offline rebuild without a prompt")
	format := fs.String("format", "", "markdown, pdf, or both; default inherits the archive setting")
	zone := fs.String("timezone", "", "IANA timezone; default inherits the archive setting")
	naming := fs.String("naming", "", "readable or opaque; default inherits the archive setting")
	relationships := fs.String("relationships", "", "inline, sidecar, or both; default inherits the archive setting")
	metadata := fs.String("metadata", "", "auto or off; default inherits the archive setting; sidecars always retained")
	font := fs.String("pdf-font", "", "optional local TrueType font")
	pdfLimits := pdfLimitFlags(fs)
	signalDigest := signalDigestFlags(fs, true)
	people := fs.String("people", "", "inherit original selection, or narrow an all-people archive to profiles/connected")
	relatedOrder := fs.String("related-order", "", "relevance or recent; default inherits the archive setting")
	relatedLimit := fs.Int("related-limit", 0, "maximum suggestions per dimension (1–500); default inherits the archive setting")
	var caches []string
	fs.Func("sdk-cache", "optional historical SDK cache; repeat up to eight times; partial status remains", func(path string) error {
		caches = append(caches, path)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		return fail(fmt.Errorf("unexpected positional arguments"))
	}
	if err := signalDigest.Validate(); err != nil {
		return fail(err)
	}
	if err := pdfLimits.Validate(); err != nil {
		return fail(err)
	}
	m, err := exporter.InspectArchive(*source)
	if err != nil {
		return fail(err)
	}
	p, dir, err := exporter.LoadPolicy(*policy)
	if err != nil {
		return fail(err)
	}
	scanner, err := exporter.NewScanner(p, dir)
	if err != nil {
		return fail(err)
	}
	if m.Mode == "filtered" && (m.PolicyHash != scanner.Hash || !maps.Equal(m.CategoryHashes, scanner.ListHashes)) {
		return fail(fmt.Errorf("rebuild requires the original privacy policy and unchanged domain lists"))
	}
	if err := exporter.ValidateSDKCaches(ctx, caches); err != nil {
		return fail(err)
	}
	included := 0
	for _, c := range m.Coverage {
		included += c.Included
	}
	fmt.Fprintf(stdout, "Offline rebuild: %d included records; privacy mode %s; %d original coverage issues.\nDestination: %s\n", included, m.Mode, len(m.Issues), *out)
	fmt.Fprintln(stdout, "No OS connection, launch, or Desktop closure. Source omissions remain; missing records cannot be downloaded offline.")
	digestMode := signalDigest.Mode
	if digestMode == "" {
		digestMode = "split"
		if m.SignalDigest != nil {
			digestMode = m.SignalDigest.Options.Mode
		}
	}
	fmt.Fprintf(stdout, "Signals digest: %s when signals are selected; up to %d signals per split document, %d MiB per document. Approved sizes are measured after privacy filtering.\n", digestMode, signalDigest.RecordsPerPart, signalDigest.MaxPartMiB)
	if m.ArchiveState == nil {
		fmt.Fprintln(stdout, "Legacy archive: original relationship/selection evidence is incomplete; rebuilt output will remain partial.")
	}
	if len(caches) > 0 {
		fmt.Fprintf(stdout, "Historical SDK caches selected: %d. Missing selected dependencies are treated conservatively; recovery may withhold additional generated content.\n", len(caches))
	}
	if !*yes {
		approved, err := confirm(ctx, bufio.NewReader(stdin), stdout)
		if err != nil || !approved {
			fmt.Fprintln(stdout, "Canceled; no rebuilt archive created.")
			return 0
		}
	}
	m, err = exporter.Rebuild(ctx, exporter.RebuildOptions{Source: *source, Options: exporter.Options{Output: *out, Scanner: scanner, SDKCaches: caches, Format: *format, Timezone: *zone, Naming: *naming, Relationships: *relationships, Metadata: *metadata, PDFFont: *font, PDFLimits: *pdfLimits, SignalDigest: *signalDigest, PeopleMode: *people, RelatedOrder: *relatedOrder, RelatedLimit: *relatedLimit, Version: version, Progress: stderr}})
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "Rebuilt archive written: %s\nStatus: %s\nRead index.md, coverage.md, and manifest.json.\n", *out, m.Status)
	if m.Status == "partial" {
		return 2
	}
	return 0
}
