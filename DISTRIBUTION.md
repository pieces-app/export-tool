# Binary distribution and installation

Updated 2026-10-07. The application is closed source under [LICENSE.txt](LICENSE.txt). The original six-platform packages are unsigned; the current Mac email packages are Developer ID signed and notarized by Apple. The repository stays private; only platform ZIPs, checksums, notices, download instructions, and the two bootstrap scripts are distributed. This document describes the release procedure, not a claim that all-platform production acceptance has passed.

## Current Windows validation — 2026-10-07

The user confirmed credits should now be available and made Windows readiness the current priority. The older Actions hold below is historical and superseded. [Windows readiness](WINDOWS_READINESS.md) records the private validation branch, the source fix, candidate artifacts and unverified runtime boundaries. [Windows quickstart](WINDOWS_START_HERE.md) contains the consumer PowerShell commands.

GitHub currently rejects both acceptance and a minimal Windows-only diagnostic with `startup_failure` before any job exists, despite enabled repository/organization Actions permissions and clean local workflow syntax checks. The precise cause needs run-page annotations; no Windows runtime pass is claimed. The Windows downloads remain unsigned. Source stays private, and no public publication is authorized by this verification pass.

## Signed and notarized Mac email package — 2026-10-03

Use [MACOS_HANDOFF.md](MACOS_HANDOFF.md) for the current ARM64/Intel ZIPs, checksums, measured email sizes and [email draft](MACOS_EMAIL_DRAFT.md). These are separate `_notarized.zip` artifacts in `dist/macos-notarized-0.18.0-rc2/`; earlier packages remain intact. Apple accepted both submissions with no issues after the user resolved the agreement. The final ZIPs refresh documentation and retain the exact tested/notarized executable bytes. Each code hash matches its Apple ticket; online notarization requirements and execution from locally quarantined copies pass on ARM64 and Intel/Rosetta. No runtime code changed. Keep the Mac online for first launch; raw CLI/ZIP files do not support stapling. Only intended packages were uploaded to Apple; no public upload or email occurred.

## Presentation candidate — 0.18.0-rc2

Six replacement ZIPs and `SHA256SUMS.txt` are built in `dist/0.18.0-rc2/`. The new package uses readable local dates and links exact attached profile versions from summaries. Full source tests, CLI race tests and actual Mac ARM64 summary/rebuild/recovery checks pass. The actual Mac package rebuilt the accepted archive in America/New_York in 28m36s. Independent body/profile/graph checks, all 11,751 summary timestamps and all 23,502 native metadata documents pass; canonical records and source fingerprints reconcile. [Open the refreshed archive](exports/readable-local-20261002/export/index.md). Other-platform runtime acceptance below applies to rc1; rebuilding their binaries does not repeat that acceptance. Nothing has been uploaded or published.

### macOS trial handoff

For the current controlled Mac trial, use the notarized packages and matching checksums in [MACOS_HANDOFF.md](MACOS_HANDOFF.md). The older `darwin_arm64.zip` and `darwin_amd64.zip` described in this section remain unsigned. These are local artifacts, not published download URLs. The recipient-machine download experience remains part of the first external trial; local quarantined-copy checks pass.

On 2026-10-02, the six unpublished ZIPs received the expanded consumer guide. The executable, license, notices and executable permissions were preserved; all executable hashes still match the prior candidate. ZIP checksums changed with the README. The previous packages/checksums are retained privately under `exports/weekend-handoff-20261002/original-packages/`. Use the current files in `dist/0.18.0-rc2/` together, not a checksum file from before this refresh. Published version artifacts must remain immutable.

Actual rc2 headless checks passed with closed stdin and separately captured stdout/stderr on Apple silicon and with the Intel executable under Rosetta. They verify periodic progress, Local/UTC settings, finalized Markdown bodies/links, complete/partial exit codes and offline recovery inspection. The Intel run also repeats recovery checks. The refreshed Mac ZIP passed the local HTTPS Bash installer test. No runtime code changed and no second large export was required. Native Windows, native Intel hardware, public-host downloads and recipient-specific security dialogs are not established by these checks.

## Previous accepted recovery candidate — 0.18.0-rc1

Six executable-only release ZIPs and `SHA256SUMS.txt` are prepared in `dist/0.18.0-rc1/`. Each ZIP contains the executable, Markdown-only release instructions, license and third-party notices. No source, recovery keys or exported data is packaged. Nothing has been published.

The actual Mac ARM64 package passed all 18 CLI checks and both installer suites, including forced interrupted-fetch recovery. Mac AMD64 under Rosetta and Linux ARM64/translated AMD64 passed the selected CLI/recovery checks. Bash installation passed through local HTTPS on Mac/Linux ARM64. PowerShell passed on Mac with substituted local-file transport; native Windows and public HTTPS delivery remain unverified. Translation is not native AMD64 hardware acceptance.

