package install_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const fixtureVersion = "0.0.0-fixture"

func fixtureBinary(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	source := `package main
import ("encoding/json"; "fmt"; "os"; "path/filepath"; "strconv"; "strings")
func main() {
 out, code := "", 0
 for i, arg := range os.Args[1:] {
  if arg == "--output" { out = os.Args[i+2] }
  if strings.HasPrefix(arg,"--test-exit=") { code, _ = strconv.Atoi(strings.TrimPrefix(arg,"--test-exit=")) }
 }
 if out == "" { os.Exit(99) }
 if err := os.MkdirAll(out,0700); err != nil { os.Exit(98) }
 b,_:=json.Marshal(os.Args[1:]); if os.WriteFile(filepath.Join(out,"fixture-args.json"),b,0600)!=nil { os.Exit(97) }
 fmt.Println("Synthetic export created; no Pieces OS requests.")
 os.Exit(code)
}`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fixture")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", path, filepath.Join(dir, "main.go"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v\n%s", err, b)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixtureRelease(t *testing.T, binary []byte, scenario string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, fixtureVersion)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	executable := "pieces-export"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	files := map[string][]byte{executable: binary, "LICENSE.txt": []byte("fixture"), "README.md": []byte("fixture"), "THIRD_PARTY_NOTICES.txt": []byte("fixture")}
	if scenario == "traversal" {
		files["../escaped.txt"] = []byte("must not extract")
	}
	for name, b := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("pieces-export_%s_%s_%s.zip", fixtureVersion, runtime.GOOS, runtime.GOARCH)
	b := buffer.Bytes()
	digest := sha256.Sum256(b)
	hash := hex.EncodeToString(digest[:])
	if scenario == "corrupt" {
		hash = strings.Repeat("0", 64)
	}
	checksums := hash + "  " + name + "\n"
	if scenario == "duplicate-checksum" {
		checksums += checksums
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS.txt"), []byte(checksums), 0600); err != nil {
		t.Fatal(err)
	}
	missing := ""
	switch scenario {
	case "missing-checksums":
		missing = "SHA256SUMS.txt"
	case "missing-archive":
		missing = name
	}
	if missing != "" {
		if err := os.Remove(filepath.Join(dir, missing)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func checkInstaller(t *testing.T, cmd *exec.Cmd, temp, output string, wantCode int, keep, ran bool) {
	t.Helper()
	b, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatalf("installer could not execute: %v", err)
		}
	}
	if code != wantCode {
		t.Fatalf("exit=%d want=%d\n%s", code, wantCode, b)
	}
	dirs, err := filepath.Glob(filepath.Join(temp, "pieces-export.*"))
	if err != nil || (len(dirs) == 1) != keep || len(dirs) > 1 {
		t.Fatalf("unexpected installation retention: count=%d keep=%t\n%s", len(dirs), keep, b)
	}
	if _, err := os.Stat(filepath.Join(output, "fixture-args.json")); (err == nil) != ran {
		t.Fatalf("export execution/retention mismatch: ran=%t\n%s", ran, b)
	}
	if ran {
		args, _ := os.ReadFile(filepath.Join(output, "fixture-args.json"))
		if !strings.Contains(string(args), `"--fixture-label=spaces $ literal"`) {
			t.Fatalf("CLI arguments not passed literally: %s", args)
		}
	}
}

func TestBashInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows uses the PowerShell installer")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	binary := fixtureBinary(t)
	script, _ := filepath.Abs("install.sh")
	for _, scenario := range []string{"complete", "partial", "failure", "cancel", "keep", "install-only", "corrupt", "duplicate-checksum", "traversal", "missing-checksums", "missing-archive"} {
		t.Run(scenario, func(t *testing.T) {
			root := fixtureRelease(t, binary, scenario)
			srv := httptest.NewTLSServer(http.FileServer(http.Dir(root)))
			defer srv.Close()
			cert := filepath.Join(t.TempDir(), "server.pem")
			if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			temp := t.TempDir()
			output := filepath.Join(t.TempDir(), "export with spaces")
			cleanup, code, keep, ran := "--remove", 0, false, true
			switch scenario {
			case "partial":
				code = 2
			case "failure", "corrupt", "duplicate-checksum", "traversal":
				code = 1
			case "cancel":
				code = 130
			case "keep":
				cleanup, keep = "--keep", true
			case "install-only":
				cleanup, keep, ran = "--install-only", true, false
			case "missing-checksums", "missing-archive":
				code, ran = 22, false // curl HTTP failure, with temporary files removed.
			}
			if scenario == "corrupt" || scenario == "duplicate-checksum" || scenario == "traversal" {
				ran = false
			}
			cmd := exec.Command("bash", script, "--base-url", srv.URL, "--version", fixtureVersion, "--output", output, cleanup, "--", "--fixture-label=spaces $ literal", fmt.Sprintf("--test-exit=%d", code))
			cmd.Env = append(os.Environ(), "TMPDIR="+temp, "CURL_CA_BUNDLE="+cert)
			checkInstaller(t, cmd, temp, output, code, keep, ran)
		})
	}
}

func TestPowerShellInstaller(t *testing.T) {
	shell := os.Getenv("PIECES_EXPORT_TEST_POWERSHELL")
	if shell == "" {
		shell = "pwsh"
	}
	if _, err := exec.LookPath(shell); err != nil {
		t.Skip("PowerShell unavailable")
	}
	binary := fixtureBinary(t)
	script, _ := filepath.Abs("install.ps1")
	for _, scenario := range []string{"complete", "partial", "failure", "cancel", "keep", "install-only", "corrupt", "duplicate-checksum", "traversal"} {
		t.Run(scenario, func(t *testing.T) {
			root := fixtureRelease(t, binary, scenario)
			temp := t.TempDir()
			output := filepath.Join(t.TempDir(), "export with spaces")
			cleanup, code, keep, ran := "Remove", 0, false, true
			switch scenario {
			case "partial":
				code = 2
			case "failure", "corrupt", "duplicate-checksum", "traversal":
				code = 1
			case "cancel":
				code = 130
			case "keep":
				cleanup, keep = "Keep", true
			case "install-only":
				keep, ran = true, false
			}
			if scenario == "corrupt" || scenario == "duplicate-checksum" || scenario == "traversal" {
				ran = false
			}
			// Mock only transport. Real package hashing, ZIP validation, byte
			// extraction, native invocation, exit status, and cleanup all execute.
			harness := `. $env:PIECES_TEST_INSTALLER
function Get-PiecesReleaseFile {
 param([string]$Url,[string]$Destination,[long]$MaxBytes)
 $uri=[Uri]$Url
 $relative=$uri.AbsolutePath.TrimStart('/').Replace('/',[IO.Path]::DirectorySeparatorChar)
 Copy-Item -LiteralPath (Join-Path $env:PIECES_TEST_RELEASE $relative) -Destination $Destination
}
$options=@{BaseUrl='https://fixture.invalid';Version='0.0.0-fixture';Output=$env:PIECES_TEST_OUTPUT;Cleanup=$env:PIECES_TEST_CLEANUP;ExportArgs=@('--fixture-label=spaces $ literal',('--test-exit='+$env:PIECES_TEST_EXIT))}
if ($env:PIECES_TEST_INSTALL_ONLY -eq 'true') { $options.InstallOnly=$true }
exit (Invoke-PiecesBootstrap @options)
`
			path := filepath.Join(t.TempDir(), "test.ps1")
			if err := os.WriteFile(path, []byte(harness), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path)
			cmd.Env = append(os.Environ(), "TMPDIR="+temp, "TMP="+temp, "TEMP="+temp, "PIECES_TEST_INSTALLER="+script, "PIECES_TEST_RELEASE="+root, "PIECES_TEST_OUTPUT="+output, "PIECES_TEST_CLEANUP="+cleanup, fmt.Sprintf("PIECES_TEST_EXIT=%d", code), fmt.Sprintf("PIECES_TEST_INSTALL_ONLY=%t", scenario == "install-only"))
			checkInstaller(t, cmd, temp, output, code, keep, ran)
		})
	}
}
