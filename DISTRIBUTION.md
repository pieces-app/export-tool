# Binary distribution and installation

Updated 2026-10-07. The application is open source under the MIT License ([LICENSE.txt](LICENSE.txt)). The 0.18.0-rc3 ZIPs include it; the older 0.18.0-rc2 ZIPs still bundle the earlier license text. macOS packages are Developer ID signed and notarized by Apple. This document describes the release procedure, not a claim that all-platform production acceptance has passed.

## Release candidate 0.18.0-rc3 — 2026-10-07

Built with `go run ./cmd/release --version 0.18.0-rc3` from `main` at `2807417`, which includes the Obsidian vault format, the Windows fixes, the MIT license and the final-audit fix for redacted credential URLs in rendered Markdown. Both Mac executables were signed with the Developer ID identity `471B68A3F2560F67EE4C4C215B1ECCE5B55861A0` (hardened runtime, secure timestamp, identifier `app.pieces.export-tool`) from the dedicated `pieces-codesign` keychain and notarized with the `global-cloud-runtime/apple-codesigning-config-pieces-app-json` credentials. Apple accepted arm64 submission `0c752b8f-0913-4fd5-9479-79d9f0187152` and amd64 submission `1dfb6042-b88c-4bf9-bcd3-fe8f19f247c7` as Ready for distribution with no issues. Each ticket's cdhash matches its executable, and extracted copies of both final ZIPs pass `codesign --verify --strict --check-notarization -R=notarized`. The amd64 build was run under Rosetta. Packaged installer acceptance passed with the notarized arm64 ZIP for both scripts. Windows and Linux ZIPs are unsigned, as before.

