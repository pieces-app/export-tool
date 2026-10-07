package install_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const fixtureVersion = "0.0.0-fixture"

// The fixture stands in for pieces-export. It records each invocation and
// imitates the real CLI contract the installers depend on: exit 0 or 2
// finalizes <output>/manifest.json, other exits leave only <output>.partial,
// a fresh --work workspace must not exist yet and needs existing parents,
// recovery is Markdown-only, and --dry-run rejects recovery folders.
const fixtureSource = `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("pieces-export 0.0.0-fixture")
		return
	}
	value := func(name string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
			if strings.HasPrefix(a, name+"=") {
				return strings.TrimPrefix(a, name+"=")
			}
		}
		return ""
	}
	has := func(name string) bool {
		for _, a := range args {
			if a == name {
				return true
			}
		}
		return false
	}
	record := map[string]any{"args": args}
	if has("--test-prompt") {
		fmt.Print("Export now? [Y/n] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		record["answer"] = strings.TrimSpace(answer)
	}
	if log := os.Getenv("PIECES_FIXTURE_LOG"); log != "" {
		b, _ := json.Marshal(record)
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(97)
		}
		f.Write(append(b, '\n'))
		f.Close()
	}
	code := 0
	if v := value("--test-exit"); v != "" {
		code, _ = strconv.Atoi(v)
	}
	command := ""
	if len(args) > 0 {
		command = args[0]
	}
	work, keys, out := value("--work"), value("--recovery-keys"), value("--output")
	if command == "export" && has("--dry-run") {
		if work != "" || keys != "" {
			fmt.Fprintln(os.Stderr, "--work and --recovery-keys apply only to an actual export")
			os.Exit(1)
		}
		fmt.Println("Dry run: inventory only.")
		os.Exit(code)
	}
	if command != "export" && command != "resume" {
		os.Exit(64)
	}
	if out == "" {
		os.Exit(99)
	}
	if command == "resume" {
		if st, err := os.Stat(work); err != nil || !st.IsDir() {
			fmt.Fprintln(os.Stderr, "existing encrypted recovery workspace required")
			os.Exit(1)
		}
	} else if work != "" || keys != "" {
		if value("--format") != "markdown" {
			fmt.Fprintln(os.Stderr, "source recovery currently supports Markdown exports without SDK-cache input")
			os.Exit(1)
		}
		for _, p := range []string{work, keys} {
			if st, err := os.Stat(filepath.Dir(p)); err != nil || !st.IsDir() {
				fmt.Fprintln(os.Stderr, "recovery workspace and key-directory parents must already exist")
				os.Exit(1)
			}
		}
		if _, err := os.Lstat(work); err == nil {
			fmt.Fprintln(os.Stderr, "export recovery workspace must be new")
			os.Exit(1)
		}
		if !has("--test-no-capture") {
			if os.Mkdir(work, 0700) != nil || os.MkdirAll(keys, 0700) != nil {
				os.Exit(94)
			}
		}
	}
	if code == 0 || code == 2 {
		if os.MkdirAll(out, 0700) != nil {
			os.Exit(98)
		}
		os.WriteFile(filepath.Join(out, "manifest.json"), []byte("{}\n"), 0600)
		os.WriteFile(filepath.Join(out, "index.md"), []byte("# Fixture export\n"), 0600)
		fmt.Println("Export written:", out)
	} else {
		os.MkdirAll(out+".partial", 0700)
	}
	os.Exit(code)
}
`

var fixture struct {
	once sync.Once
	b    []byte
	err  error
}

func fixtureBinary(t *testing.T) []byte {
	t.Helper()
	fixture.once.Do(func() {
		dir, err := os.MkdirTemp("", "pieces-export-fixture")
		if err != nil {
			fixture.err = err
			return
		}
		defer os.RemoveAll(dir)
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(fixtureSource), 0600); err != nil {
			fixture.err = err
			return
		}
		path := filepath.Join(dir, "fixture")
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if b, err := exec.Command("go", "build", "-o", path, filepath.Join(dir, "main.go")).CombinedOutput(); err != nil {
			fixture.err = fmt.Errorf("fixture build: %v\n%s", err, b)
			return
		}
		fixture.b, fixture.err = os.ReadFile(path)
	})
	if fixture.err != nil {
		t.Fatal(fixture.err)
	}
	return fixture.b
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "pieces-export.exe"
	}
	return "pieces-export"
}

