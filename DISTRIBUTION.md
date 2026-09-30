# Binary distribution and installation

Updated 2026-09-30. The application is closed source under [LICENSE.txt](LICENSE.txt). Binaries are intentionally unsigned and unnotarized. The repository stays private; only platform ZIPs, checksums, notices, download instructions, and the two bootstrap scripts are distributed. This document describes the release procedure, not a claim that production acceptance has passed.

The latest local packages are `0.15.0-dev` under `dist/0.15.0-dev/`, adding opt-in completed-source recovery. All six ZIP hashes, exact four-file layouts and extracted-file equality were verified. Actual-package default/projected-summary/rebuild/recovery checks passed on macOS ARM64 and Rosetta AMD64. Bash HTTPS and PowerShell 7 with substituted file transport passed actual-ZIP installation/export/cleanup tests, including retained external recovery workspace/keys after utility removal. Linux could not run this revision because Docker Desktop is stopped; earlier Linux results belong to older builds. Windows binaries compile but remain unverified natively. Full race checks and vet passed. The old all-data export is still active; a new live summaries attempt stopped before archive creation because Pieces OS is no longer running. No binaries have been uploaded, no GCP base is configured, and Actions remains on the billing hold. See [the current checklist](TODO.md#current-checkpoint--2026-09-30) and [installer evidence](INSTALLER_ACCEPTANCE.md); versioned notes below are historical.

## Installation UX

1. The user runs the Bash bootstrap on macOS/Linux or the PowerShell bootstrap on Windows. No administrator permissions or package manager are needed. The CLI itself needs no Go/Python/.NET installation; the Windows bootstrap uses built-in PowerShell/.NET facilities.
2. Require an explicit HTTPS release base URL and pinned version. Detect OS/CPU, create a unique temporary installation, and download that release's SHA256SUMS.txt and matching ZIP. Do not alter PATH or shell startup files.
3. Check SHA-256 before executing anything. Reject absent/duplicate checksum entries and any archive members beyond the exact executable, README.md, LICENSE.txt, and THIRD_PARTY_NOTICES.txt. Copy those named entries into new regular files instead of extracting archive paths or symlinks.
4. Put exports outside the temporary installation. The default is `Documents/Pieces-Exports/<timestamp>` under the user account. An explicit output path must be new. Ordinary CLI discovery, inventory, format choice, privacy settings, and export confirmation still apply.
5. Run the CLI and preserve its exit status: zero completed for implemented scope, two partial archive, one fatal error, or an interruption status. Installation success is not export success.
6. Ask `Remove the downloaded CLI and installer files? Export files will stay. [Y/n]`. Yes removes only the unique installation, package, and checksum file. No keeps the CLI and prints its path. Download/setup failures clean the failed installation automatically. EOF/no terminal retains a verified installation; unattended callers choose cleanup explicitly. Exports, partial exports, user policies, and Pieces applications are never deleted by installer cleanup.

The Gist bootstrap can run from memory, so there is no separate saved bootstrap file to uninstall. If someone saves it manually, that user-chosen file remains theirs to remove. The installer does not delete an arbitrary path it was launched from.

## Bootstrap commands

Repository commands, before publication:

```sh
bash install/install.sh --base-url https://storage.googleapis.com/BUCKET/releases \
  --version VERSION --output "$HOME/Documents/Pieces-Export"

# Optional CLI flags follow --. An unattended export needs explicit approval.
bash install/install.sh --base-url https://storage.googleapis.com/BUCKET/releases \
  --version VERSION --remove -- --yes --format markdown --people profiles
```

```powershell
& ./install/install.ps1 -BaseUrl 'https://storage.googleapis.com/BUCKET/releases' `
  -Version 'VERSION' -Output "$HOME\Documents\Pieces-Export"

& ./install/install.ps1 -BaseUrl 'https://storage.googleapis.com/BUCKET/releases' `
  -Version 'VERSION' -Cleanup Remove -ExportArgs @('--yes', '--format', 'markdown')
```

`--keep` / `-Cleanup Keep` skips cleanup. `--install-only` / `-InstallOnly` verifies and retains a CLI without exporting. Export flags cannot override `--output`; choose the destination through the installer option. Only Bash needs `curl`, `unzip`, and `sha256sum` or `shasum`. Neither script changes execution policy, removes quarantine attributes, or disables platform security checks.

The [installer Gist](https://gist.github.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1) is published as unlisted (anyone with its link can read it). Both files were retrieved through the GitHub API and matched to the tested local files by SHA-256. The commands below pin immutable raw revisions. Review the script before running it. The base URL and version remain explicit; replace BUCKET and VERSION with the published release values:

```sh
curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location \
  'https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/6d928715f5faf7bf4e9776be2a8b34c981abacab/install.sh' | bash -s -- \
  --base-url https://storage.googleapis.com/BUCKET/releases --version VERSION
```

```powershell
$bootstrap = (Invoke-WebRequest -UseBasicParsing -Uri 'https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/866cfdf5ec3192a7a43d82f4b3c4b34343f523de/install.ps1').Content
& ([scriptblock]::Create($bootstrap)) -BaseUrl 'https://storage.googleapis.com/BUCKET/releases' -Version 'VERSION'
```

Checksums fetched from the same HTTPS release origin detect corruption or mismatched files; they are not independent proof against a compromised publisher/bucket. Version objects should be immutable. The Gist does not contain application source, credentials, private fixtures, or exported records.

## GCP object layout

```text
gs://BUCKET/releases/
  VERSION/
    SHA256SUMS.txt
    pieces-export_VERSION_darwin_arm64.zip
    pieces-export_VERSION_darwin_amd64.zip
    pieces-export_VERSION_linux_arm64.zip
    pieces-export_VERSION_linux_amd64.zip
    pieces-export_VERSION_windows_arm64.zip
    pieces-export_VERSION_windows_amd64.zip
```

Public downloads use `https://storage.googleapis.com/BUCKET/OBJECT`; browser-console URLs require a different authentication flow and are not the installer URL. Configure the chosen distribution bucket/prefix deliberately; do not change an unrelated bucket's public-access policy. [Google public-object documentation](https://docs.cloud.google.com/storage/docs/access-public-data).

Build into a new version directory. This isolates checksums from earlier development builds:

```sh
go run ./cmd/release --version VERSION --output dist/VERSION
```

After acceptance, upload only those six ZIPs and that version's checksum file. Do not upload the repo, `exports/`, expanded working directories, or a wildcard spanning old releases. Set caching on the objects themselves; use long-lived caching for immutable versions, and no mutable `latest` redirect in the initial installer. Google serves cache headers from object metadata. [Cloud Storage metadata](https://docs.cloud.google.com/storage/docs/metadata).

The GCP bucket/base URL is still awaiting configuration. No binary upload or bucket IAM change has been performed. The Gist contains development bootstrap scripts; publication of production binaries remains gated on the actual export/runtime evidence.

## Native CI and verification

[native-acceptance.yml](.github/workflows/native-acceptance.yml) prepares native ARM64 and AMD64 jobs on macOS, Linux, and Windows. It checks the actual Go host architecture, executes tests/vet, runs race tests on supported targets, scans dependencies, builds binary-only packages, and exercises the built executable against a synthetic loopback OS. Windows also runs the installer tests with Windows PowerShell 5.1. Runner labels come from GitHub's standard private-repository runner matrix. [GitHub runner documentation](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).

The synthetic binary test verifies doctor/scan/export, Markdown/PDF output, summary bodies, user/persona placement, per-type counts, privacy checks, and local links after moving the archive. It never contacts or launches an installed OS. CI uploads only binary ZIPs/checksums; it does not publish to GCP or Gist. Actual installed-OS migration, app lifecycle, file-manager behavior, and PDF-viewer behavior are separate acceptance requirements.

Local evidence so far:

- Bash installer: HTTPS download against a temporary trusted test server, real SHA/ZIP validation, native fixture execution, exit statuses, literal arguments, and cleanup/retention passed.
- Actual `0.8.6-dev` ZIP: Bash HTTPS → scoped Markdown/PDF export → cleanup/retention passed on macOS ARM64 and isolated Linux ARM64. PowerShell 7 on macOS passed the same actual-package/export cases with file transport substituted for HTTPS. Genuine terminal cleanup prompts were also exercised on macOS. See [installer acceptance and reproduction](INSTALLER_ACCEPTANCE.md), including the executable-temporary-directory requirement and open GCP/Windows gates.
- PowerShell 7 on macOS: same package/execution/cleanup tests passed with transport mocked; the actual HTTPS download helper separately retrieved a pinned public README with certificate validation enabled. This is not Windows runtime evidence.
- Both scripts reject corrupted hashes, duplicate checksum entries, and ZIP path traversal before executing any binary.
- Native macOS ARM64 and a local Linux ARM64 Docker VM: packaged synthetic end-to-end acceptance passed. The Linux container had no Go, no network, and no private export/source mounts; exporter, CLI, and fake-lifecycle tests also passed. macOS AMD64 passed under Rosetta, which is not Intel hardware acceptance. GUI metadata and installed-OS lifecycle remain separate tests.
- Workflow passed actionlint. GitHub Actions attempts returned startup failures with no jobs; the user confirmed outstanding billing prevents Actions until next week. Do not retry CI until billing is resolved. Windows and remote native runners remain unverified.

Record full live migration and published-URL install evidence in [TODO.md](TODO.md) before marking the release production ready.

The `0.6.0-dev` packages add optional [SDK-cache recovery](SDK_CACHE_RECOVERY.md) and a UUID/payment-card false-positive fix. All six packages compile with CGO disabled and include SQLite/libc notices. Packaged ordinary and cache-recovery acceptance passed on macOS ARM64, Linux ARM64 in the isolated local VM, and macOS AMD64 under Rosetta. These remain development builds: the full live migration, remote native matrix, GUI behavior, and GCP download path are still unverified.

The local `0.7.0-dev` packages add the explicit summaries scope and batched supporting-reference reads. All six checksums/package members were verified. Packaged ordinary/cache/summaries acceptance passed on macOS ARM64, local Linux ARM64, and macOS AMD64 under Rosetta. No binaries were uploaded; Windows/Intel-hardware/runtime acceptance and the configured GCP download path remain pending. The prepared workflow includes the new scoped acceptance case, but Actions remains on the billing hold.

The `0.8.2-dev` packages added offline rebuilding and archive format 5 reconstruction evidence. All six packages compile and pass checksum/member verification. Actual packaged ordinary, historical-cache, summaries-scope, and offline-rebuild acceptance passed on macOS ARM64, Linux ARM64 in the isolated local VM, and macOS AMD64 under Rosetta. Rebuilding was exercised with the fixture OS closed. These packages superseded the intermediate local 0.8.0/0.8.1 builds.

The `0.8.3-dev` packages added conservative reconciliation of explicit legacy person-selection reports. All six package checksums and exact four-member layouts passed. The same three local runtime environments passed all packaged cases, including legacy rebuilding that retains unknown people. Compatibility was also checked against an archive produced by the actual older 0.4.x executable on a synthetic server.

The `0.8.4-dev` packages added exact bounded related-summary ranking, cached recency ranks, and reuse of inline/sibling sections. All six checksums and binary-only layouts passed. Actual packaged ordinary/cache/summaries/rebuild/legacy-rebuild acceptance passed on macOS ARM64, local Linux ARM64, and macOS AMD64 under Rosetta; the Linux VM also passed the complete-sort equivalence fixtures. See TODO for local microbenchmarks and their limits. No upload/publication or GitHub Actions retry occurred; live recovery and measured person reduction, Windows/native Intel/GUI acceptance, and the GCP download path remain open.

The `0.8.5-dev` packages replaced PDF-to-Markdown URI links with relative PDF file actions and explicit plain-label fallbacks. All six checksums/member lists passed. Actual packaged acceptance passed on macOS ARM64, macOS AMD64 under Rosetta, and isolated Linux ARM64; Linux also passed the new link regressions. The download README describes filename/viewer limits. [Native GUI evidence](NATIVE_GUI_ACCEPTANCE.md) records the Preview permission prerequisite and the Finder Comments limitation; neither is hidden behind the parser or metadata-readback results. No binaries were published and GitHub Actions remains paused.

The `0.8.6-dev` packages added failed finalization status, content-free filesystem errors, and exclusive native destination finalization. All six checksums and exact package layouts passed. Actual packaged acceptance passed on macOS ARM64, macOS AMD64 under Rosetta, and isolated Linux ARM64. The Linux packaged executable also passed a real disk-full PDF conversion test with exit 1 and no finalized archive. Windows runtime and long-path checks are still pending. [Failure-testing instructions](FAILURE_TESTING.md) explain the isolated test volume and remaining crash/resume limits. Actions remains on hold; no publication occurred.

For the pinned local vulnerability check, build the analyzer with the project's Go version: `GOTOOLCHAIN=go1.27.1 go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`. The `0.8.6-dev` native scan found no reachable vulnerabilities; 3 imported-package and 18 required-module advisories were outside detected called paths.

The `0.8.7-dev` packages added explicit signal relationship coverage and conservative reading of older signal reconstruction evidence. Six checksums and exact four-file ZIP layouts passed. Actual packaged acceptance passed on macOS ARM64, macOS AMD64 under Rosetta, and isolated Linux ARM64. The actual-package Bash HTTPS installer passed on macOS/Linux ARM64; PowerShell 7 on macOS passed with transport substituted. Actual `0.8.6-dev` archives rebuilt offline on macOS/Linux with all seven original signal projections honestly unknown. Full race tests, vet, actionlint, and the pinned vulnerability scan passed (zero reachable findings; unchanged non-reachable advisory counts). Native Windows/Intel hardware, viewers, full live migration/recovery, and GCP publication remain open.

The preceding local packages were `0.8.8-dev`. They avoid rewriting unchanged canonical JSON during privacy reconciliation/rendering and add privacy-phase record progress. All records still receive the full scan and final audits. All six package checksums/layouts passed; packaged ordinary/cache/summaries/rebuild/legacy-rebuild acceptance passed on macOS ARM64, Rosetta AMD64, and isolated Linux ARM64. Actual-package installer cases passed on macOS/Linux ARM64, with PowerShell's file-transport limitation unchanged. Linux real disk-full cases still passed, including the packaged PDF failure. Full race tests, vet, actionlint, and the pinned vulnerability scan passed (zero reachable findings). The running live export remains on `0.4.1-dev`; these improvements do not change it in place. No binaries were uploaded or Actions jobs retried.

The preceding local packages were `0.8.9-dev`, adding configurable per-document PDF input/page/output limits, checks within long-block rendering, page progress, bounded font reads, and sticky writer errors. All six binary-only ZIP hashes/layouts passed. Actual packaged ordinary/cache/summaries/rebuild/legacy-rebuild/PDF-limit acceptance passed on macOS ARM64, Rosetta AMD64, and isolated Linux ARM64. The new failure/retry case finishes Markdown, closes the synthetic OS, fails a one-page PDF rebuild without finalizing, then succeeds offline with explicit budgets recorded in the manifest. macOS/Linux Bash installer and macOS PowerShell file-transport tests passed; Windows test compilation is not native execution. Linux ENOSPC tests still passed, including the packaged PDF failure. No binaries were uploaded, root executable replaced, or Actions jobs retried.

For `0.8.9-dev`, the final full race suite, vet, actionlint, and pinned vulnerability scan passed (zero reachable findings). PDF visual/parsing evidence and remaining synchronous-library/cross-platform limits are documented in NATIVE_GUI_ACCEPTANCE.md and FAILURE_TESTING.md.

The preceding local packages were `0.8.10-dev`, correcting signal/description dependency filtering and adding explicit legacy privacy handling. All six checksum/four-file ZIP checks passed. Actual packaged ordinary/cache/summaries/rebuild/legacy-rebuild/PDF-limit/signal-privacy tests passed on macOS ARM64, Rosetta AMD64, and isolated Linux ARM64. The actual older `0.8.9-dev` writer reproduced a source-filtering gap on macOS/Linux ARM64; corrected offline rebuild withheld the unverified signals/descriptions without changing the source. Bash installer cases passed on macOS/Linux ARM64; PowerShell cases passed on macOS with substituted file transport. Linux actual ENOSPC failures still stopped without finalization. Windows test compilation passed; native Windows execution and public hosting remain outstanding. No binaries were uploaded or Actions jobs run.

For `0.8.10-dev`, full race tests, vet, actionlint, and the pinned vulnerability scan passed (zero reachable findings). Signal body/projection gaps and the consolidated digest remain separate unfinished work; this version does not certify a complete live migration.

The preceding local packages were `0.9.0-dev`, adding bounded split/single/off signal digests and all seven inverse signal relationship dimensions before privacy filtering. Six hashes/four-file layouts passed. Actual packaged export/cache/summaries/rebuild/legacy/PDF-limit/signal-privacy/digest cases passed on macOS ARM64, Rosetta AMD64, and isolated Linux ARM64, including installer cleanup and Linux disk-full behavior. Windows AMD64/ARM64 test executables compile; native execution is still open. Full race tests, vet, actionlint, and the pinned vulnerability scan passed with zero reachable findings. A PDF click-area issue discovered during visual review remains under correction; the full live archive and public hosting are unverified. No upload, root executable replacement, or Actions job occurred.

The earlier `0.9.1-dev` packages corrected PDF click areas around mixed linked/unlinked prose. All six hashes/four-file layouts passed, as did the actual packaged CLI cases on macOS ARM64, Rosetta AMD64, and isolated Linux ARM64. Bash/PowerShell installer cleanup and Linux actual disk-full failures passed with their previously documented platform/transport limits. Both Windows test architectures compile; native runtime, full live migration, and public GCP downloads remain unverified. The actual packaged macOS rebuild passed independent PDF text/action checks and visual digest review. No binary was uploaded and Actions remains on hold.

Final `0.9.1-dev` source verification: full race tests passed (exporter 215.811 seconds), plus vet, actionlint, and diff checks. The pinned vulnerability scan earlier in this pass found zero reachable findings; the PDF correction changed no dependencies. Full live reconciliation/recovery and native/public-download gates remain open.