All six ZIPs, per-file `.sha256` files and `SHA256SUMS.txt` are in the public [0.18.0-rc3 Drive folder](https://drive.google.com/drive/folders/1lABvGdTeCue2AMRHSAS_cl4dQ9OYEiE4) (anyone with the link can view). Anonymous downloads of every ZIP matched its SHA-256. Local artifacts are in ignored `dist/0.18.0-rc3/` and `dist/macos-notarized-0.18.0-rc3/` (with Apple receipts under `receipts/`). A first rc3 build from `eeb77f5` was signed and notarized but superseded before publication by `2807417`; its Drive uploads were moved to the trash.

| File | Drive ID | SHA-256 |
| --- | --- | --- |
| `pieces-export_0.18.0-rc3_darwin_arm64_notarized.zip` | `1RGIoNvro0AuyADwL3zKTKFZi1oDal9l-` | `88dc2067fb9e27321bdb37ec3f463ecf5b6a67d7282131b94fd64f78dafa9a1b` |
| `pieces-export_0.18.0-rc3_darwin_amd64_notarized.zip` | `1vgoUYENQRqob845TYnXJzSi7jdUQJl3f` | `24f27445f0a769970ffc9b3a2e0a674cb83e1e86946fd63de1a004fcd71fa167` |
| `pieces-export_0.18.0-rc3_linux_amd64.zip` | `1alrEphCpjI2l8A_vkQ3y3aKOkwt1a_LI` | `1579cf23a9c1be22967ece3ba5c1f1b6c88da5c0ddcc30c8f5916c0919905286` |
| `pieces-export_0.18.0-rc3_linux_arm64.zip` | `1ReiyL2qQRbDYjQMht1fEX5EQ8baCFTHY` | `a6bc477aac963cd277890ba8b10de5edf6cb56ff5c5890da85cbbf2e489ef1cd` |
| `pieces-export_0.18.0-rc3_windows_amd64.zip` | `1FUpqjfpXRfumjfECHJB3IMi2mD2zZ04Z` | `fd8331e0730a8248df2c6a34abb6b06ff7342500bcfacc3dfd26b99c116ef7e6` |
| `pieces-export_0.18.0-rc3_windows_arm64.zip` | `1b7I4WOu1yl6JFROX6KNMwn5Aie0cMS33` | `538a4c1c475bfe46c6b2eb53de979c7d920982d1848267a1db6cdf690bd0cebf` |

## Current Windows validation — 2026-10-07

The user confirmed credits should now be available and made Windows readiness the current priority. The older Actions hold below is historical and superseded. [Windows quickstart](WINDOWS_START_HERE.md) contains the consumer PowerShell commands.

GitHub currently rejects both acceptance and a minimal Windows-only diagnostic with `startup_failure` before any job exists, despite enabled repository/organization Actions permissions and clean local workflow syntax checks. The precise cause needs run-page annotations; no Windows runtime pass is claimed. The Windows downloads remain unsigned. 

## Signed and notarized Mac email package — 2026-10-03

The current ARM64 and Intel macOS packages in `dist/macos-notarized-0.18.0-rc2/` are Developer ID signed and notarized by Apple. Apple accepted both submissions with no issues after the user resolved the agreement. The final ZIPs refresh documentation and retain the exact tested/notarized executable bytes. Each code hash matches its Apple ticket; online notarization requirements and execution from locally quarantined copies pass on ARM64 and Intel/Rosetta. No runtime code changed. Keep the Mac online for first launch; raw CLI/ZIP files do not support stapling. Only intended packages were uploaded to Apple; no public upload or email occurred.

## Presentation candidate — 0.18.0-rc2

Six replacement ZIPs and `SHA256SUMS.txt` are built in `dist/0.18.0-rc2/`. The new package uses readable local dates and links exact attached profile versions from summaries. Full source tests, CLI race tests and actual Mac ARM64 summary/rebuild/recovery checks pass. The actual Mac package rebuilt the accepted archive in America/New_York in 28m36s. Independent body/profile/graph checks, all 11,751 summary timestamps and all 23,502 native metadata documents pass; canonical records and source fingerprints reconcile. [Open the refreshed archive](exports/readable-local-20261002/export/index.md). Other-platform runtime acceptance below applies to rc1; rebuilding their binaries does not repeat that acceptance. Nothing has been uploaded or published.

### macOS trial handoff

For macOS builds, use the notarized packages and matching checksums. The older `darwin_arm64.zip` and `darwin_amd64.zip` described in this section remain unsigned. These are local artifacts, not published download URLs. The recipient-machine download experience remains part of the first external trial; local quarantined-copy checks pass.

On 2026-10-02, the six unpublished ZIPs received the expanded consumer guide. The executable, license, notices and executable permissions were preserved; all executable hashes still match the prior candidate. ZIP checksums changed with the README. The previous packages/checksums are retained privately under `exports/weekend-handoff-20261002/original-packages/`. Use the current files in `dist/0.18.0-rc2/` together, not a checksum file from before this refresh. Published version artifacts must remain immutable.

Actual rc2 headless checks passed with closed stdin and separately captured stdout/stderr on Apple silicon and with the Intel executable under Rosetta. They verify periodic progress, Local/UTC settings, finalized Markdown bodies/links, complete/partial exit codes and offline recovery inspection. The Intel run also repeats recovery checks. The refreshed Mac ZIP passed the local HTTPS Bash installer test. No runtime code changed and no second large export was required. Native Windows, native Intel hardware, public-host downloads and recipient-specific security dialogs are not established by these checks.

## Previous accepted recovery candidate — 0.18.0-rc1

Six executable-only release ZIPs and `SHA256SUMS.txt` are prepared in `dist/0.18.0-rc1/`. Each ZIP contains the executable, Markdown-only release instructions, license and third-party notices. No source, recovery keys or exported data is packaged. Nothing has been published.

The actual Mac ARM64 package passed all 18 CLI checks and both installer suites, including forced interrupted-fetch recovery. Mac AMD64 under Rosetta and Linux ARM64/translated AMD64 passed the selected CLI/recovery checks. Bash installation passed through local HTTPS on Mac/Linux ARM64. PowerShell passed on Mac with substituted local-file transport; native Windows and public HTTPS delivery remain unverified. Translation is not native AMD64 hardware acceptance.

**Full real-data recovery passed.** The packaged CLI was killed after 500 saved snapshots, then resumed into a new output in **43m45s**, including fresh OS reads. All 500 snapshots were reused; the original partial folder is unchanged. Independent checks verified **11,751 summaries, 1,380 people, 5,170 profile-history documents, 547,897 graph edges and 23,502 native metadata documents**. The archive remains partial: **43 unavailable people, 375 summaries without attached text**, plus reported source-inventory changes. See [the evidence and source-drift explanation](RECOVERY_DESIGN.md#candidate-verification). Storage prototypes, optional formats and automatic recovery cleanup remain deferred.

The local Bash bootstrap additionally fixes cleanup after a failed download. Missing checksum/archive HTTP 404 cases reproduced the failure, then passed with the fix. The full installer suite and current Mac/Linux ARM64 packaged Bash checks pass. The ZIP executables are unchanged by this installer-only fix. The Gist was updated with this fix and the new installer behavior on 2026-10-07; see [Installation UX](#installation-ux).

## First public release scope — user decision, 2026-10-01

The measured **28m32s** offline rebuild runtime is acceptable for this user's database. Keep the first release focused on: summaries, profiles, linked Markdown, privacy and recovery. Further storage rewrites, automatic recovery paths/cleanup and extra formats are deferred. The separate range/all-material storage prototypes are not being merged for this release. Verify the chosen package once against real data; do not reopen optimization work without a demonstrated failure.

The release focuses on **Markdown summaries, persona/profile histories, pipeline organization and their linked graph**, with privacy filtering and explicit coverage reports. Attachment extraction, audio and PDF acceptance are **deferred and do not block this release**. Existing optional code is not evidence of public support. Signals/all-data expansion also stays outside the default summaries release.

The verified **43 unavailable people and 375 summary records without attached body text** are accepted source limitations when plainly reported. Preserve those summary records and available metadata, omit invalid links, retain partial status and exit code 2, and do not promise complete historical recovery. New unexplained omissions still require investigation.

Release gates: interrupted-fetch recovery; one real-data acceptance of the chosen final candidate; actual supported-platform CLI/installer checks; and configured public binary hosting/download/cleanup verification. The prior Actions billing hold is superseded by the October 7 Windows validation request; current startup failures are tracked above. Source repository publication is not required. The later Mac handoff request adds Developer ID signing and notarization; both are complete as recorded above.

**Latest direction:** finish recovery and release verification without expanding scope. Hosting is deferred; public GitHub Releases is the likely distribution route. No GCP configuration or publication is required. The October 7 Windows request authorizes private Actions validation.

## Installation UX

Updated 2026-10-07. The [installer Gist](https://gist.github.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1) (unlisted) holds `README.md`, `install.sh` and `install.ps1`, published from `install/`. Revision `12613a13cbb370e94020fc6eb13b28e201050e3d` introduced this installer, and `1fe8f0c0d326e7962ee81d88705324571fac1591` updated its license wording to MIT. The previous development revision is `fc60b9d6a5712ab655407ff1798093afa7260d7e`.

1. With no options, both scripts install the built-in `0.18.0-rc3` release from its public Drive folder. Each platform's Drive file ID and SHA-256 are pinned in the script, so the expected checksum no longer comes from the download origin. `--base-url`/`-BaseUrl` still selects an HTTPS `<base>/<version>/` release with `SHA256SUMS.txt`, for tests and later GitHub Releases hosting.
2. Detect OS and CPU. Apple silicon under Rosetta and ARM64 Windows running emulated PowerShell get native builds. Verify the SHA-256 and the exact four-member ZIP before anything runs, copy only those entries into a staging folder beside the install location, then move it into place.
3. Keep the tool by default in a per-user folder: macOS `~/Library/Application Support/Pieces Export/tool/<version>/`, Windows `%LOCALAPPDATA%\Pieces Export\tool\<version>\`, Linux `${XDG_DATA_HOME:-~/.local/share}/pieces-export/tool/<version>/`. `PIECES_EXPORT_HOME` overrides the root. `--remove` / `-Cleanup Remove` deletes the tool after the run, and `--ask` / `-Cleanup Ask` prompts `[y/N]`.
4. Export to `Documents/Pieces-Exports/<YYYY-MM-DD_HH-MM-SS>`. Windows uses the shell Documents folder, so OneDrive redirection works. Linux honors `xdg-user-dir DOCUMENTS`.
5. Pass `--format markdown` unless the user supplies a format, so the format prompt no longer appears. For Markdown exports without SDK caches or user-supplied recovery flags, add `--work`/`--recovery-keys` in a private session under `<root>/recovery/`, outside Documents so it is not synced. A finalized export deletes the session. An unfinished export keeps it and prints the `--resume` / `-Resume` command, which resumes the newest saved session into a new export folder. Dry runs (`--dry-run` / `-DryRun`, or the CLI flag) get no recovery or output flags, because the CLI rejects recovery folders there.
6. Run the CLI on the user's terminal. Bash reads prompts from `/dev/tty` when the script itself arrives on stdin. PowerShell starts the CLI with `Process.Start` on the inherited console; the earlier `| Out-Host` hid `Export now? [Y/n]` until Enter was pressed, which an `expect` terminal test reproduced.
7. After a finalized export, open the folder in Finder, Explorer or `xdg-open` when stdout is a terminal. `--no-open` / `-NoOpen` disables this. Preserve the CLI exit status: 0 complete, 2 partial, 1 failure, 130 interrupted.
8. Under `irm | iex`, the PowerShell script sets `$LASTEXITCODE` instead of calling `exit`, which previously closed the user's window. Run as a script file, it still exits with the status.

## Bootstrap commands

```sh
curl -fsSL https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.sh | bash
curl -fsSL https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.sh | bash -s -- --resume
```

```powershell
irm https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.ps1 | iex
& ([scriptblock]::Create((irm https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.ps1))) -Resume
```

The revision-less raw URL serves the newest Gist revision, so customer commands stay the same across releases; pin a revision URL for audits. The Gist README lists every option, the manual ZIP links and the per-OS manual commands. Repository runs use the same flags, for example `bash install/install.sh --base-url https://HOST/releases --version VERSION -- --yes`. Export flags cannot override `--output`; use the installer option.

Verification on 2026-10-07: `go test ./install` passed with Homebrew Bash 5.3, macOS `/bin/bash` 3.2 and PowerShell 7.5. It covers resume, dry runs, cleanup choices, integrity failures, default locations, PowerShell argument quoting, `irm | iex` session survival and a real-terminal prompt check. Packaged acceptance with the actual `0.18.0-rc2` CLI passed for both installers, including a new default-Markdown case where the CLI accepted the installer's recovery folders. All six pinned Drive files were downloaded through curl and PowerShell's HTTP client and matched their hashes. The published one-liners ran from the Gist: a live read-only dry run, `irm | iex`, and the options form. Windows PowerShell 5.1 and native Windows were not available for these checks.

Checksums pinned in the scripts are independent of Drive, but they are only as trustworthy as the Gist account that publishes them. Release objects should stay immutable.

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