// fixtureRelease lays out <root>/<version>/{zip,SHA256SUMS.txt}, the
// structure both installers expect behind --base-url.
func fixtureRelease(t *testing.T, binary []byte, scenario string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, fixtureVersion)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	files := map[string][]byte{executableName(): binary, "LICENSE.txt": []byte("fixture"), "README.md": []byte("fixture"), "THIRD_PARTY_NOTICES.txt": []byte("fixture")}
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
	switch scenario {
	case "missing-checksums":
		if err := os.Remove(filepath.Join(dir, "SHA256SUMS.txt")); err != nil {
			t.Fatal(err)
		}
	case "missing-archive":
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type installer struct {
	name   string // "bash" or "powershell"
	shell  string
	script string
}

func bashInstaller(t *testing.T) installer {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native Windows uses the PowerShell installer")
	}
	shell := os.Getenv("PIECES_EXPORT_TEST_BASH")
	if shell == "" {
		shell = "bash"
	}
	if _, err := exec.LookPath(shell); err != nil {
		t.Skip("bash unavailable")
	}
	script, _ := filepath.Abs("install.sh")
	return installer{name: "bash", shell: shell, script: script}
}

func powerShellInstaller(t *testing.T) installer {
	t.Helper()
	shell := os.Getenv("PIECES_EXPORT_TEST_POWERSHELL")
	if shell == "" {
		shell = "pwsh"
	}
	if _, err := exec.LookPath(shell); err != nil {
		t.Skip("PowerShell unavailable")
	}
	script, _ := filepath.Abs("install.ps1")
	return installer{name: "powershell", shell: shell, script: script}
}

// Only download transport is replaced: hashing, ZIP checks, extraction,
// installation, native invocation, recovery handling and exit codes are real.
const powerShellHarness = `. $env:PIECES_TEST_INSTALLER
function Get-PiecesReleaseFile {
 param([string]$Url,[string]$Destination,[long]$MaxBytes)
 $uri=[Uri]$Url
 $relative=$uri.AbsolutePath.TrimStart('/').Replace('/',[IO.Path]::DirectorySeparatorChar)
 $source=Join-Path $env:PIECES_TEST_RELEASE $relative
 if (!(Test-Path -LiteralPath $source -PathType Leaf)) { throw "fixture HTTP 404: $relative" }
 Copy-Item -LiteralPath $source -Destination $Destination
}
$json=$env:PIECES_TEST_OPTIONS | ConvertFrom-Json
$options=@{}
foreach ($p in $json.PSObject.Properties) { $options[$p.Name]=$p.Value }
exit (Invoke-PiecesBootstrap @options)
`

type options struct {
	baseURL     bool
	version     string
	output      string
	resume      bool
	dryRun      bool
	installOnly bool
	noOpen      bool
	cleanup     string // "", "keep", "remove", "ask"
	defaultData bool   // leave PIECES_EXPORT_HOME unset
	exportArgs  []string
}

type harness struct {
	t       *testing.T
	inst    installer
	release string
	server  *httptest.Server
	cert    string
	home    string
	data    string
	temp    string
	log     string
	psFile  string
}

