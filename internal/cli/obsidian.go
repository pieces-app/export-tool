package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/pieces-app/export-tool/internal/exporter"
)

func obsidian(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obsidian", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", "finalized Pieces export; conversion retains its existing privacy decisions")
	output := fs.String("output", "", "new vault directory outside the source (required)")
	yes := fs.Bool("yes", false, "create the vault without an interactive confirmation")
	fs.BoolVar(yes, "y", false, "create the vault without an interactive confirmation")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	fail := func(err error) int { fmt.Fprintln(stderr, "Error:", err); return 1 }
	if fs.NArg() != 0 || *source == "" || *output == "" {
		return fail(fmt.Errorf("obsidian requires --source and a new --output directory"))
	}
	m, err := exporter.InspectArchive(*source)
	if err != nil {
		return fail(err)
	}
	destination, err := filepath.Abs(*output)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "Create a separate Obsidian vault from the finalized export.\nSource status: %s | Privacy: %s\nDestination: %s\nNo Pieces OS connection or source edits. Existing privacy decisions are retained; conversion does not re-scan manually edited content.\n", m.Status, m.Mode, destination)
	if !*yes {
		approved, err := confirm(ctx, bufio.NewReader(stdin), stdout)
		if err != nil || !approved {
			fmt.Fprintln(stdout, "Canceled; no vault created.")
			return 0
		}
	}
	m, err = exporter.ConvertObsidian(ctx, *source, destination, stderr)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "Obsidian vault written: %s\nNotes: %d | Status: %s\nIn Obsidian choose Open folder as vault, select the vault subfolder, then open Start Here.md.\n", destination, m.Obsidian.Notes, m.Status)
	if m.Status == "partial" {
		return 2
	}
	return 0
}
