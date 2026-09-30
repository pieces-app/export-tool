package cli

import (
	"flag"

	"github.com/pieces-app/export-tool/internal/exporter"
)

func pdfLimitFlags(fs *flag.FlagSet) *exporter.PDFLimits {
	l := exporter.DefaultPDFLimits()
	fs.IntVar(&l.InputMiB, "pdf-max-input-mib", l.InputMiB, "maximum Markdown input per PDF (1–128 MiB); excess fails without finalizing")
	fs.IntVar(&l.Pages, "pdf-max-pages", l.Pages, "maximum pages per PDF (1–10000); excess fails without truncating")
	fs.IntVar(&l.OutputMiB, "pdf-max-output-mib", l.OutputMiB, "maximum bytes per PDF (1–512 MiB); excess fails without finalizing")
	return &l
}