func newHarness(t *testing.T, inst installer, binary []byte, scenario string) *harness {
	t.Helper()
	h := &harness{t: t, inst: inst, release: fixtureRelease(t, binary, scenario)}
	h.home = t.TempDir()
	h.data = filepath.Join(t.TempDir(), "data dir with spaces")
	h.temp = t.TempDir()
	h.log = filepath.Join(t.TempDir(), "fixture.log")
	if inst.name == "bash" {
		h.server = httptest.NewTLSServer(http.FileServer(http.Dir(h.release)))
		t.Cleanup(h.server.Close)
		h.cert = filepath.Join(t.TempDir(), "server.pem")
		if err := os.WriteFile(h.cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: h.server.Certificate().Raw}), 0600); err != nil {
			t.Fatal(err)
		}
	} else {
		h.psFile = filepath.Join(t.TempDir(), "harness.ps1")
		if err := os.WriteFile(h.psFile, []byte(powerShellHarness), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h *harness) env(o options) []string {
	env := append(os.Environ(), "HOME="+h.home, "TMPDIR="+h.temp, "TMP="+h.temp, "TEMP="+h.temp,
		"PIECES_FIXTURE_LOG="+h.log, "XDG_DATA_HOME=", "XDG_CONFIG_HOME=", "PIECES_EXPORT_HOME="+h.data)
	if o.defaultData {
		env = append(env, "PIECES_EXPORT_HOME=")
	}
	return env
}

func (h *harness) command(o options) *exec.Cmd {
	if h.inst.name == "bash" {
		args := []string{h.inst.script}
		if o.baseURL {
			args = append(args, "--base-url", h.server.URL, "--version", fixtureVersion)
		}
		if o.version != "" {
			args = append(args, "--version", o.version)
		}
		if o.output != "" {
			args = append(args, "--output", o.output)
		}
		for flag, on := range map[string]bool{"--resume": o.resume, "--dry-run": o.dryRun, "--install-only": o.installOnly, "--no-open": o.noOpen} {
			if on {
				args = append(args, flag)
			}
		}
		if o.cleanup != "" {
			args = append(args, "--"+o.cleanup)
		}
		if len(o.exportArgs) > 0 {
			args = append(append(args, "--"), o.exportArgs...)
		}
		cmd := exec.Command(h.inst.shell, args...)
		cmd.Env = append(h.env(o), "CURL_CA_BUNDLE="+h.cert)
		detach(cmd)
		return cmd
	}
	m := map[string]any{}
	if o.baseURL {
		m["BaseUrl"], m["Version"] = "https://fixture.invalid", fixtureVersion
	}
	if o.version != "" {
		m["Version"] = o.version
	}
	if o.output != "" {
		m["Output"] = o.output
	}
	if o.resume {
		m["Resume"] = true
	}
	if o.dryRun {
		m["DryRun"] = true
	}
	if o.installOnly {
		m["InstallOnly"] = true
	}
	if o.noOpen {
		m["NoOpen"] = true
	}
	if o.cleanup != "" {
		m["Cleanup"] = strings.ToUpper(o.cleanup[:1]) + o.cleanup[1:]
	}
	if len(o.exportArgs) > 0 {
		m["ExportArgs"] = o.exportArgs
	}
	b, err := json.Marshal(m)
	if err != nil {
		h.t.Fatal(err)
	}
	cmd := exec.Command(h.inst.shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", h.psFile)
	cmd.Env = append(h.env(o), "PIECES_TEST_INSTALLER="+h.inst.script, "PIECES_TEST_RELEASE="+h.release, "PIECES_TEST_OPTIONS="+string(b))
	detach(cmd)
	return cmd
}

type result struct {
	code int
	out  string
}

func (h *harness) run(o options) result {
	h.t.Helper()
	b, err := h.command(o).CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			h.t.Fatalf("installer could not execute: %v", err)
		}
		code = exit.ExitCode()
	}
	return result{code: code, out: string(b)}
}

type invocation struct {
	Args   []string `json:"args"`
	Answer string   `json:"answer"`
}

func (h *harness) invocations() []invocation {
	h.t.Helper()
	b, err := os.ReadFile(h.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	var calls []invocation
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		var c invocation
		if err := json.Unmarshal(s.Bytes(), &c); err != nil {
			h.t.Fatal(err)
		}
		calls = append(calls, c)
	}
	return calls
}

