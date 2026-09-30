package exporter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/signintech/gopdf"
)

// PDFLimits apply to each document, not the whole archive. These bound inputs
// and accumulated output; they are not a process RSS or wall-clock guarantee.
type PDFLimits struct {
	InputMiB  int `json:"input_mib"`
	Pages     int `json:"pages"`
	OutputMiB int `json:"output_mib"`
}

func DefaultPDFLimits() PDFLimits { return PDFLimits{InputMiB: 16, Pages: 1000, OutputMiB: 64} }

func (l PDFLimits) Validate() error {
	if l.InputMiB < 1 || l.InputMiB > 128 || l.Pages < 1 || l.Pages > 10000 || l.OutputMiB < 1 || l.OutputMiB > 512 {
		return errConfig("PDF limits require pdf-max-input-mib 1–128, pdf-max-pages 1–10000, and pdf-max-output-mib 1–512")
	}
	return nil
}

func (l PDFLimits) defaults() PDFLimits {
	d := DefaultPDFLimits()
	if l.InputMiB == 0 {
		l.InputMiB = d.InputMiB
	}
	if l.Pages == 0 {
		l.Pages = d.Pages
	}
	if l.OutputMiB == 0 {
		l.OutputMiB = d.OutputMiB
	}
	return l
}

func pdfLimitError(flag string, limit int) error {
	return errConfig(fmt.Sprintf("PDF document exceeds --%s=%d; no archive was finalized. Use --format markdown or raise the PDF limit and retry with a new output folder; partial folders cannot be resumed", flag, limit))
}

func readPDFInput(ctx context.Context, path string, limit int64) ([]byte, error) {
	return readPDFBytes(ctx, path, limit, pdfLimitError("pdf-max-input-mib", int(limit>>20)))
}

func readPDFBytes(ctx context.Context, path string, limit int64, boundError error) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Refuse pipes/devices before opening them; caller-supplied font paths
	// must not block waiting for a producer. Recheck the opened descriptor.
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errConfig("PDF input must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errConfig("PDF input must be a regular file")
	}
	if st.Size() > limit {
		return nil, boundError
	}
	var b bytes.Buffer
	buf := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := f.Read(buf)
		if int64(b.Len()+n) > limit {
			return nil, boundError
		}
		b.Write(buf[:n])
		if err == io.EOF {
			return b.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// gopdf's compiler ignores errors returned by some writes. Latch the first
// failure and refuse all later writes so cancellation/limits cannot be hidden.
type pdfOutputBuffer struct {
	buffer bytes.Buffer
	ctx    context.Context
	limit  int
	err    error
}

func (b *pdfOutputBuffer) Len() int      { return b.buffer.Len() }
func (b *pdfOutputBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *pdfOutputBuffer) Write(p []byte) (int, error) {
	if b.err == nil {
		b.err = b.ctx.Err()
	}
	if b.err == nil && len(p) > b.limit-b.Len() {
		b.err = pdfLimitError("pdf-max-output-mib", b.limit>>20)
	}
	if b.err != nil {
		return 0, b.err
	}
	return b.buffer.Write(p)
}

// Keep the library's wrapping and font metrics, but feed bounded UTF-8 chunks.
// The final wrapped line is carried into the next chunk, so chunk boundaries do
// not add line breaks. Only the short keep-together prefix is buffered upstream.
func walkPDFLines(ctx context.Context, pdf *gopdf.GoPdf, value string, width float64, emit func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !utf8.ValidString(value) {
		return errConfig("PDF text is not valid UTF-8")
	}
	checked := map[rune]bool{}
	for paragraph := range strings.SplitSeq(value, "\n") {
		if err := ctx.Err(); err != nil {
			return err
		}
		if paragraph == "" {
			if err := emit(""); err != nil {
				return err
			}
			continue
		}
		pending := ""
		for len(paragraph) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := min(len(paragraph), 512)
			for end < len(paragraph) && !utf8.RuneStart(paragraph[end]) {
				end--
			}
			part := strings.ReplaceAll(paragraph[:end], "\t", "    ")
			paragraph = paragraph[end:]
			// A glyph wider than the line makes the library's strict-break loop
			// retry forever. Check each distinct glyph at this font size first.
			for _, c := range part {
				if !checked[c] {
					w, err := pdf.MeasureTextWidth(string(c))
					if err != nil {
						return err
					}
					if w < 0 || w > width || math.IsNaN(w) || math.IsInf(w, 0) {
						return errConfig("PDF font has unsupported glyph widths; select another --pdf-font or --format markdown")
					}
					checked[c] = true
				}
			}
			pending += part
			if len(pending) > 8192 {
				return errConfig("PDF line exceeds its 8192-byte layout bound; select another --pdf-font or --format markdown")
			}
			lines, err := pdf.SplitTextWithWordWrap(pending, width)
			if err != nil {
				return err
			}
			for _, line := range lines[:len(lines)-1] {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := emit(line); err != nil {
					return err
				}
			}
			pending = lines[len(lines)-1]
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(pending); err != nil {
			return err
		}
	}
	return nil
}
