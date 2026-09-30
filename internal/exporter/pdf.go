package exporter

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	pdfread "github.com/ledongthuc/pdf"
	"github.com/signintech/gopdf"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/text/encoding/charmap"
)

func pdfPath(path string) string {
	if strings.HasPrefix(path, "markdown/") {
		path = "pdf/" + strings.TrimPrefix(path, "markdown/")
	} else if path != "index.md" {
		path = "pdf/" + path
	}
	return strings.TrimSuffix(path, ".md") + ".pdf"
}

type pdfLink struct{ Label, Destination string }
type pdfBlock struct {
	Text    string
	Size    float64
	Code    bool
	Heading bool
	Links   []pdfLink
}

// Extract displayed inline text, omitting Markdown punctuation and destinations.
func inlineText(ctx context.Context, n ast.Node, data []byte) string {
	var b strings.Builder
	var visit func(ast.Node, bool)
	visit = func(node ast.Node, code bool) {
		if ctx.Err() != nil {
			return
		}
		switch item := node.(type) {
		case *ast.CodeSpan:
			for c := item.FirstChild(); c != nil; c = c.NextSibling() {
				visit(c, true)
			}
			return
		case *ast.Text:
			value := item.Segment.Value(data)
			if code {
				b.Write(value)
			} else {
				b.WriteString(html.UnescapeString(string(util.UnescapePunctuations(value))))
			}
			if item.SoftLineBreak() || item.HardLineBreak() {
				b.WriteByte('\n')
			}
			return
		case *ast.String:
			b.WriteString(html.UnescapeString(string(util.UnescapePunctuations(item.Value))))
			return
		case *ast.AutoLink:
			b.Write(item.Label(data))
			return
		case *ast.RawHTML:
			for i := 0; i < item.Segments.Len(); i++ {
				if ctx.Err() != nil {
					return
				}
				segment := item.Segments.At(i)
				b.Write(segment.Value(data))
			}
			return
		}
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c, code)
		}
	}
	visit(n, false)
	return b.String()
}

func ValidatePDFFont(ctx context.Context, path string) error {
	font, err := readPDFFont(ctx, path)
	if err != nil {
		return err
	}
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	return addPDFFont(ctx, &pdf, font, gopdf.TtfOption{})
}

func readPDFFont(ctx context.Context, path string) ([]byte, error) {
	font, err := readPDFBytes(ctx, path, 32<<20, errConfig("PDF font exceeds 32 MiB"))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, errConfig("PDF font unreadable or larger than 32 MiB")
	}
	return font, nil
}

// Parsing is bounded by font bytes but the library is synchronous; cancellation
// cannot preempt it mid-call. Recover malformed-font panics as ordinary errors.
func addPDFFont(ctx context.Context, pdf *gopdf.GoPdf, data []byte, option gopdf.TtfOption) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if recover() != nil {
			err = errConfig("PDF font is not a supported TrueType font")
		}
	}()
	if err := pdf.AddTTFFontDataWithOption("body", data, option); err != nil {
		return errConfig("PDF font is not a supported TrueType font")
	}
	return ctx.Err()
}