// flagValues returns every value supplied for name, in either form.
func flagValues(args []string, name string) []string {
	var values []string
	for i, a := range args {
		if a == name && i+1 < len(args) {
			values = append(values, args[i+1])
		} else if strings.HasPrefix(a, name+"=") {
			values = append(values, strings.TrimPrefix(a, name+"="))
		}
	}
	return values
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func (h *harness) sessions() []string {
	h.t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.data, "recovery"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(h.data, "recovery", e.Name()))
		}
	}
	return dirs
}

func (h *harness) toolPath() string {
	return filepath.Join(h.data, "tool", fixtureVersion, executableName())
}

func (h *harness) assertTool(r result, want bool) {
	h.t.Helper()
	_, err := os.Stat(h.toolPath())
	if (err == nil) != want {
		h.t.Fatalf("tool retained=%t want %t\n%s", err == nil, want, r.out)
	}
	if want {
		b, err := exec.Command(h.toolPath(), "version").CombinedOutput()
		if err != nil || !strings.Contains(string(b), fixtureVersion) {
			h.t.Fatalf("retained tool does not run: %v %s", err, b)
		}
	}
}

func (h *harness) assertNoStaging(r result) {
	h.t.Helper()
	leftovers, _ := filepath.Glob(filepath.Join(h.temp, "pieces-export.*"))
	downloads, _ := filepath.Glob(filepath.Join(h.data, "tool", ".download*"))
	if len(leftovers)+len(downloads) > 0 {
		h.t.Fatalf("download staging left behind: %v %v\n%s", leftovers, downloads, r.out)
	}
}

func assertFinalized(t *testing.T, dir string, want bool, r result) {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, "manifest.json"))
	if (err == nil) != want {
		t.Fatalf("finalized export at %s = %t, want %t\n%s", dir, err == nil, want, r.out)
	}
}

func eachInstaller(t *testing.T, test func(t *testing.T, inst installer, binary []byte)) {
	t.Helper()
	for _, c := range []struct {
		name string
		pick func(*testing.T) installer
	}{{"bash", bashInstaller}, {"powershell", powerShellInstaller}} {
		t.Run(c.name, func(t *testing.T) {
			inst := c.pick(t)
			test(t, inst, fixtureBinary(t))
		})
	}
}

func TestDefaultExportKeepsToolUsesMarkdownAndCleansRecovery(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		out := filepath.Join(t.TempDir(), "export with spaces")
		r := h.run(options{baseURL: true, output: out, exportArgs: []string{"--fixture-label=spaces $ literal"}})
		if r.code != 0 {
			t.Fatalf("exit %d\n%s", r.code, r.out)
		}
		calls := h.invocations()
		if len(calls) != 1 || calls[0].Args[0] != "export" {
			t.Fatalf("want one export invocation, got %v\n%s", calls, r.out)
		}
		args := calls[0].Args
		if f := flagValues(args, "--format"); len(f) != 1 || f[0] != "markdown" {
			t.Fatalf("format flags %v, want exactly markdown: %v", f, args)
		}
		if o := flagValues(args, "--output"); len(o) != 1 || o[0] != out {
			t.Fatalf("output flags %v: %v", o, args)
		}
		if !contains(args, "--fixture-label=spaces $ literal") {
			t.Fatalf("CLI arguments not passed literally: %v", args)
		}
		work, keys := flagValues(args, "--work"), flagValues(args, "--recovery-keys")
		if len(work) != 1 || len(keys) != 1 {
			t.Fatalf("installer did not add private recovery folders: %v", args)
		}
		recoveryRoot := filepath.Join(h.data, "recovery") + string(filepath.Separator)
		if !strings.HasPrefix(work[0], recoveryRoot) || filepath.Dir(work[0]) != filepath.Dir(keys[0]) {
			t.Fatalf("recovery folders %q %q are not one session under %q", work[0], keys[0], recoveryRoot)
		}
		assertFinalized(t, out, true, r)
		if s := h.sessions(); len(s) != 0 {
			t.Fatalf("finished export left recovery data behind: %v\n%s", s, r.out)
		}
		h.assertTool(r, true)
		h.assertNoStaging(r)
	})
}

