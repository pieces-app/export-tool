package cli

import (
	"flag"

	"github.com/pieces-app/export-tool/internal/exporter"
)

func signalDigestFlags(fs *flag.FlagSet, rebuild bool) *exporter.SignalDigestOptions {
	o := exporter.DefaultSignalDigestOptions()
	help := "split (default), single, or off; canonical signal files are always retained"
	if rebuild {
		o.Mode = ""
		help = "split, single, or off; inherit mode when recorded, otherwise split"
	}
	fs.StringVar(&o.Mode, "signals-digest", o.Mode, help)
	fs.IntVar(&o.RecordsPerPart, "signals-per-part", o.RecordsPerPart, "maximum signals per split digest document (1–10000)")
	fs.IntVar(&o.MaxPartMiB, "signals-max-part-mib", o.MaxPartMiB, "maximum digest document size (1–128 MiB); oversized single entries fail without truncation")
	return &o
}