// Only Markdown text and link destinations enter the PDF. No HTML execution,
// remote images, filesystem image reads, or imported source PDFs are enabled.
func markdownBlocks(ctx context.Context, data []byte) ([]pdfBlock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(data))
	blocks := []pdfBlock{}
	err := ast.Walk(root, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !enter {
			return ast.WalkContinue, nil
		}
		block := pdfBlock{Size: 11}
		switch node := n.(type) {
		case *ast.Heading:
			block.Size = max(12, float64(24-node.Level*2))
			block.Heading = true
			block.Text = inlineText(ctx, node, data)
		case *ast.Paragraph:
			block.Text = inlineText(ctx, node, data)
		case *ast.TextBlock:
			block.Text = inlineText(ctx, node, data)
		case *ast.FencedCodeBlock:
			block.Code = true
			block.Size = 9
		case *ast.CodeBlock:
			block.Code = true
			block.Size = 9
		case *ast.HTMLBlock:
			block.Code = true
			block.Size = 9
		default:
			if n.Kind().String() == "TableRow" || n.Kind().String() == "TableHeader" {
				cells := []string{}
				for cell := n.FirstChild(); cell != nil; cell = cell.NextSibling() {
					if err := ctx.Err(); err != nil {
						return ast.WalkStop, err
					}
					cells = append(cells, inlineText(ctx, cell, data))
				}
				block.Text = strings.Join(cells, " | ")
			} else {
				return ast.WalkContinue, nil
			}
		}
		if block.Code {
			var b strings.Builder
			for i := 0; i < n.Lines().Len(); i++ {
				if err := ctx.Err(); err != nil {
					return ast.WalkStop, err
				}
				line := n.Lines().At(i)
				b.Write(line.Value(data))
			}
			block.Text = b.String()
		}
		if err := ast.Walk(n, func(child ast.Node, in bool) (ast.WalkStatus, error) {
			if err := ctx.Err(); err != nil {
				return ast.WalkStop, err
			}
			if in {
				if link, ok := child.(*ast.Link); ok {
					block.Links = append(block.Links, pdfLink{inlineText(ctx, link, data), string(link.Destination)})
				}
			}
			return ast.WalkContinue, nil
		}); err != nil {
			return ast.WalkStop, err
		}
		if n.Parent() != nil && n.Parent().Kind() == ast.KindListItem {
			block.Text = "• " + block.Text
		}
		if strings.TrimSpace(block.Text) != "" {
			blocks = append(blocks, block)
		}
		return ast.WalkSkipChildren, nil
	})
	return blocks, err
}