func TestPartialExportStillFinalizesAndCleansRecovery(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		out := filepath.Join(t.TempDir(), "export")
		r := h.run(options{baseURL: true, output: out, exportArgs: []string{"--test-exit=2"}})
		if r.code != 2 {
			t.Fatalf("exit %d, want 2\n%s", r.code, r.out)
		}
		assertFinalized(t, out, true, r)
		if len(flagValues(h.invocations()[0].Args, "--work")) != 1 {
			t.Fatalf("export ran without recovery folders:\n%s", r.out)
		}
		if s := h.sessions(); len(s) != 0 {
			t.Fatalf("partial-but-final export left recovery data: %v", s)
		}
	})
}

func TestInterruptedExportKeepsRecoveryAndResumeFinishesIt(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		first := filepath.Join(t.TempDir(), "first")
		r := h.run(options{baseURL: true, output: first, exportArgs: []string{"--test-exit=130"}})
		if r.code != 130 {
			t.Fatalf("exit %d, want 130\n%s", r.code, r.out)
		}
		assertFinalized(t, first, false, r)
		sessions := h.sessions()
		if len(sessions) != 1 {
			t.Fatalf("interrupted export should keep one recovery session, got %v\n%s", sessions, r.out)
		}
		if !strings.Contains(r.out, "-Resume") && !strings.Contains(r.out, "--resume") {
			t.Fatalf("interrupted run did not explain how to resume:\n%s", r.out)
		}
		started := h.invocations()[0].Args

		second := filepath.Join(t.TempDir(), "second")
		r = h.run(options{baseURL: true, resume: true, output: second})
		if r.code != 0 {
			t.Fatalf("resume exit %d\n%s", r.code, r.out)
		}
		calls := h.invocations()
		if len(calls) != 2 {
			t.Fatalf("want export then resume, got %v", calls)
		}
		resumed := calls[1].Args
		if resumed[0] != "resume" {
			t.Fatalf("second invocation is not resume: %v", resumed)
		}
		for _, flag := range []string{"--work", "--recovery-keys"} {
			if got, want := flagValues(resumed, flag), flagValues(started, flag); len(got) != 1 || got[0] != want[0] {
				t.Fatalf("resume %s = %v, want the interrupted export's %v", flag, got, want)
			}
		}
		if o := flagValues(resumed, "--output"); len(o) != 1 || o[0] != second {
			t.Fatalf("resume output %v, want %s", o, second)
		}
		if f := flagValues(resumed, "--format"); len(f) != 0 {
			t.Fatalf("resume does not accept --format, got %v", f)
		}
		assertFinalized(t, second, true, r)
		if s := h.sessions(); len(s) != 0 {
			t.Fatalf("finished resume left recovery data: %v", s)
		}
	})
}

func TestFailureBeforeCaptureLeavesNothingToResume(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		r := h.run(options{baseURL: true, output: filepath.Join(t.TempDir(), "export"), exportArgs: []string{"--test-exit=1", "--test-no-capture"}})
		if r.code != 1 {
			t.Fatalf("exit %d, want 1\n%s", r.code, r.out)
		}
		if s := h.sessions(); len(s) != 0 {
			t.Fatalf("empty recovery session kept: %v", s)
		}
		if strings.Contains(r.out, "-Resume") || strings.Contains(r.out, "--resume") {
			t.Fatalf("offered to resume when nothing was saved:\n%s", r.out)
		}
		h.assertTool(r, true)
	})
}

func TestResumeWithoutSavedExportFails(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		r := h.run(options{baseURL: true, resume: true})
		if r.code != 1 || !strings.Contains(strings.ToLower(r.out), "no unfinished export") {
			t.Fatalf("exit %d\n%s", r.code, r.out)
		}
		if len(h.invocations()) != 0 {
			t.Fatal("ran the CLI without a saved export")
		}
	})
}

