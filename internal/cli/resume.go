package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/pieces-app/export-tool/internal/exporter"
)

func recoveryQuote(s string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func printRecoveryHint(w io.Writer, o *exporter.RecoveryOptions) {
	if o == nil {
		return
	}
	fmt.Fprintln(w, "Recovery workspace and keys are retained if created. Inspect whether source capture completed:")
	fmt.Fprintf(w, "  pieces-export resume --work %s --recovery-keys %s --inspect\n", recoveryQuote(o.Directory), recoveryQuote(o.KeyDirectory))
}

func resume(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, version string) (result int) {
	fail := func(err error) int { fmt.Fprintln(stderr, "Error:", err); return 1 }
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(stderr)
	work := fs.String("work", "", "existing encrypted recovery workspace")
	keys := fs.String("recovery-keys", "", "matching private key directory, outside all archives")
	out := fs.String("output", "pieces-export-recovered-"+time.Now().Format("20060102-150405"), "new output directory; original output/partial folder is never reused")
	inspect := fs.Bool("inspect", false, "verify recovery state and report aggregates without contacting OS or creating an archive")
	yes := fs.Bool("yes", false, "approve replay without prompting")
	fs.BoolVar(yes, "y", false, "approve replay without prompting")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		return fail(fmt.Errorf("unexpected positional arguments"))
	}
	o := exporter.RecoveryOptions{Directory: *work, KeyDirectory: *keys}
	if !*inspect {
		if *out == "" {
			return fail(fmt.Errorf("resume requires a new --output directory"))
		}
		if err := exporter.ValidateRecoveryOptions(o, *out, false); err != nil {
			return fail(err)
		}
	}
	fmt.Fprintln(stderr, "Verifying encrypted recovery state and acquiring exclusive ownership...")
	session, err := exporter.OpenRecovery(ctx, o)
	if err != nil {
		return fail(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			fmt.Fprintln(stderr, "Error: recovery workspace could not be cleanly closed; retain its files and any exported archive.")
			result = 1
		}
	}()
	info := session.Info()
	fmt.Fprintf(stdout, "Recovery phase: %s | Generation: %d | Encrypted items: %d\n", info.Phase, info.Generation, info.StoredItems)
	fmt.Fprintln(stdout, "Local replay only: no OS connection, launch, Desktop closure, or source cache reads.")
	if info.CanResume {
		fmt.Fprintf(stdout, "Captured records: %d | Included before final privacy: %d | Excluded: %d | Withheld: %d | Missing: %d | Source issues: %d\n", info.Records, info.Included, info.Excluded, info.Withheld, info.Missing, info.Issues)
		fmt.Fprintf(stdout, "Scope: %s | People: %s | Privacy: %s | Format: %s\nSource collection started: %s | Captured: %s\n", info.Scope, info.PeopleMode, info.Mode, info.Format, info.SourceStarted.Format(time.RFC3339), info.CapturedAt.Format(time.RFC3339))
		fmt.Fprintln(stdout, "Ready to replay local processing. Original omissions remain; privacy, paths, documents and validation will run again.")
	} else {
		fmt.Fprintln(stdout, "Not ready to resume: source capture is incomplete. Interrupted fetching cannot resume yet. Retain these files; a new export needs a new workspace and output.")
	}
	if *inspect {
		return 0
	}
	if !info.CanResume {
		return fail(fmt.Errorf("no complete source capture is available for replay"))
	}
	if err := session.ValidateOutput(*out); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "New destination: %s\n", *out)
	fmt.Fprintln(stdout, "Original output, workspace and separate keys are retained. Keep recovery directories private and outside the shareable export.")
	if !*yes {
		approved, err := confirm(ctx, bufio.NewReader(stdin), stdout)
		if err != nil || !approved {
			fmt.Fprintln(stdout, "Canceled; no replay output created.")
			return 0
		}
	}
	started := time.Now()
	m, err := session.Replay(ctx, *out, stderr, version)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "Recovered archive written: %s\nStatus: %s\nLocal replay elapsed: %s | Current OS requests: %d\nRead index.md, coverage.md and manifest.json.\n", *out, m.Status, time.Since(started).Round(time.Second), m.Performance.Requests)
	if m.Status == "partial" {
		return 2
	}
	return 0
}
