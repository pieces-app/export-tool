package exporter

import (
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (r *run) validateMarkdownLinks() error {
	return filepath.WalkDir(r.stage, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		root := goldmark.DefaultParser().Parse(text.NewReader(data))
		return ast.Walk(root, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
			if !enter {
				return ast.WalkContinue, nil
			}
			link, ok := n.(*ast.Link)
			if !ok {
				return ast.WalkContinue, nil
			}
			u, err := url.Parse(string(link.Destination))
			if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" {
				return ast.WalkContinue, nil
			}
			target := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(u.Path)))
			relative, err := filepath.Rel(r.stage, target)
			if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(u.Path) {
				return ast.WalkStop, errConfig("document link leaves the export directory")
			}
			// Manifest is written after rendering; its destination is reserved.
			if relative == "manifest.json" {
				return ast.WalkContinue, nil
			}
			if _, err := os.Stat(target); err != nil {
				return ast.WalkStop, errConfig("document contains a missing local target")
			}
			return ast.WalkContinue, nil
		})
	})
}