func TestExplicitFormatIsRespectedWithoutRecovery(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		out := filepath.Join(t.TempDir(), "export")
		r := h.run(options{baseURL: true, output: out, exportArgs: []string{"--format", "both"}})
		if r.code != 0 {
			t.Fatalf("exit %d\n%s", r.code, r.out)
		}
		args := h.invocations()[0].Args
		if f := flagValues(args, "--format"); len(f) != 1 || f[0] != "both" {
			t.Fatalf("format flags %v, want only the user's: %v", f, args)
		}
		if len(flagValues(args, "--work")) != 0 {
			t.Fatalf("recovery added to a PDF export, which the CLI rejects: %v", args)
		}
		assertFinalized(t, out, true, r)
	})
}

func TestUserRecoveryFoldersArePassedThrough(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		private := t.TempDir()
		work, keys := filepath.Join(private, "work"), filepath.Join(private, "keys")
		r := h.run(options{baseURL: true, output: filepath.Join(t.TempDir(), "export"), exportArgs: []string{"--work", work, "--recovery-keys", keys}})
		if r.code != 0 {
			t.Fatalf("exit %d\n%s", r.code, r.out)
		}
		args := h.invocations()[0].Args
		if w := flagValues(args, "--work"); len(w) != 1 || w[0] != work {
			t.Fatalf("work flags %v: %v", w, args)
		}
		if s := h.sessions(); len(s) != 0 {
			t.Fatalf("installer created its own session despite user folders: %v", s)
		}
		if _, err := os.Stat(work); err != nil {
			t.Fatal("installer removed a user-managed workspace")
		}
	})
}

func TestDryRunScansWithoutRecoveryOrOutput(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		for name, o := range map[string]options{
			"installer option": {baseURL: true, dryRun: true},
			"passed through":   {baseURL: true, exportArgs: []string{"--dry-run"}},
		} {
			t.Run(name, func(t *testing.T) {
				h := newHarness(t, inst, binary, "complete")
				r := h.run(o)
				if r.code != 0 {
					t.Fatalf("exit %d\n%s", r.code, r.out)
				}
				args := h.invocations()[0].Args
				if !contains(args, "--dry-run") || len(flagValues(args, "--work")) != 0 || len(flagValues(args, "--output")) != 0 {
					t.Fatalf("dry run arguments %v", args)
				}
				if s := h.sessions(); len(s) != 0 {
					t.Fatalf("dry run created recovery data: %v", s)
				}
				if exports, _ := filepath.Glob(filepath.Join(h.home, "Documents", "Pieces-Exports", "*")); len(exports) != 0 {
					t.Fatalf("dry run created export folders: %v", exports)
				}
			})
		}
	})
}

func TestCleanupChoices(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		for _, tc := range []struct {
			cleanup string
			keep    bool
		}{{"keep", true}, {"remove", false}, {"ask", true}} {
			t.Run(tc.cleanup, func(t *testing.T) {
				h := newHarness(t, inst, binary, "complete")
				out := filepath.Join(t.TempDir(), "export")
				r := h.run(options{baseURL: true, output: out, cleanup: tc.cleanup})
				if r.code != 0 {
					t.Fatalf("exit %d\n%s", r.code, r.out)
				}
				h.assertTool(r, tc.keep)
				assertFinalized(t, out, true, r)
				h.assertNoStaging(r)
			})
		}
	})
}

func TestInstallOnlyKeepsToolWithoutExporting(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		r := h.run(options{baseURL: true, installOnly: true})
		if r.code != 0 {
			t.Fatalf("exit %d\n%s", r.code, r.out)
		}
		if len(h.invocations()) != 0 {
			t.Fatal("install-only ran an export")
		}
		h.assertTool(r, true)
		h.assertNoStaging(r)
	})
}

