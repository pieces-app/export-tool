package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// This test serves the unmodified release ZIP and checksums, then executes the
// real bootstrap and CLI against an isolated synthetic OS. No installed OS,
// system trust store, shell profile, or public download service is touched.
func TestPackagedBashInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows uses PowerShell")
	}
	testPackagedInstaller(t, false)
}

// PowerShell uses the same actual release ZIP and CLI, with file transport
// substituted for HTTPS. This does not certify its TLS implementation or GCP.
func TestPackagedPowerShellInstaller(t *testing.T) {
	testPackagedInstaller(t, true)
}

func testPackagedInstaller(t *testing.T, powershell bool) {
	release, version := os.Getenv("PIECES_EXPORT_TEST_RELEASE"), os.Getenv("PIECES_EXPORT_TEST_VERSION")
	if release == "" || version == "" {
		t.Skip("set PIECES_EXPORT_TEST_RELEASE to the release directory and PIECES_EXPORT_TEST_VERSION to its pinned version")
	}
	release, err := filepath.Abs(release)
	if err != nil {
		t.Fatal(err)
	}
	archive := fmt.Sprintf("pieces-export_%s_%s_%s.zip", version, runtime.GOOS, runtime.GOARCH)
	for _, name := range []string{archive, "SHA256SUMS.txt"} {
		if _, err := os.Stat(filepath.Join(release, name)); err != nil {
			t.Fatal(err)
		}
	}
	scriptName := "install.sh"
	if powershell {
		scriptName = "install.ps1"
	}
	script, err := filepath.Abs(filepath.Join("../../install", scriptName))
	if err != nil {
		t.Fatal(err)
	}
	interactive := os.Getenv("PIECES_EXPORT_TEST_INSTALLER_PROMPT")
	// default-keep omits --format and recovery flags, so the actual CLI must
	// accept the installer's own Markdown default and private recovery folders.
	scenarios := []string{"complete-remove", "complete-keep", "partial-remove", "recovery-remove", "default-keep"}
	if interactive != "" {
		if interactive != "remove" && interactive != "keep" {
			t.Fatal("PIECES_EXPORT_TEST_INSTALLER_PROMPT must be remove or keep")
		}
		// Run the compiled test executable in a terminal. The operator answers
		// the genuine final prompt; this option is not an automatic response.
		scenarios = []string{"prompt-" + interactive}
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			requests := map[string]int{}
			downloads := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests[r.URL.Path]++
				mu.Unlock()
				for _, name := range []string{archive, "SHA256SUMS.txt"} {
					if r.Method == http.MethodGet && r.URL.Path == "/"+version+"/"+name {
						http.ServeFile(w, r, filepath.Join(release, name))
						return
					}
				}
				http.NotFound(w, r)
			}))
			defer downloads.Close()
			cert := filepath.Join(t.TempDir(), "fixture-ca.pem")
			if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: downloads.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			f := summaryScopeFixture()
			unprofiled := record("unprofiled-person", "")
			unprofiled["annotations"], unprofiled["summaries"] = refs(), refs()
			f.data["PERSONS"] = append(f.data["PERSONS"], unprofiled)
			secret := fakeSecret()
			f.data["ANNOTATIONS"][0]["text"] = "Actual summary narrative with [a person](pieces://persons/person). Synthetic credential: " + secret
			partial := scenario == "partial-remove"
			if partial {
				// A selected summary names an annotation that the source cannot
				// supply. The actual CLI must finalize a partial archive and exit 2.
				f.data["ANNOTATIONS"] = f.data["ANNOTATIONS"][1:]
			}
			osServer := f.server(t)
			defer osServer.Close()
			temp := t.TempDir()
			data := filepath.Join(t.TempDir(), "installer data")
			out := filepath.Join(t.TempDir(), "export with spaces")
			args := []string{script, "--base-url", downloads.URL, "--version", version, "--output", out}
			keep := strings.HasSuffix(scenario, "keep")
			cleanup := "--remove"
			if keep {
				cleanup = "--keep"
			}
			if interactive != "" {
				cleanup = "--ask"
			}
			args = append(args, cleanup)
			format := "both"
			if scenario == "recovery-remove" || scenario == "default-keep" {
				format = "markdown"
			}
			args = append(args, "--", "--base-url", osServer.URL, "--launch-os=false", "--close-desktop=false", "--yes", "--metadata", "off")
			if scenario != "default-keep" {
				args = append(args, "--format", format)
			}
			var work, keys string
			if scenario == "recovery-remove" {
				cfg := recoveryOptionsFixture(t)
				work, keys = cfg.Directory, cfg.KeyDirectory
				args = append(args, "--work", work, "--recovery-keys", keys)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", args...)
			cmd.Env = append(os.Environ(), "TMPDIR="+temp, "CURL_CA_BUNDLE="+cert, "PIECES_EXPORT_HOME="+data)
			if powershell {
				cleanup := "Remove"
				if keep {
					cleanup = "Keep"
				}
				if interactive != "" {
					cleanup = "Ask"
				}
				cmd = packagedPowerShellInstallerCommand(t, ctx, script, release, version, out, temp, osServer.URL, cleanup)
				cmd.Env = append(cmd.Env, "PIECES_EXPORT_HOME="+data, "PIECES_TEST_RECOVERY_WORK="+work, "PIECES_TEST_RECOVERY_KEYS="+keys)
				if scenario == "default-keep" {
					cmd.Env = append(cmd.Env, "PIECES_TEST_DEFAULT_FORMAT=1")
				}
			}
			var log bytes.Buffer
			cmd.Stdout, cmd.Stderr = &log, &log
			if interactive != "" {
				cmd.Stdin = os.Stdin
				// Use the same writer for both streams so os/exec serializes
				// copying into the buffer even when run with the race detector.
				writer := io.MultiWriter(os.Stdout, &log)
				cmd.Stdout, cmd.Stderr = writer, writer
			}
			err := cmd.Run()
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			wantCode, wantStatus := 0, "complete_for_implemented_scope"
			if partial {
				wantCode, wantStatus = 2, "partial"
			}
			if code != wantCode {
				t.Fatalf("bootstrap exit %d, want %d: %s", code, wantCode, log.String())
			}
			if interactive != "" && !strings.Contains(log.String(), "Remove the export tool? Your exports will stay. [y/N]") {
				t.Fatal("interactive cleanup prompt was not displayed; run the compiled test executable in a terminal")
			}
			executable := "pieces-export"
			if runtime.GOOS == "windows" {
				executable += ".exe"
			}
			binary := filepath.Join(data, "tool", version, executable)
			_, statErr := os.Stat(binary)
			if (statErr == nil) != keep {
				t.Fatalf("tool retention=%t wantKeep=%t: %s", statErr == nil, keep, log.String())
			}
			if statErr == nil {
				b, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
				if err != nil || !strings.Contains(string(b), version) {
					t.Fatal("retained CLI is not the selected runnable release")
				}
			}
			staging, _ := filepath.Glob(filepath.Join(data, "tool", ".download*"))
			if len(staging) != 0 {
				t.Fatalf("download staging left behind: %v", staging)
			}
			sessions, _ := filepath.Glob(filepath.Join(data, "recovery", "*"))
			if len(sessions) != 0 {
				t.Fatalf("finalized export left installer recovery data: %v", sessions)
			}
			var manifest Manifest
			b, err := os.ReadFile(filepath.Join(out, "manifest.json"))
			if err != nil || json.Unmarshal(b, &manifest) != nil || manifest.Status != wantStatus || manifest.Scope.Name != "summaries" {
				t.Fatal("cleanup lost or changed the finalized archive or its coverage status")
			}
			if manifest.People.Mode != "profiles" || manifest.People.Selected != 1 || manifest.People.Omitted != 1 {
				t.Fatal("installed default export lost profile selection")
			}
			if scenario == "recovery-remove" {
				session, err := OpenRecovery(ctx, RecoveryOptions{Directory: work, KeyDirectory: keys})
				if err != nil {
					t.Fatal("installer cleanup lost recovery state or keys", err)
				}
				info := session.Info()
				if err := session.Close(); err != nil {
					t.Fatal(err)
				}
				if !info.CanResume || info.Records == 0 {
					t.Fatal("installer left an unusable capture")
				}
			}
			paths := map[string]string{}
			b, err = os.ReadFile(filepath.Join(out, "link-map.json"))
			if err != nil || json.Unmarshal(b, &paths) != nil {
				t.Fatal("export link map missing")
			}
			if !partial {
				b, err = os.ReadFile(filepath.Join(out, paths[opaque("WORKSTREAM_SUMMARIES", "summary")]))
				if err != nil || !strings.Contains(string(b), "Actual summary narrative") || strings.Contains(string(b), secret) {
					t.Fatal("summary narrative or credential filtering failed")
				}
				for _, coverage := range manifest.Coverage {
					if coverage.InventoryMode != "references" && (coverage.InitialCount != coverage.FinalCount || coverage.Fetched != coverage.InitialCount) {
						t.Fatal("selected synthetic inventory did not reconcile")
					}
				}
			}
			if format == "both" {
				if _, err := os.Stat(filepath.Join(out, "index.pdf")); err != nil {
					t.Fatal("PDF output missing after installer cleanup")
				}
			} else if _, err := os.Stat(filepath.Join(out, "index.md")); err != nil {
				t.Fatal("Markdown output missing after installer cleanup")
			}
			moved := out + "-moved"
			if err := os.Rename(out, moved); err != nil {
				t.Fatal(err)
			}
			r := &run{ctx: ctx, stage: moved, opts: Options{Mode: "filtered", Scanner: scanner(t, DefaultPolicy())}}
			if err := r.validateMarkdownLinks(); err != nil {
				t.Fatal(err)
			}
			if err := r.auditOutput(); err != nil {
				t.Fatal(err)
			}
			assertSummaryScopeRequests(t, f)
			mu.Lock()
			defer mu.Unlock()
			if !powershell && (len(requests) != 2 || requests["/"+version+"/SHA256SUMS.txt"] != 1 || requests["/"+version+"/"+archive] != 1) {
				t.Fatalf("unexpected download requests: %v", requests)
			}
			transport := "HTTPS download"
			if powershell {
				transport = "file transport (HTTPS mocked)"
			}
			t.Logf("actual release %s, scoped export, exit status, cleanup/retention, privacy, PDF, and relocated links passed", transport)
		})
	}
}