**Full real-data recovery passed.** The packaged CLI was killed after 500 saved snapshots, then resumed into a new output in **43m45s**, including fresh OS reads. All 500 snapshots were reused; the original partial folder is unchanged. Independent checks verified **11,751 summaries, 1,380 people, 5,170 profile-history documents, 547,897 graph edges and 23,502 native metadata documents**. The archive remains partial: **43 unavailable people, 375 summaries without attached text**, plus reported source-inventory changes. See [the evidence and source-drift explanation](RECOVERY_DESIGN.md#candidate-verification). Storage prototypes, optional formats and automatic recovery cleanup remain deferred.

The local Bash bootstrap additionally fixes cleanup after a failed download. Missing checksum/archive HTTP 404 cases reproduced the failure, then passed with the fix. The full installer suite and current Mac/Linux ARM64 packaged Bash checks pass. The ZIP executables are unchanged by this installer-only fix. The existing Gist has **not** been updated; its earlier byte-match record no longer applies to the local Bash script. Publish the tested script with the chosen release later.

## First public release scope — user decision, 2026-10-01

The measured **28m32s** offline rebuild runtime is acceptable for this user's database. Keep the first release focused on: summaries, profiles, linked Markdown, privacy and recovery. Further storage rewrites, automatic recovery paths/cleanup and extra formats are deferred. The separate range/all-material storage prototypes are not being merged for this release. Verify the chosen package once against real data; do not reopen optimization work without a demonstrated failure.

The release focuses on **Markdown summaries, persona/profile histories, pipeline organization and their linked graph**, with privacy filtering and explicit coverage reports. Attachment extraction, audio and PDF acceptance are **deferred and do not block this release**. Existing optional code is not evidence of public support. Signals/all-data expansion also stays outside the default summaries release.

The verified **43 unavailable people and 375 summary records without attached body text** are accepted source limitations when plainly reported. Preserve those summary records and available metadata, omit invalid links, retain partial status and exit code 2, and do not promise complete historical recovery. New unexplained omissions still require investigation.

Release gates: interrupted-fetch recovery; one real-data acceptance of the chosen final candidate; actual supported-platform CLI/installer checks; and configured public binary hosting/download/cleanup verification. The prior Actions billing hold is superseded by the October 7 Windows validation request; current startup failures are tracked above. Source repository publication is not required. The later Mac handoff request adds Developer ID signing and notarization; both are complete as recorded above.

**Latest direction:** finish recovery and release verification without expanding scope. Hosting is deferred; public GitHub Releases is the likely distribution route. No GCP configuration or publication is required. The October 7 Windows request authorizes private Actions validation.

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
bash install/install.sh --base-url https://github.com/OWNER/RELEASES-REPO/releases/download \
  --version VERSION --output "$HOME/Documents/Pieces-Export"

# Optional CLI flags follow --. An unattended export needs explicit approval.
bash install/install.sh --base-url https://github.com/OWNER/RELEASES-REPO/releases/download \
  --version VERSION --remove -- --yes --format markdown --people profiles
```

```powershell
& ./install/install.ps1 -BaseUrl 'https://github.com/OWNER/RELEASES-REPO/releases/download' `
  -Version 'VERSION' -Output "$HOME\Documents\Pieces-Export"

& ./install/install.ps1 -BaseUrl 'https://github.com/OWNER/RELEASES-REPO/releases/download' `
  -Version 'VERSION' -Cleanup Remove -ExportArgs @('--yes', '--format', 'markdown')
```

`--keep` / `-Cleanup Keep` skips cleanup. `--install-only` / `-InstallOnly` verifies and retains a CLI without exporting. Export flags cannot override `--output`; choose the destination through the installer option. Only Bash needs `curl`, `unzip`, and `sha256sum` or `shasum`. Neither script changes execution policy, removes quarantine attributes, or disables platform security checks.

The [installer Gist](https://gist.github.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1) is published as unlisted (anyone with its link can read it). Both files were retrieved through the GitHub API and matched to the tested local files by SHA-256. The commands below pin immutable raw revisions. Review the script before running it. The base URL and version remain explicit; replace BUCKET and VERSION with the published release values:

```sh
curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location \
  'https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/6d928715f5faf7bf4e9776be2a8b34c981abacab/install.sh' | bash -s -- \
  --base-url https://github.com/OWNER/RELEASES-REPO/releases/download --version VERSION
```

```powershell
$bootstrap = (Invoke-WebRequest -UseBasicParsing -Uri 'https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/866cfdf5ec3192a7a43d82f4b3c4b34343f523de/install.ps1').Content
& ([scriptblock]::Create($bootstrap)) -BaseUrl 'https://github.com/OWNER/RELEASES-REPO/releases/download' -Version 'VERSION'
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