func TestUntrustedDownloadsNeverRun(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		for _, scenario := range []string{"corrupt", "duplicate-checksum", "traversal", "missing-checksums", "missing-archive"} {
			t.Run(scenario, func(t *testing.T) {
				h := newHarness(t, inst, binary, scenario)
				r := h.run(options{baseURL: true, output: filepath.Join(t.TempDir(), "export")})
				want := 1
				if inst.name == "bash" && strings.HasPrefix(scenario, "missing-") {
					want = 22 // curl's HTTP failure status
				}
				if r.code != want {
					t.Fatalf("exit %d, want %d\n%s", r.code, want, r.out)
				}
				if len(h.invocations()) != 0 {
					t.Fatal("executed an unverified download")
				}
				h.assertTool(r, false)
				h.assertNoStaging(r)
				if s := h.sessions(); len(s) != 0 {
					t.Fatalf("recovery session created for a failed download: %v", s)
				}
			})
		}
	})
}

func TestDefaultLocationsAreDocumentsAndUserAppData(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows resolves Documents and LocalAppData from the user profile, which a test cannot redirect")
		}
		h := newHarness(t, inst, binary, "complete")
		r := h.run(options{baseURL: true, defaultData: true})
		if r.code != 0 {
			t.Fatalf("exit %d\n%s", r.code, r.out)
		}
		exports, _ := filepath.Glob(filepath.Join(h.home, "Documents", "Pieces-Exports", "*", "manifest.json"))
		if len(exports) != 1 {
			t.Fatalf("export not under Documents/Pieces-Exports: %v\n%s", exports, r.out)
		}
		name := filepath.Base(filepath.Dir(exports[0]))
		if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$`).MatchString(name) {
			t.Fatalf("export folder %q is not a readable date and time", name)
		}
		data := filepath.Join(h.home, ".local", "share", "pieces-export")
		if runtime.GOOS == "darwin" {
			data = filepath.Join(h.home, "Library", "Application Support", "Pieces Export")
		}
		if _, err := os.Stat(filepath.Join(data, "tool", fixtureVersion, executableName())); err != nil {
			t.Fatalf("tool not kept under %s\n%s", data, r.out)
		}
	})
}

func TestBuiltInReleaseRejectsOtherVersionsOffline(t *testing.T) {
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		r := h.run(options{version: "9.9.9"})
		if r.code != 1 || !strings.Contains(r.out, "9.9.9") {
			t.Fatalf("exit %d\n%s", r.code, r.out)
		}
		if len(h.invocations()) != 0 {
			t.Fatal("ran a CLI for an unknown version")
		}
		h.assertNoStaging(r)
	})
}

// A one-line install runs the script through Invoke-Expression in the
// user's own session. An exit statement there closes their window.
func TestPowerShellOneLinerDoesNotEndTheSession(t *testing.T) {
	inst := powerShellInstaller(t)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	command := `iex (Get-Content -Raw -LiteralPath $env:PIECES_TEST_INSTALLER); "after-iex exit=$LASTEXITCODE"`
	cmd := exec.Command(inst.shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "PIECES_TEST_INSTALLER="+inst.script, "PIECES_EXPORT_HOME="+blocker)
	detach(cmd)
	b, _ := cmd.CombinedOutput()
	if !strings.Contains(string(b), "after-iex exit=1") {
		t.Fatalf("session did not continue after the installer failed:\n%s", b)
	}
	// Run as a script file, the same failure is still the process exit code.
	cmd = exec.Command(inst.shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", inst.script)
	cmd.Env = append(os.Environ(), "HOME="+home, "PIECES_EXPORT_HOME="+blocker)
	detach(cmd)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("script-file failure exit = %v, want 1", err)
	}
}

// Windows PowerShell 5.1 has no ProcessStartInfo.ArgumentList, so arguments
// are quoted into one command line. Expected strings follow the documented
// CommandLineToArgvW rules.
func TestPowerShellArgumentQuoting(t *testing.T) {
	inst := powerShellInstaller(t)
	cases := []struct{ in, want string }{
		{`plain`, `plain`},
		{`with space`, `"with space"`},
		{``, `""`},
		{`quote"inside`, `"quote\"inside"`},
		{`trailing\`, `trailing\`},
		{`dir with space\`, `"dir with space\\"`},
		{`a\"b`, `"a\\\"b"`},
	}
	var script strings.Builder
	script.WriteString(". $env:PIECES_TEST_INSTALLER\n")
	for _, c := range cases {
		fmt.Fprintf(&script, "'<' + (ConvertTo-PiecesArgument '%s') + '>'\n", strings.ReplaceAll(c.in, "'", "''"))
	}
	path := filepath.Join(t.TempDir(), "quote.ps1")
	if err := os.WriteFile(path, []byte(script.String()), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(inst.shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path)
	cmd.Env = append(os.Environ(), "PIECES_TEST_INSTALLER="+inst.script)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n")), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("got %d lines for %d cases:\n%s", len(lines), len(cases), b)
	}
	for i, c := range cases {
		if lines[i] != "<"+c.want+">" {
			t.Errorf("ConvertTo-PiecesArgument(%q) = %s, want <%s>", c.in, lines[i], c.want)
		}
	}

	// The quoted command line must also survive a real process launch.
	binary := fixtureBinary(t)
	fixture := filepath.Join(t.TempDir(), executableName())
	if err := os.WriteFile(fixture, binary, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "fixture.log")
	want := []string{"export", "--dry-run", "with space", "quote\"inside", "dir with space\\", `a\"b`, "--fixture-label=spaces $ literal"}
	launch := ". $env:PIECES_TEST_INSTALLER\n$a = $env:PIECES_TEST_ARGS | ConvertFrom-Json\nexit (Start-PiecesProcess -FilePath $env:PIECES_TEST_FIXTURE -Arguments ([string[]]$a) -UseArgumentString)\n"
	if err := os.WriteFile(path, []byte(launch), 0600); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(want)
	cmd = exec.Command(inst.shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path)
	cmd.Env = append(os.Environ(), "PIECES_TEST_INSTALLER="+inst.script, "PIECES_TEST_FIXTURE="+fixture, "PIECES_FIXTURE_LOG="+log, "PIECES_TEST_ARGS="+string(encoded))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	b, err = os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var got invocation
	if err := json.Unmarshal(bytes.TrimSpace(b), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments changed through the command line:\n got %q\nwant %q", got.Args, want)
	}
}