func packagedPowerShellInstallerCommand(t *testing.T, ctx context.Context, script, release, version, out, temp, osURL, cleanup string) *exec.Cmd {
	t.Helper()
	shell := os.Getenv("PIECES_EXPORT_TEST_POWERSHELL")
	if shell == "" {
		shell = "pwsh"
	}
	if _, err := exec.LookPath(shell); err != nil {
		t.Skip("PowerShell unavailable")
	}
	harness := `. $env:PIECES_TEST_INSTALLER
function Get-PiecesReleaseFile {
 param([string]$Url,[string]$Destination,[long]$MaxBytes)
 $uri=[Uri]$Url
 $name=[IO.Path]::GetFileName($uri.AbsolutePath)
 $source=Join-Path $env:PIECES_TEST_RELEASE $name
 if ((Get-Item -LiteralPath $source).Length -gt $MaxBytes) { throw 'Fixture exceeds download limit.' }
 Copy-Item -LiteralPath $source -Destination $Destination
}
$exportFormat='both'
if ($env:PIECES_TEST_RECOVERY_WORK) { $exportFormat='markdown' }
$exportArgs=@('--base-url',$env:PIECES_TEST_OS,'--launch-os=false','--close-desktop=false','--yes','--metadata','off')
if (!$env:PIECES_TEST_DEFAULT_FORMAT) { $exportArgs += @('--format',$exportFormat) }
$options=@{BaseUrl='https://fixture.invalid';Version=$env:PIECES_TEST_VERSION;Output=$env:PIECES_TEST_OUTPUT;Cleanup=$env:PIECES_TEST_CLEANUP;ExportArgs=$exportArgs}
if ($env:PIECES_TEST_RECOVERY_WORK) { $options.ExportArgs += @('--work',$env:PIECES_TEST_RECOVERY_WORK,'--recovery-keys',$env:PIECES_TEST_RECOVERY_KEYS) }
exit (Invoke-PiecesBootstrap @options)
`
	path := filepath.Join(t.TempDir(), "bootstrap-test.ps1")
	if err := os.WriteFile(path, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-NoLogo", "-NoProfile"}
	if cleanup != "Ask" {
		args = append(args, "-NonInteractive")
	}
	args = append(args, "-File", path)
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Env = append(os.Environ(), "TMPDIR="+temp, "TMP="+temp, "TEMP="+temp,
		"PIECES_TEST_INSTALLER="+script, "PIECES_TEST_RELEASE="+release,
		"PIECES_TEST_VERSION="+version, "PIECES_TEST_OUTPUT="+out,
		"PIECES_TEST_CLEANUP="+cleanup, "PIECES_TEST_OS="+osURL)
	return cmd
}
