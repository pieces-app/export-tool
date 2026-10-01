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
	"github.com/pieces-app/export-tool/internal/lifecycle"
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
	fmt.Fprintln(w, "Recovery workspace and keys are retained if created. Inspect saved source progress:")
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
	base := fs.String("base-url", "", "OS loopback URL for unfinished source fetching; otherwise discover")
	environment := fs.String("environment", "auto", "auto, production, or staging; saved source identity must still match")
	launch := fs.Bool("launch-os", true, "launch an installed OS if unfinished source fetching needs it")
	osPath := fs.String("os-path", "", "explicit OS executable or macOS app bundle")
	timeout := fs.Duration("timeout", 60*time.Second, "timeout per OS request")
	startup := fs.Duration("startup-timeout", 2*time.Minute, "OS readiness deadline")
	maxMiB := fs.Int64("max-response-mib", 64, "maximum OS response size in MiB")
	yes := fs.Bool("yes", false, "approve recovery without prompting")
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
	if *timeout <= 0 || *startup <= 0 || *maxMiB < 1 || *maxMiB > 1024 || (*environment != "auto" && *environment != "production" && *environment != "staging") {
		return fail(fmt.Errorf("invalid recovery connection settings"))
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
	if info.NeedsSource {
		fmt.Fprintf(stdout, "Ready to resume source fetching. Saved snapshots: %d | Scope: %s | Privacy: %s\n", info.StoredItems, info.Scope, info.Mode)
		fmt.Fprintln(stdout, "After approval, reconnect to the same OS installation and user. Recheck record freshness, repeat relationship/inventory reads, and write a new archive. Desktop will remain open.")
	} else if info.CanResume {
		fmt.Fprintln(stdout, "Local replay only: no OS connection, launch, Desktop closure, or source cache reads.")
		fmt.Fprintf(stdout, "Captured records: %d | Included before final privacy: %d | Excluded: %d | Withheld: %d | Missing: %d | Source issues: %d\n", info.Records, info.Included, info.Excluded, info.Withheld, info.Missing, info.Issues)
		fmt.Fprintf(stdout, "Scope: %s | People: %s | Privacy: %s | Format: %s\nSource collection started: %s | Captured: %s\n", info.Scope, info.PeopleMode, info.Mode, info.Format, info.SourceStarted.Format(time.RFC3339), info.CapturedAt.Format(time.RFC3339))
		fmt.Fprintln(stdout, "Ready to replay local processing. Original omissions remain; privacy, paths, documents and validation will run again.")
	} else {
		fmt.Fprintln(stdout, "This older or uninitialized checkpoint has no resumable source batches. Retain it; a new export needs a new workspace and output.")
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
	var m exporter.Manifest
	if info.NeedsSource {
		client, _, e := lifecycle.Connect(ctx, lifecycle.Options{BaseURL: *base, Environment: *environment, OSPath: *osPath, Launch: *launch, Timeout: *timeout, StartupTimeout: *startup, MaxBytes: *maxMiB << 20})
		if e != nil {
			return fail(e)
		}
		if e = client.ConfigurePerformance("adaptive", 50, 250*time.Millisecond, stderr); e != nil {
			return fail(e)
		}
		fmt.Fprintln(stderr, "Verifying saved source identity and snapshot freshness...")
		m, err = session.Continue(ctx, client, *out, stderr, version)
	} else {
		m, err = session.Replay(ctx, *out, stderr, version)
	}
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "Recovered archive written: %s\nStatus: %s\nRecovery elapsed: %s | Current OS requests: %d\nRead index.md, coverage.md and manifest.json.\n", *out, m.Status, time.Since(started).Round(time.Second), m.Performance.Requests)
	if info.NeedsSource && m.SourceRecovery != nil {
		fmt.Fprintf(stdout, "Source snapshots reused: %d | Fresh snapshots saved: %d\n", m.SourceRecovery.ReusedRecords, m.SourceRecovery.FreshRecords)
	}
	if m.Status == "partial" {
		return 2
	}
	return 0
}
