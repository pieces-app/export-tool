// release builds binary-only download archives. It never publishes them.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type module struct {
	Path, Version, Dir string
	Main               bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	version := flag.String("version", "0.12.3-dev", "embedded release version")
	out := flag.String("output", "dist", "release directory")
	targets := flag.String("targets", "darwin/arm64,darwin/amd64,linux/amd64,linux/arm64,windows/amd64,windows/arm64", "comma-separated OS/architecture pairs")
	flag.Parse()
	if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(*version) {
		return fmt.Errorf("invalid version")
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	notices, err := licenseNotices(strings.Split(*targets, ","))
	if err != nil {
		return err
	}
	downloadHelp, err := os.ReadFile("DOWNLOAD_README.md")
	if err != nil {
		return err
	}
	license, err := os.ReadFile("LICENSE.txt")
	if err != nil {
		return err
	}
	checksums := []string{}
	for _, target := range strings.Split(*targets, ",") {
		parts := strings.Split(target, "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid target")
		}
		osName, arch := parts[0], parts[1]
		if (osName != "darwin" && osName != "linux" && osName != "windows") || (arch != "amd64" && arch != "arm64") {
			return fmt.Errorf("unsupported target")
		}
		name := "pieces-export"
		if osName == "windows" {
			name += ".exe"
		}
		label := "pieces-export_" + *version + "_" + osName + "_" + arch
		dir := filepath.Join(*out, label)
		if err := os.Mkdir(dir, 0755); err != nil {
			return fmt.Errorf("release directory exists or cannot be created: %w", err)
		}
		binary := filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X main.version="+*version, "-o", binary, "./cmd/pieces-export")
		cmd.Env = envWith(map[string]string{"CGO_ENABLED": "0", "GOOS": osName, "GOARCH": arch})
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		fmt.Println("Building", target)
		if err := cmd.Run(); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "THIRD_PARTY_NOTICES.txt"), notices, 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), downloadHelp, 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "LICENSE.txt"), license, 0644); err != nil {
			return err
		}
		zipPath := filepath.Join(*out, label+".zip")
		if err := zipDirectory(zipPath, dir); err != nil {
			return err
		}
		b, err := os.ReadFile(zipPath)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(b)
		checksums = append(checksums, hex.EncodeToString(hash[:])+"  "+filepath.Base(zipPath))
	}
	sort.Strings(checksums)
	return os.WriteFile(filepath.Join(*out, "SHA256SUMS.txt"), []byte(strings.Join(checksums, "\n")+"\n"), 0644)
}
func envWith(values map[string]string) []string {
	result := []string{}
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, ok := values[key]; !ok {
			result = append(result, item)
		}
	}
	for k, v := range values {
		result = append(result, k+"="+v)
	}
	return result
}
func licenseNotices(targets []string) ([]byte, error) {
	// Test-only and unused transitive modules are not distributed. Collect the
	// union of modules supplying packages compiled into the requested binaries.
	byPath := map[string]module{}
	for _, target := range targets {
		parts := strings.Split(target, "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid target")
		}
		cmd := exec.Command("go", "list", "-deps", "-json", "./cmd/pieces-export")
		cmd.Env = envWith(map[string]string{"CGO_ENABLED": "0", "GOOS": parts[0], "GOARCH": parts[1]})
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("cannot inspect dependency licenses for %s: %w", target, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		for {
			var pkg struct{ Module *module }
			err := decoder.Decode(&pkg)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if pkg.Module != nil && !pkg.Module.Main {
				byPath[pkg.Module.Path] = *pkg.Module
			}
		}
	}
	modules := make([]module, 0, len(byPath))
	for _, m := range byPath {
		modules = append(modules, m)
	}
	var result bytes.Buffer
	result.WriteString("Third-party notices for Pieces Export\n\nProject source is private. The following notices apply to third-party dependencies, not to the export-tool source.\nIncludes the union of dependency modules compiled into the requested platform binaries.\n\n")
	root, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return nil, err
	}
	goLicense, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(root)), "LICENSE"))
	if err != nil {
		return nil, err
	}
	result.WriteString("=== Go runtime and standard library ===\n")
	result.Write(goLicense)
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	for _, m := range modules {
		if m.Dir == "" {
			download, err := exec.Command("go", "mod", "download", "-json", m.Path+"@"+m.Version).Output()
			if err != nil {
				return nil, err
			}
			if err = json.Unmarshal(download, &m); err != nil {
				return nil, err
			}
		}
		if m.Path == "golang.org/x/image" {
			fontLicense, err := os.ReadFile(filepath.Join(m.Dir, "font/gofont/ttfs/README"))
			if err != nil {
				return nil, err
			}
			result.WriteString("\n\n=== Bundled Go fonts / Bigelow & Holmes ===\n")
			result.Write(fontLicense)
		}
		files, err := os.ReadDir(m.Dir)
		if err != nil {
			return nil, err
		}
		count := 0
		for _, entry := range files {
			name := strings.ToUpper(entry.Name())
			if entry.IsDir() || !(strings.HasPrefix(name, "LICENSE") || strings.HasPrefix(name, "COPYING") || strings.HasPrefix(name, "NOTICE")) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(m.Dir, entry.Name()))
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&result, "\n\n=== %s %s / %s ===\n", m.Path, m.Version, entry.Name())
			result.Write(b)
			count++
		}
		if count == 0 {
			return nil, fmt.Errorf("no root license/notice found for %s; review before distribution", m.Path)
		}
	}
	return result.Bytes(), nil
}
func zipDirectory(destination, dir string) error {
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	z := zip.NewWriter(f)
	entries, err := os.ReadDir(dir)
	if err != nil {
		f.Close()
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("unexpected directory in release")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = entry.Name()
		header.Method = zip.Deflate
		writer, err := z.CreateHeader(header)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		if _, err = writer.Write(b); err != nil {
			return err
		}
	}
	if err = z.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
