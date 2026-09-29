package exporter

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

const pdfLinkSlotPrefix = "pieces-export-pdf-link-slot:"

// PDFKit on macOS 15 interprets F as a MacRoman filename and ignores UF.
// Readers supporting Unicode file specifications can use UF. Do not generate
// an apparently clickable local link when its legacy filename cannot be encoded.
func pdfFileAction(path string) (string, bool) {
	legacy, err := charmap.Macintosh.NewEncoder().String(path)
	if err != nil {
		return "", false
	}
	var unicode strings.Builder
	unicode.WriteString("FEFF")
	for _, unit := range utf16.Encode([]rune(path)) {
		fmt.Fprintf(&unicode, "%04X", unit)
	}
	return fmt.Sprintf("/A << /S /GoToR /F << /Type /Filespec /F <%X> /UF <%s> >> /D [0 /XYZ 0 842 null] >> >>", []byte(legacy), unicode.String()), true
}

// The pinned gopdf writer only exposes URI annotations. Reserve an ASCII URI
// slot larger than our file action, then replace it with equal-length bytes.
// This keeps every object offset and the existing xref table unchanged. Slots
// are generated internally, never supplied by source Markdown. No Launch,
// JavaScript, absolute file URL, or executable action is generated.
type pdfActionSlots map[string]string

func (slots pdfActionSlots) reserve(action string) string {
	marker := fmt.Sprintf("%s%d:%s", pdfLinkSlotPrefix, len(slots), strings.Repeat("x", len(action)))
	original := "/A <</S /URI /URI (" + marker + ")>>>>"
	slots[original] = action + strings.Repeat(" ", len(original)-len(action))
	return marker
}

func (slots pdfActionSlots) apply(data []byte) ([]byte, error) {
	if len(slots) == 0 {
		return data, nil
	}
	result := bytes.Clone(data)
	prefix := []byte("/A <</S /URI /URI (" + pdfLinkSlotPrefix)
	seen := make(map[string]bool, len(slots))
	for offset := 0; ; {
		start := bytes.Index(result[offset:], prefix)
		if start < 0 {
			break
		}
		start += offset
		end := bytes.Index(result[start:], []byte(")>>>>"))
		if end < 0 {
			return nil, errConfig("invalid PDF file-action slot")
		}
		end += start + len(")>>>>")
		original := string(result[start:end])
		replacement, ok := slots[original]
		if !ok || seen[original] || len(replacement) != len(original) {
			return nil, errConfig("unexpected PDF file-action slot")
		}
		copy(result[start:end], replacement)
		seen[original] = true
		offset = end
	}
	if len(seen) != len(slots) {
		return nil, errConfig("missing PDF file-action slot")
	}
	return result, nil
}
