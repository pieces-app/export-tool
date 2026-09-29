# Binary distribution and installation

Updated 2026-09-29. The application is closed source under [LICENSE.txt](LICENSE.txt). Binaries are intentionally unsigned and unnotarized. The repository stays private; only platform ZIPs, checksums, notices, download instructions, and the two bootstrap scripts are distributed. This document describes the release procedure, not a claim that production acceptance has passed.

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
- PowerShell 7 on macOS: same package/execution/cleanup tests passed with transport mocked; the actual HTTPS download helper separately retrieved a pinned public README with certificate validation enabled. This is not Windows runtime evidence.
- Both scripts reject corrupted hashes, duplicate checksum entries, and ZIP path traversal before executing any binary.
- Native macOS ARM64 and a local Linux ARM64 Docker VM: packaged synthetic end-to-end acceptance passed. The Linux container had no Go, no network, and no private export/source mounts; exporter, CLI, and fake-lifecycle tests also passed. macOS AMD64 passed under Rosetta, which is not Intel hardware acceptance. GUI metadata and installed-OS lifecycle remain separate tests.
- Workflow passed actionlint. GitHub Actions attempts returned startup failures with no jobs; the user confirmed outstanding billing prevents Actions until next week. Do not retry CI until billing is resolved. Windows and remote native runners remain unverified.

Record full live migration and published-URL install evidence in [TODO.md](TODO.md) before marking the release production ready.

The `0.6.0-dev` packages add optional [SDK-cache recovery](SDK_CACHE_RECOVERY.md) and a UUID/payment-card false-positive fix. All six packages compile with CGO disabled and include SQLite/libc notices. Packaged ordinary and cache-recovery acceptance passed on macOS ARM64, Linux ARM64 in the isolated local VM, and macOS AMD64 under Rosetta. These remain development builds: the full live migration, remote native matrix, GUI behavior, and GCP download path are still unverified.

The local `0.7.0-dev` packages add the explicit summaries scope and batched supporting-reference reads. All six checksums/package members were verified. Packaged ordinary/cache/summaries acceptance passed on macOS ARM64, local Linux ARM64, and macOS AMD64 under Rosetta. No binaries were uploaded; Windows/Intel-hardware/runtime acceptance and the configured GCP download path remain pending. The prepared workflow includes the new scoped acceptance case, but Actions remains on the billing hold.

The `0.8.2-dev` packages added offline rebuilding and archive format 5 reconstruction evidence. All six packages compile and pass checksum/member verification. Actual packaged ordinary, historical-cache, summaries-scope, and offline-rebuild acceptance passed on macOS ARM64, Linux ARM64 in the isolated local VM, and macOS AMD64 under Rosetta. Rebuilding was exercised with the fixture OS closed. These packages superseded the intermediate local 0.8.0/0.8.1 builds.

The current local packages are `0.8.3-dev`, adding conservative reconciliation of explicit legacy person-selection reports. All six package checksums and exact four-member layouts passed. The same three local runtime environments passed all packaged cases, including legacy rebuilding that retains unknown people. Compatibility was also checked against an archive produced by the actual older 0.4.x executable on a synthetic server. No upload/publication or GitHub Actions retry occurred; live recovery and measured person reduction, Windows/native Intel/GUI acceptance, and the GCP download path remain open.