// The CLI asks "Export now? [Y/n]" without a trailing newline. The prompt
// must reach a real terminal before the user answers.
func TestPromptIsVisibleInARealTerminal(t *testing.T) {
	expect, err := exec.LookPath("expect")
	if err != nil {
		t.Skip("expect unavailable")
	}
	eachInstaller(t, func(t *testing.T, inst installer, binary []byte) {
		h := newHarness(t, inst, binary, "complete")
		out := filepath.Join(t.TempDir(), "export")
		o := options{baseURL: true, output: out, exportArgs: []string{"--test-prompt"}}
		o.noOpen = true
		cmd := h.command(o)
		script := filepath.Join(t.TempDir(), "prompt.exp")
		program := `set timeout 60
log_user 1
spawn -noecho {*}$argv
expect {
  -exact "Export now? \[Y/n\]" { send "n\r" }
  timeout { puts "\nPROMPT-NOT-VISIBLE"; exit 3 }
  eof { puts "\nEXITED-WITHOUT-PROMPT"; exit 4 }
}
expect eof
catch wait status
exit [lindex $status 3]
`
		if err := os.WriteFile(script, []byte(program), 0600); err != nil {
			t.Fatal(err)
		}
		// PowerShell must run interactively here so its console is the terminal.
		var shellArgs []string
		for _, a := range cmd.Args[1:] {
			if a != "-NonInteractive" {
				shellArgs = append(shellArgs, a)
			}
		}
		run := exec.Command(expect, append([]string{script, cmd.Path}, shellArgs...)...)
		run.Env = cmd.Env
		b, err := run.CombinedOutput()
		if err != nil {
			t.Fatalf("prompt check failed: %v\n%s", err, b)
		}
		calls := h.invocations()
		if len(calls) != 1 || calls[0].Answer != "n" {
			t.Fatalf("CLI did not read the answer typed at the prompt: %v\n%s", calls, b)
		}
	})
}
