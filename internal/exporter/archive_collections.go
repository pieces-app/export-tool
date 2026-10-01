package exporter

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// Read every physical canonical file, accounting for every row and byte. The
// visitor receives exact checksum bytes; shared document paths never identify
// records. Legacy formats retain their one-file-per-record contract.
func archiveCollection(ctx context.Context, root *os.Root, material Material, mode string, format int, visit func(string, []byte, string, int64) error) error {
	prefix := "data/"
	if mode == "preserve" {
		prefix = "raw/"
	}
	folder := prefix + material.Folder
	_, association := associationFamilyByType(material.Type)
	grouped := format >= 6 && association
	seen := map[int]bool{}
	err := archiveDirectory(root, folder, func(entry fs.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return errConfig("unexpected canonical subdirectory")
		}
		file := folder + "/" + entry.Name()
		if !grouped {
			ref := strings.TrimSuffix(entry.Name(), ".json")
			if !strings.HasSuffix(entry.Name(), ".json") || !validDigest(ref) {
				return errConfig("unexpected canonical record filename")
			}
			b, err := archiveRead(root, file, 128<<20)
			if err != nil {
				return err
			}
			return visit(ref, b, file, 0)
		}
		index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "group-"), ".jsonl"))
		if err != nil || index < 0 || entry.Name() != fmt.Sprintf("group-%06d.jsonl", index) || seen[index] {
			return errConfig("unexpected or duplicate association chunk")
		}
		seen[index] = true
		f, err := archiveOpen(root, file)
		if err != nil {
			return err
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 64<<10), canonicalPayloadLimit+1)
		s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				return i + 1, data[:i+1], nil
			}
			if atEOF && len(data) > 0 {
				return 0, nil, errConfig("association chunk has an unterminated row")
			}
			return 0, nil, nil
		})
		var offset int64
		count := 0
		for s.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			b := s.Bytes()
			count++
			if len(b) > canonicalPayloadLimit || count > associationChunkRecords || count > 1 && offset+int64(len(b)) > associationChunkBytes {
				return errConfig("association chunk exceeds its record or byte bounds")
			}
			var v map[string]any
			if decodeArchiveJSON(b, &v) != nil || fieldString(v, "id") == "" {
				return errConfig("association chunk contains an invalid canonical row")
			}
			if err := visit(opaque(material.Type, fieldString(v, "id")), b, file, offset); err != nil {
				return err
			}
			offset += int64(len(b))
		}
		if s.Err() != nil || count == 0 {
			return errConfig("association chunk is empty, truncated, or unreadable")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := 0; i < len(seen); i++ {
		if !seen[i] {
			return errConfig("association chunk sequence is incomplete")
		}
	}
	return nil
}

func groupedDocumentPath(dataPath string) string {
	parts := strings.SplitN(dataPath, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	return "markdown/" + strings.TrimSuffix(parts[1], ".jsonl") + ".md"
}
