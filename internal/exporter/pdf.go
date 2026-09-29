package exporter

import (
	"bytes"
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
func inlineText(n ast.Node, data []byte) string {
	var b strings.Builder
	var visit func(ast.Node, bool)
	visit = func(node ast.Node, code bool) {
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

func ValidatePDFFont(path string) error {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return errConfig("PDF font unreadable or larger than 32 MiB")
	}
	font, err := os.ReadFile(path)
	if err != nil {
		return errConfig("PDF font could not be read")
	}
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData("body", font); err != nil {
		return errConfig("PDF font is not a supported TrueType font")
	}
	return nil
}

// Only Markdown text and link destinations enter the PDF. No HTML execution,
// remote images, filesystem image reads, or imported source PDFs are enabled.
func markdownBlocks(data []byte) []pdfBlock {
	root := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(data))
	blocks := []pdfBlock{}
	_ = ast.Walk(root, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			return ast.WalkContinue, nil
		}
		block := pdfBlock{Size: 11}
		switch node := n.(type) {
		case *ast.Heading:
			block.Size = max(12, float64(24-node.Level*2))
			block.Heading = true
			block.Text = inlineText(node, data)
		case *ast.Paragraph:
			block.Text = inlineText(node, data)
		case *ast.TextBlock:
			block.Text = inlineText(node, data)
		case *ast.FencedCodeBlock:
			block.Code = true
			block.Size = 9
			for i := 0; i < node.Lines().Len(); i++ {
				line := node.Lines().At(i)
				block.Text += string(line.Value(data))
			}
		case *ast.CodeBlock:
			block.Code = true
			block.Size = 9
			for i := 0; i < node.Lines().Len(); i++ {
				line := node.Lines().At(i)
				block.Text += string(line.Value(data))
			}
		case *ast.HTMLBlock:
			block.Code = true
			block.Size = 9
			for i := 0; i < node.Lines().Len(); i++ {
				line := node.Lines().At(i)
				block.Text += string(line.Value(data))
			}
		default:
			if n.Kind().String() == "TableRow" || n.Kind().String() == "TableHeader" {
				cells := []string{}
				for cell := n.FirstChild(); cell != nil; cell = cell.NextSibling() {
					cells = append(cells, inlineText(cell, data))
				}
				block.Text = strings.Join(cells, " | ")
			} else {
				return ast.WalkContinue, nil
			}
		}
		_ = ast.Walk(n, func(child ast.Node, in bool) (ast.WalkStatus, error) {
			if in {
				if link, ok := child.(*ast.Link); ok {
					block.Links = append(block.Links, pdfLink{inlineText(link, data), string(link.Destination)})
				}
			}
			return ast.WalkContinue, nil
		})
		if n.Parent() != nil && n.Parent().Kind() == ast.KindListItem {
			block.Text = "• " + block.Text
		}
		if strings.TrimSpace(block.Text) != "" {
			blocks = append(blocks, block)
		}
		return ast.WalkSkipChildren, nil
	})
	return blocks
}

func (r *run) renderPDFs() error {
	if r.opts.Progress != nil {
		fmt.Fprintln(r.opts.Progress, "Converting approved Markdown to PDFs...")
	}
	paths := []string{}
	err := filepath.WalkDir(r.stage, func(path string, d fs.DirEntry, err error) error {
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
		r.progress.Add(1)
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		data, err := os.ReadFile(filepath.Join(r.stage, path))
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
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	pdf.SetMargins(44, 44, 44, 44)
	slots := pdfActionSlots{}
	unsupportedLinks := map[string]bool{}
	missing := map[rune]bool{}
	option := gopdf.TtfOption{OnGlyphNotFound: func(c rune) { missing[c] = true }, OnGlyphNotFoundSubstitute: func(rune) rune { return '?' }}
	font := goregular.TTF
	if r.opts.PDFFont != "" {
		st, err := os.Stat(r.opts.PDFFont)
		if err != nil || st.Size() > 32<<20 {
			return 0, errConfig("PDF font unreadable or larger than 32 MiB")
		}
		font, err = os.ReadFile(r.opts.PDFFont)
		if err != nil {
			return 0, errConfig("PDF font could not be read")
		}
	}
	if err := pdf.AddTTFFontDataWithOption("body", font, option); err != nil {
		return 0, err
	}
	info := gopdf.PdfInfo{Title: "Pieces export", Creator: "Pieces Export"}
	if meta != nil {
		info.Title = meta.Title
		info.Subject = meta.Description

	}
	pdf.SetInfo(info)
	page := 0
	y := float64(44)
	newPage := func() error {
		pdf.AddPage()
		page++
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
		lines := []string{}
		for _, paragraph := range strings.Split(strings.ReplaceAll(value, "\t", "    "), "\n") {
			if paragraph == "" {
				lines = append(lines, "")
				continue
			}
			wrapped, err := pdf.SplitTextWithWordWrap(paragraph, 507)
			if err != nil {
				return err
			}
			lines = append(lines, wrapped...)
		}
		height := float64(len(lines))*size*1.5 + 6
		if heading {
			height += 60
		}
		// Keep headings with following text and ordinary short blocks together.
		if height < 741 && y+height > 785 && y > 44 {
			if err := newPage(); err != nil {
				return err
			}
			if err := pdf.SetFont("body", "", size); err != nil {
				return err
			}
		}
		for _, line := range lines {
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
				annotation := destination
				if strings.HasPrefix(destination, "/A <<") {
					annotation = slots.reserve(destination)
				}
				pdf.AddExternalLink(annotation, 44, y, width, size*1.5)
			}
			y += size * 1.5
		}
		y += 6
		return nil
	}
	for _, block := range markdownBlocks(data) {
		if r.ctx.Err() != nil {
			return 0, r.ctx.Err()
		}
		links := []pdfLink{}
		for _, link := range block.Links {
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
		if len(links) == 1 && len(block.Links) == 1 {
			// A single-link paragraph/list item is one clickable block, avoiding
			// duplicated labels and detached link lines at page boundaries.
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
	var output bytes.Buffer
	if err := pdf.Write(&output); err != nil {
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
	if err := writeFile(filepath.Join(r.stage, target), pdfBytes); err != nil {
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
	pdf, err := pdfread.NewReader(f, st.Size())
	if err != nil {
		return errConfig("generated PDF could not be parsed")
	}
	check := func(s string) error {
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