func (r *run) renderPDFs() error {
	limits := r.opts.PDFLimits.defaults()
	if err := limits.Validate(); err != nil {
		return err
	}
	r.manifest.PDFLimits = &limits
	if r.opts.Progress != nil {
		fmt.Fprintln(r.opts.Progress, "Converting approved Markdown to PDFs...")
	}
	paths := []string{}
	err := filepath.WalkDir(r.stage, func(path string, d fs.DirEntry, err error) error {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".md" {
			rel, e := filepath.Rel(r.stage, path)
			if e != nil {
				return e
			}
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	r.progress.Stage("Render PDFs", len(paths))
	for _, path := range paths {
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		data, err := readPDFInput(r.ctx, filepath.Join(r.stage, path), int64(limits.InputMiB)<<20)
		if err != nil {
			return err
		}
		target := pdfPath(path)
		meta := r.documentMetadata[path]
		missing, err := r.writePDF(path, target, data, meta)
		if err != nil {
			return err
		}
		if missing > 0 {
			r.manifest.Warnings = append(r.manifest.Warnings, fmt.Sprintf("A PDF substituted %d unsupported glyphs with '?'; complete Unicode text remains in its Markdown companion.", missing))
			r.issue("PDF", "", "unsupported_font_glyphs")
		}
		if meta != nil {
			r.documentMetadata[target] = meta
		}
		r.progress.Add(1)
	}
	// Some destinations are rendered later in the loop. Check their existence
	// after all PDFs have been written, including in unfiltered mode.
	for _, path := range paths {
		if err := r.auditPDFFile(filepath.Join(r.stage, pdfPath(path)), true, false); err != nil {
			return err
		}
	}
	return nil
}
func (r *run) writePDF(source, target string, data []byte, meta *DocumentMetadata) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	limits := r.opts.PDFLimits.defaults()
	if err := limits.Validate(); err != nil {
		return 0, err
	}
	if len(data) > limits.InputMiB<<20 {
		return 0, pdfLimitError("pdf-max-input-mib", limits.InputMiB)
	}
	if meta != nil {
		size := len(meta.Title) + len(meta.Description)
		for _, tag := range meta.NativeTags {
			size += len(tag) + 2
		}
		if size > limits.InputMiB<<20 {
			return 0, pdfLimitError("pdf-max-input-mib", limits.InputMiB)
		}
	}
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	pdf.SetMargins(44, 44, 44, 44)
	slots := pdfActionSlots{}
	unsupportedLinks := map[string]bool{}
	missing := map[rune]bool{}
	option := gopdf.TtfOption{OnGlyphNotFound: func(c rune) { missing[c] = true }, OnGlyphNotFoundSubstitute: func(rune) rune { return '?' }}
	font := goregular.TTF
	if r.opts.PDFFont != "" {
		var err error
		font, err = readPDFFont(r.ctx, r.opts.PDFFont)
		if err != nil {
			return 0, err
		}
	}
	if err := addPDFFont(r.ctx, &pdf, font, option); err != nil {
		return 0, err
	}
	info := gopdf.PdfInfo{Title: "Pieces export", Creator: "Pieces Export"}
	if meta != nil {
		info.Title = meta.Title
		info.Subject = meta.Description

	}
	pdf.SetInfo(info)
	annotationBytes := 0
	page := 0
	y := float64(44)
	newPage := func() error {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if page >= limits.Pages {
			return pdfLimitError("pdf-max-pages", limits.Pages)
		}
		pdf.AddPage()
		page++
		if page%25 == 0 && r.opts.Progress != nil {
			fmt.Fprintf(r.opts.Progress, "PDF progress: current document %d pages\n", page)
		}
		y = 44
		if err := pdf.SetFont("body", "", 9); err != nil {
			return err
		}
		pdf.SetTextColor(95, 103, 115)
		pdf.SetXY(44, 807)
		if err := pdf.Text(fmt.Sprintf("Pieces export  |  %d", page)); err != nil {
			return err
		}
		pdf.SetTextColor(24, 33, 48)
		return nil
	}
	if err := newPage(); err != nil {
		return 0, err
	}
	write := func(value string, size float64, destination string, heading bool) error {
		if err := pdf.SetFont("body", "", size); err != nil {
			return err
		}
		writeLine := func(line string) error {
			if err := r.ctx.Err(); err != nil {
				return err
			}
			if y+size*1.5 > 785 {
				if err := newPage(); err != nil {
					return err
				}
				if err := pdf.SetFont("body", "", size); err != nil {
					return err
				}
			}
			if destination != "" {
				pdf.SetTextColor(27, 86, 169)
			} else {
				pdf.SetTextColor(24, 33, 48)
			}
			pdf.SetXY(44, y+size)
			if err := pdf.Text(line); err != nil {
				return err
			}
			if destination != "" {
				width, err := pdf.MeasureTextWidth(line)
				if err != nil {
					return err
				}
				// Budget annotation payloads before the library and action-slot
				// map retain them, including repeated wrapped-link annotations.
				annotationBytes += len(destination) + 256
				if annotationBytes > limits.OutputMiB<<20 {
					return pdfLimitError("pdf-max-output-mib", limits.OutputMiB)
				}
				annotation := destination
				if strings.HasPrefix(destination, "/A <<") {
					annotation = slots.reserve(destination)
				}
				pdf.AddExternalLink(annotation, 44, y, width, size*1.5)
			}
			y += size * 1.5
			return nil
		}
		// Buffer only a page-sized prefix for keep-together layout. Large
		// paragraphs/code blocks stream into the renderer and check context.
		pending := []string{}
		streaming := false
		flush := func() error {
			for _, line := range pending {
				if err := writeLine(line); err != nil {
					return err
				}
			}
			pending = nil
			return nil
		}
		height := func() float64 {
			h := float64(len(pending))*size*1.5 + 6
			if heading {
				h += 60
			}
			return h
		}
		if err := walkPDFLines(r.ctx, &pdf, value, 507, func(line string) error {
			if streaming {
				return writeLine(line)
			}
			pending = append(pending, line)
			if height() >= 741 {
				streaming = true
				return flush()
			}
			return nil
		}); err != nil {
			return err
		}
		if !streaming {
			if y+height() > 785 && y > 44 {
				if err := newPage(); err != nil {
					return err
				}
				if err := pdf.SetFont("body", "", size); err != nil {
					return err
				}
			}
			if err := flush(); err != nil {
				return err
			}
		}
		y += 6
		return nil
	}
	blocks, err := markdownBlocks(r.ctx, data)
	if err != nil {
		return 0, err
	}
	for _, block := range blocks {
		if r.ctx.Err() != nil {
			return 0, r.ctx.Err()
		}
		links := []pdfLink{}
		for _, link := range block.Links {
			if err := r.ctx.Err(); err != nil {
				return 0, err
			}
			u, err := url.Parse(link.Destination)
			if err != nil {
				continue
			}
			destination := link.Destination
			if !u.IsAbs() && u.Host == "" && u.Path != "" && !strings.Contains(u.Path, "\\") && !strings.HasPrefix(u.Path, "/") {
				resolved := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(source), filepath.FromSlash(u.Path))))
				if resolved == ".." || strings.HasPrefix(resolved, "../") {
					return 0, errConfig("PDF link leaves export directory")
				}
				// JSON and other non-document links remain visible labels. Local
				// PDF navigation opens the mirrored PDF, at its first page.
				if filepath.Ext(resolved) != ".md" {
					continue
				}
				if st, err := os.Stat(filepath.Join(r.stage, resolved)); err != nil || !st.Mode().IsRegular() {
					return 0, errConfig("PDF Markdown companion is missing")
				}
				rel, err := filepath.Rel(filepath.Dir(filepath.FromSlash(target)), filepath.FromSlash(pdfPath(resolved)))
				if err != nil {
					return 0, errConfig("PDF link could not be resolved")
				}
				var supported bool
				destination, supported = pdfFileAction(filepath.ToSlash(rel))
				if !supported {
					unsupportedLinks[resolved] = true
					continue
				}
			} else if !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
				continue
			}
			links = append(links, pdfLink{link.Label, destination})
		}
		if len(links) == 1 && len(block.Links) == 1 && strings.TrimSpace(strings.TrimPrefix(block.Text, "• ")) == strings.TrimSpace(links[0].Label) {
			// A standalone link (including a list bullet) can be clicked directly.
			// Surrounding prose must stay plain: an unavailable relationship label
			// in that prose must never inherit this link's destination.
			if err := write(block.Text, block.Size, links[0].Destination, block.Heading); err != nil {
				return 0, err
			}
		} else {
			if err := write(block.Text, block.Size, "", block.Heading); err != nil {
				return 0, err
			}
			for _, link := range links {
				if err := write("Open: "+link.Label, 10, link.Destination, false); err != nil {
					return 0, err
				}
			}
		}
	}
	output := pdfOutputBuffer{ctx: r.ctx, limit: limits.OutputMiB << 20}
	if err := pdf.Write(&output); err != nil {
		return 0, err
	}
	if output.err != nil {
		return 0, output.err
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	pdfBytes, err := slots.apply(output.Bytes())
	if err != nil {
		return 0, err
	}
	pdfBytes, err = pdfInformation(pdfBytes, meta)
	if err != nil {
		return 0, err
	}
	if len(pdfBytes) > limits.OutputMiB<<20 {
		return 0, pdfLimitError("pdf-max-output-mib", limits.OutputMiB)
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if err := r.writeFile(filepath.Join(r.stage, target), pdfBytes); err != nil {
		return 0, err
	}
	if err := r.auditPDFFile(filepath.Join(r.stage, target), false, true); err != nil {
		return 0, err
	}
	if len(unsupportedLinks) > 0 {
		r.manifest.Warnings = append(r.manifest.Warnings, fmt.Sprintf("A PDF left %d local destinations as plain text because their filenames cannot be represented in Preview's legacy file encoding; full links remain in Markdown.", len(unsupportedLinks)))
		r.issue("PDF", "", "unsupported_pdf_link_filename")
	}
	return len(missing), nil
}

// Scan decoded page text, document metadata, and links; compressed binary bytes
// and font programs are not meaningful inputs for a text secret detector.
func (r *run) auditPDF(path string) error {
	return r.auditPDFFile(path, true, true)
}

func (r *run) auditPDFFile(path string, targetsExist, content bool) (err error) {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if recover() != nil {
			err = errConfig("generated PDF could not be validated")
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	limits := r.opts.PDFLimits.defaults()
	if st.Size() > int64(limits.OutputMiB)<<20 {
		return pdfLimitError("pdf-max-output-mib", limits.OutputMiB)
	}
	pdf, err := pdfread.NewReader(f, st.Size())
	if err != nil {
		return errConfig("generated PDF could not be parsed")
	}
	if pdf.NumPage() > limits.Pages {
		return pdfLimitError("pdf-max-pages", limits.Pages)
	}
	check := func(s string) error {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if content && r.opts.Mode == "filtered" {
			return r.auditText(s)
		}
		return nil
	}
	for _, key := range []string{"Title", "Subject", "Keywords", "Creator"} {
		if err := check(pdf.Trailer().Key("Info").Key(key).Text()); err != nil {
			return err
		}
	}
	for page := 1; page <= pdf.NumPage(); page++ {
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		p := pdf.Page(page)
		if content {
			text, err := p.GetPlainText(nil)
			if err != nil {
				return errConfig("generated PDF text extraction failed")
			}
			if err := check(text); err != nil {
				return err
			}
		}
		annotations := p.V.Key("Annots")
		for i := 0; i < annotations.Len(); i++ {
			if err := r.ctx.Err(); err != nil {
				return err
			}
			action := annotations.Index(i).Key("A")
			if !action.Key("Next").IsNull() {
				return errConfig("PDF contains chained actions")
			}
			if action.Key("S").Name() == "GoToR" {
				file := action.Key("F")
				page := action.Key("D")
				if len(action.Keys()) != 3 || len(file.Keys()) != 3 || page.Len() != 5 || page.Index(0).Kind() != pdfread.Integer || page.Index(0).Int64() != 0 || page.Index(1).Name() != "XYZ" || page.Index(2).Kind() != pdfread.Integer || page.Index(2).Int64() != 0 || page.Index(3).Int64() != 842 || !page.Index(4).IsNull() {
					return errConfig("PDF file action has unsupported options")
				}
				destination := file.Key("UF").Text()
				legacy, decodeErr := charmap.Macintosh.NewDecoder().String(file.Key("F").RawString())
				if file.Key("Type").Name() != "Filespec" || decodeErr != nil || legacy != destination || destination == "" {
					return errConfig("PDF filename encodings disagree")
				}
				if err := check(destination); err != nil {
					return err
				}
				if strings.ContainsAny(destination, "\\:\x00\r\n") || strings.HasPrefix(destination, "/") || filepath.Ext(destination) != ".pdf" {
					return errConfig("PDF file action has an invalid destination")
				}
				resolved := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(destination)))
				rel, err := filepath.Rel(r.stage, resolved)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return errConfig("PDF link leaves export directory")
				}
				if targetsExist {
					if st, err := os.Stat(resolved); err != nil || !st.Mode().IsRegular() {
						return errConfig("PDF link target is missing")
					}
				}
				continue
			}
			if action.Key("S").Name() != "URI" {
				return errConfig("PDF contains an unsupported action")
			}
			destination := action.Key("URI").Text()
			if err := check(destination); err != nil {
				return err
			}
			u, err := url.Parse(destination)
			if err != nil {
				return errConfig("PDF link is invalid")
			}
			if (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto") || !u.IsAbs() {
				return errConfig("PDF URI action is not an approved external link")
			}
		}
	}
	return nil
}

// gopdf's pinned writer puts its Info dictionary inline in the trailer, after
// the xref table. Replacing that dictionary does not move any indexed object.
// Encode UTF-16 explicitly for non-BMP metadata and the unsupported Keywords key.
func pdfInformation(data []byte, meta *DocumentMetadata) ([]byte, error) {
	if meta == nil {
		return data, nil
	}
	trailer := bytes.LastIndex(data, []byte("\ntrailer\n"))
	if trailer < 0 {
		return nil, errConfig("unexpected PDF trailer")
	}
	pos := bytes.Index(data[trailer:], []byte("/Info <<\n"))
	if pos < 0 {
		return nil, errConfig("missing PDF information dictionary")
	}
	pos += trailer
	end := bytes.Index(data[pos:], []byte("\n >>\n"))
	if end < 0 {
		return nil, errConfig("invalid PDF information dictionary")
	}
	end += pos + len("\n >>\n")
	var b strings.Builder
	b.WriteString("/Info <<\n")
	for _, field := range []struct{ key, value string }{{"Title", meta.Title}, {"Subject", meta.Description}, {"Keywords", strings.Join(meta.NativeTags, ", ")}, {"Creator", "Pieces Export"}} {
		fmt.Fprintf(&b, "/%s <FEFF", field.key)
		for _, unit := range utf16.Encode([]rune(field.value)) {
			fmt.Fprintf(&b, "%04X", unit)
		}
		b.WriteString(">\n")
	}
	b.WriteString(" >>\n")
	result := append([]byte(nil), data[:pos]...)
	result = append(result, []byte(b.String())...)
	return append(result, data[end:]...), nil
}
