# Installer acceptance

## One-line installer with Drive downloads and resume — 2026-10-07

Both scripts now default to the pinned `0.18.0-rc2` Drive files, keep the tool, pass `--format markdown`, add private recovery folders for Markdown runs, support `--resume`/`-Resume` and `--dry-run`/`-DryRun`, and open the finished folder. Behavior and paths are in [DISTRIBUTION.md](DISTRIBUTION.md#installation-ux).

Two Windows defects were reproduced before the fix. Under `irm | iex`, the script's final `exit` ended the user's PowerShell session. Piping the CLI through `Out-Host` hid `Export now? [Y/n]` until Enter was pressed, shown by an `expect`-driven terminal test that timed out waiting for the prompt. Reintroducing either defect makes its test fail again.

`go test ./install` passed with Homebrew Bash 5.3, macOS `/bin/bash` 3.2 and PowerShell 7.5. Installer runs start in a new session, so `/dev/tty` prompts cannot block on a developer's terminal. Packaged acceptance with the actual `0.18.0-rc2` CLI passed on macOS ARM64 for both scripts, including a default-Markdown case using the installer's own recovery folders. All six pinned Drive files matched their hashes through curl and PowerShell's HTTP client. The published Gist one-liners ran a live read-only dry run, `irm | iex`, and the scriptblock options form. Native Windows and Windows PowerShell 5.1 remain unverified.

## Frozen release candidate — 0.18.0-rc1

Six executable-only release ZIPs and `SHA256SUMS.txt` are prepared in `dist/0.18.0-rc1/`. Each ZIP contains the executable, Markdown-only release instructions, license and third-party notices. No source, recovery keys or exported data is packaged. Nothing has been published.

The actual Mac ARM64 package passed all 18 CLI checks and both installer suites, including forced interrupted-fetch recovery. Mac AMD64 under Rosetta and Linux ARM64/translated AMD64 passed the selected CLI/recovery checks. Bash installation passed through local HTTPS on Mac/Linux ARM64. PowerShell passed on Mac with substituted local-file transport; native Windows and public HTTPS delivery remain unverified. Translation is not native AMD64 hardware acceptance.

**Full real-data recovery passed.** The packaged CLI was killed after 500 saved snapshots, then resumed into a new output in **43m45s**, including fresh OS reads. All 500 snapshots were reused; the original partial folder is unchanged. Independent checks verified **11,751 summaries, 1,380 people, 5,170 profile-history documents, 547,897 graph edges and 23,502 native metadata documents**. The archive remains partial: **43 unavailable people, 375 summaries without attached text**, plus reported source-inventory changes. See [the evidence and source-drift explanation](RECOVERY_DESIGN.md#candidate-verification). Storage prototypes, optional formats and automatic recovery cleanup remain deferred.

## Download-failure cleanup — 2026-10-01

A missing checksum or archive returned HTTP 404 but the Bash EXIT handler lost its function-local state, leaving cleanup incomplete and reporting an unbound variable. Running the installer function in a subshell keeps that state available to EXIT. Regression cases reproduce both failures, then verify curl's exit status 22, no executable invocation and no retained installation directory. All installer source tests pass (7.091s), and actual `0.18.0-rc1` Bash ZIP installation/cleanup checks pass again on macOS ARM64 and Linux ARM64. No CLI executable or exported archive changed. The local script fix has not been republished to the Gist.

Recovery installer scenarios now request supported Markdown and verify `index.md`; existing non-recovery PDF fixture scenarios remain unchanged. These test cases do not make PDF a supported first-release feature.

## Earlier local package checks — 2026-10-01

The **`0.17.5-dev`** scan-retry candidate passed all selected actual Mac ARM64 packaged CLI/installer cases in **105.458 s** with race checks. The actual Mac AMD64 executable under Rosetta passed CLI/default-summary/recovery checks and the retry fixtures in **12.265 s**. Six ZIP hashes, exact four-file layouts and embedded files were verified. The Linux ARM64 package passed ten top-level checks, covering retry/cancellation, missing-person recovery, ordinary/default-summary CLI export, recovery and Bash HTTPS installation/cleanup. Its isolated Docker container had no network or private export/source mounts. The later production change is limited to complete scanner retries and aggregate retry accounting; the separate `0.17.4-dev` disk-full/native-xattr checks below remain revision-specific evidence.

The actual **Linux AMD64 `0.17.5-dev` executable** also passed **25 top-level checks** through the local ARM64 Docker VM's translation support, with no skipped cases. These covered ordinary/default/current-junction exports, grouped evidence, privacy, offline rebuild/recovery, Markdown/PDF links, signals, scanner retries and actual Markdown/PDF disk-full failures. The sum of reported test durations was **36.17 s**, not full-history throughput. The container had one CPU, 512 MiB RAM, a read-only root, no network/private-data mounts and an 8 MiB ENOSPC tmpfs. This is AMD64 Linux executable/ABI evidence under translation, not native Intel hardware. The AMD64 Bash installer was not exercised because the surrounding ARM64 container tools report ARM64; do not treat an architecture-spoofed installer run as native acceptance.

The actual **`0.17.4-dev`** ZIP and CLI passed all selected Mac ARM64 packaged cases in **171.645 s** with race checks. This includes Bash local-HTTPS and PowerShell 7 substituted-file-transport complete/remove, complete/keep, partial/remove and recovery/remove cases. The private workspace and keys remain outside utility cleanup. Rosetta CLI/layout checks also passed. The package adds the missing-person link correction, bounded parallel auditing and a default single relationship sibling.

The installed Docker runtime was started for current Linux ARM64 acceptance. **39 top-level checks passed** against the actual package and compiled fixtures, including the four Bash local-HTTPS installation/cleanup cases, default summaries/profile/sidecar behavior, current junctions, recovery, graph/body/link preservation, signal privacy, grouped transactions and audit-worker failure/cancellation. The sum of reported test durations was **24.57 s**; this is a fixture measurement on tmpfs, not real-export throughput. The container used a read-only root, no network, two CPUs, 512 MiB RAM and a private executable tmpfs. It mounted only compiled checks, binary-only release files and public bootstrap scripts; no source OS or private export data was mounted.

Dedicated Linux tests exercised real ENOSPC in an 8 MiB tmpfs during hydration, PDF output, metadata-sidecar output and reconstruction-state writing. Actual compiled CLI Markdown/PDF disk-full cases exited 1 with no finalized archive or success announcement. These cases were skipped in the first container without an ENOSPC root and explicitly passed in the dedicated container. Independent readback also matched `user.xdg.tags` and `user.xdg.comment` to all eight Markdown/PDF metadata sidecars in the synthetic export. This checks stored attributes, not Linux desktop indexing.

This is local fixture acceptance on Mac ARM64/Rosetta, an ARM64 Linux VM and translated Linux AMD64 executables. Native Windows, native AMD64 hardware/installer execution, public GCP downloads, PowerShell end-to-end HTTPS, desktop viewers and installed-OS lifecycle/migration remain separate gates. Earlier `0.17.1-dev` Mac checks passed in 108.829 s; they are superseded by the current package checks above.

The published two-file Gist was fetched again and matched the tested local scripts by SHA-256. The prepared native workflow now selects all `TestPackaged.*CLI` tests and passed actionlint. No GitHub Actions job or binary upload occurred.

Recorded 2026-09-29–30 using the existing, unmodified `0.8.6-dev` release ZIPs. These checks execute the downloaded application binary, not the small fixture executable used by the installer unit tests. They do not read, launch, or stop the installed Pieces OS. The active live export remains independent.

## Verified behavior

`internal/exporter/installer_acceptance_test.go` supplies a synthetic OS with a summary, its annotation body, a user persona/profile, and referenced tags/source. The actual bootstrap verifies and extracts the actual release package, then invokes `export --format both --yes --launch-os=false --close-desktop=false --metadata off` against that fixture. Starting with the `0.14.0-dev` acceptance run, the test omits scope and people flags and asserts summaries/profile defaults; earlier runs explicitly selected summaries.

Every case checks the finalized manifest, summary scope, output retention, credential filtering, and link audits after relocating the archive. Recovery cases use Markdown and check its index; the older non-recovery cases also check PDF output. Complete cases also check summary text and inventory reconciliation. The source fixture records requests so the test can reject event-body reads and unexpected inventories.

| Platform and shell | Package delivery | Cases verified |
| --- | --- | --- |
| macOS ARM64, system Bash | Real local HTTPS, with a temporary fixture CA passed only to the child curl process | Complete export/remove, complete export/keep, partial export/remove; exit 0 or 2 preserved; race detector passed |
| macOS ARM64, PowerShell 7 | Download function replaced with a local file copy; hashing, extraction, executable, and cleanup are real | Same three cases; race detector passed; this is not end-to-end HTTPS or Windows evidence |
| Linux ARM64, Bash, isolated Debian VM/container | Real local HTTPS inside the container, with fixture CA | Same three cases; no Go or application installation required inside the container |

The partial case supplies a summary whose selected body annotation cannot be fetched. The CLI finalizes an honestly partial archive, returns 2, and the installer removes its own temporary installation while retaining that archive.

Actual terminal prompts were also exercised on macOS: Bash `y` removes the utility and `n` keeps a runnable CLI; PowerShell Enter accepts the default removal and `n` keeps the runnable CLI. The fixture verifies retention before the test framework cleans up its temporary test files. This is separate from normal installer operation, where the user's output remains after the script exits.

The Linux runtime had its network disabled, a read-only root filesystem, two CPUs and 512 MiB RAM. Only the compiled synthetic test, public bootstrap, native release ZIP, and checksum file were mounted. A private executable tmpfs held the installation and output; no private export or OS data was mounted.

## Repeat the tests

From the repository root on a native build target:

```sh
PIECES_EXPORT_TEST_RELEASE="$PWD/dist/0.8.6-dev" \
PIECES_EXPORT_TEST_VERSION=0.8.6-dev \
go test -race ./internal/exporter \
  -run '^TestPackaged(Bash|PowerShell)Installer$' -count=1 -v
```

Use a directory containing the exact published-style ZIP name and its original `SHA256SUMS.txt`; the test does not rebuild, repackage, or relabel the application. Missing PowerShell is skipped. Set `PIECES_EXPORT_TEST_POWERSHELL=powershell.exe` to test Windows PowerShell 5.1; use `pwsh` for PowerShell 7. On Windows ARM64, run without `-race` until the Go race runtime supports that target.

For the genuine final prompt, compile the test executable and run it directly in a terminal from the package directory:

```sh
go test -c -o exports/installer-acceptance.test ./internal/exporter
cd internal/exporter
PIECES_EXPORT_TEST_RELEASE="$PWD/../../dist/0.8.6-dev" \
PIECES_EXPORT_TEST_VERSION=0.8.6-dev \
PIECES_EXPORT_TEST_INSTALLER_PROMPT=remove \
../../exports/installer-acceptance.test \
  -test.run '^TestPackagedBashInstaller$' -test.v
```

Answer the displayed cleanup question with `y` or Enter. Repeat with `PIECES_EXPORT_TEST_INSTALLER_PROMPT=keep` and answer `n`. Replace the test name with `TestPackagedPowerShellInstaller` for PowerShell. The environment variable selects the expected outcome; it does not answer the prompt. The CLI's initial export approval is suppressed only for this synthetic test by `--yes`.

The prepared native Actions workflow includes the Bash actual-package cases on macOS/Linux and actual-package PowerShell 5.1/7 cases on Windows. It remains paused for the user's billing hold; adding a test to YAML is not execution evidence.

## Temporary-directory execution

The temporary directory must permit executable files. A Linux trial with Docker's default `noexec` tmpfs correctly failed to execute the downloaded binary, returned shell status 126, and produced no export. The installer did not report successful completion. The successful isolated test explicitly provisioned its own `/tmp` mount with `rw,exec`; it did not change host mount settings.

On a machine whose temporary directories use `noexec`, choose an allowed, private temporary directory through `TMPDIR` before invoking Bash. The installer does not remount filesystems or bypass operating-system execution restrictions. An executable-mount prerequisite and a checksum match do not override platform security policy.

## Open release gates

- Full download/install/export/cleanup from the chosen GCP object URLs, including version immutability and correct content/cache metadata.
- Windows native runtime, both PowerShell versions, and native AMD64 Linux/Intel Mac evidence; Linux ARM64 VM evidence is not those targets.
- Clean-machine unsigned download UX and platform trust/quarantine behavior.
- PowerShell end-to-end HTTPS delivery with the actual package. Its HTTPS helper was tested separately, but local transport substitution above does not close this gate.
- Installed Pieces OS lifecycle, real full-history migration, and native viewers/metadata remain independent of these synthetic installer checks.

The bootstrap scripts did not change during this acceptance work. The previously verified Gist revisions and script hashes remain applicable; no application binaries or private records were uploaded.

The unattended complete/remove, complete/keep, and partial/remove cases were repeated successfully for `0.8.7-dev`: Bash HTTPS on macOS ARM64 and isolated Linux ARM64, plus PowerShell 7 with substituted file transport on macOS. The earlier interactive prompt evidence used `0.8.6-dev`; the scripts themselves are unchanged.

The same unattended cases also passed with the `0.8.8-dev` packages on those platforms and transports, after the unchanged-record optimization. The CLI still scans retained records, returns partial status when selected content is unavailable, and leaves the finalized export after installer cleanup.

The unattended actual-package cases also passed for `0.8.9-dev` on macOS ARM64 (Bash local HTTPS and PowerShell substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). Complete/remove, complete/keep, and partial/remove retained the exported files as intended. Installer scripts and the published Gist are unchanged; this does not verify public hosting or native Windows execution.

The unattended actual-package cases passed again for `0.8.10-dev` on macOS ARM64 (Bash local HTTPS and PowerShell substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). Installer cleanup continued to preserve complete and partial-status exports. The scripts/Gist are unchanged; native Windows and public-host download verification remain open.

The `0.9.0-dev` actual-package cases also passed on macOS ARM64 and isolated Linux ARM64, covering successful removal, requested retention, and cleanup after a partial-status export. Bash used local HTTPS; PowerShell 7 on macOS used substituted file transport. Native Windows and public GCP-hosted downloads remain unverified. The published Gist/scripts are unchanged.

The same actual-package cleanup cases passed with `0.9.1-dev` on macOS ARM64 and isolated Linux ARM64. The PDF link correction changes the downloaded executable, not the Bash/PowerShell bootstrap or published Gist. PowerShell transport substitution and native Windows/public-host limitations remain.

The actual-package cleanup cases passed again for `0.10.0-dev` on macOS ARM64 (Bash local HTTPS and PowerShell 7 with substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). This release adds wrapped SDK-cache recovery to the executable. Bootstrap scripts and the Gist remain unchanged; native Windows, public hosting and clean-machine download behavior remain open.

The same actual-package cleanup cases passed for `0.10.1-dev` on macOS ARM64 and isolated Linux ARM64. Bash used local HTTPS; PowerShell 7 on macOS used substituted file transport. Complete/remove, complete/keep, and partial/remove preserved the exported files. The executable adds canonical annotation recovery from SDK provider views and current inverse-field precedence. Bootstrap scripts and the Gist are unchanged; native Windows, public hosting, clean-machine behavior, and PowerShell end-to-end HTTPS remain unverified.

The actual-package cleanup cases passed for `0.11.0-dev` on macOS ARM64 (Bash local HTTPS and PowerShell 7 with substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). The executable now exports observed-pair association metadata. Complete/remove, complete/keep, and partial/remove preserve output archives. Bootstrap scripts and the Gist remain unchanged; this does not establish native Windows or public GCP-hosted download acceptance.

The actual-package cleanup cases passed for `0.12.1-dev` on macOS ARM64 (Bash local HTTPS and PowerShell 7 with substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). The executable adds bounded event/person association pagination with older-schema fallback. Complete/remove, complete/keep, and partial/remove retain output archives. The broader packaged-CLI suites also passed on macOS ARM64, Rosetta AMD64, and Linux ARM64, including offline reconstruction and moved Markdown/PDF links for newly recovered association evidence. Windows binaries and test executables compile; native Windows, PowerShell end-to-end HTTPS, public hosting, and real full-history acceptance remain open. Bootstrap scripts, the published Gist, and the Actions billing hold are unchanged.

The same actual-package cleanup cases passed for `0.12.2-dev` on macOS ARM64 (Bash local HTTPS and PowerShell 7 with substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). This build reduces supporting-record reads and retained JSON during summary graph construction; it does not change the bootstrap scripts. Complete/remove, complete/keep, and partial/remove retain output archives. Broader packaged export/rebuild/Markdown/PDF/link acceptance passed on macOS ARM64, Rosetta AMD64, and Linux ARM64. Windows compilation, Gist availability, and these local fixtures do not close native Windows, public-hosting, clean-machine, or real migration gates.

## Local diagnostics build (`0.12.3-dev`)

Actual-package complete/remove, complete/keep, and partial/remove cases passed again on macOS ARM64 (Bash local HTTPS and PowerShell 7 with substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). The bootstrap scripts are unchanged. Packaged CLI export/rebuild tests now require persisted local performance measurements, current HTTP counters for export, and zero current HTTP requests for offline rebuild with separate source diagnostics. Broader packaged export/cache/summaries/rebuild/legacy/PDF-limit/signal-privacy/digest/wrapped-cache/association/paged-association acceptance passed on macOS ARM64 (94.243 seconds including installers), Rosetta AMD64 (68.860 seconds), and Linux ARM64. Native Windows, remote HTTPS hosting, clean-machine download and full live migration remain unverified.

## Bounded Markdown writers (`0.13.0-dev`)

The actual release ZIPs passed the existing Bash local-HTTPS complete/remove, complete/keep and partial/remove cases on macOS ARM64 and isolated Linux ARM64; PowerShell 7 cases passed on macOS with substituted file transport. Source scripts and the published Gist are unchanged. Broader actual-package export/rebuild/privacy/PDF/link tests passed on macOS ARM64 (84.894 seconds including installers), Rosetta AMD64 (66.307 seconds), and Linux ARM64, with the default two writers and an explicit four-writer rebuild. Actual four-writer Markdown disk exhaustion passed in the isolated Linux mount. Windows compilation and these fixtures do not close native Windows, real OS lifecycle, public hosting, clean-machine installation or full live migration gates.

## Final audit buffering (`0.13.1-dev`)

The actual ZIP installer cases passed on macOS ARM64 (Bash local HTTPS; PowerShell 7 substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). Complete/remove, complete/keep and partial/remove retained exports and preserved exit status. Broader packaged export/rebuild/privacy/PDF/link cases passed on macOS ARM64 (66.855 seconds including installers), Rosetta AMD64 (47.485 seconds), and Linux ARM64. The executable now reuses bounded audit buffers and rejects unfinished JSON containers; scripts and the published Gist are unchanged. These fixtures do not close native Windows, real migration or public-hosting acceptance.

## Summaries/profile default (`0.14.0-dev`)

The actual ZIP default-command tests passed on macOS ARM64 (Bash local HTTPS; PowerShell 7 substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). They omit scope/people flags, retain the fixture profile, and omit a person with explicit empty profile/summary evidence. Complete/remove, complete/keep and partial/remove still preserve export folders and return the CLI status. Default scan, dry run and export also passed direct-binary tests on macOS ARM64, Rosetta AMD64 and Linux ARM64, with no event bodies or whole supporting-collection inventories read. These checks do not establish large-database runtime, native Windows, real OS lifecycle, public hosting or clean-machine trust behavior. Bootstrap scripts and the published Gist did not change.

## Reduced persona reads (`0.14.1-dev`)

The actual ZIP/default-command installer cases passed again on macOS ARM64 (Bash local HTTPS; PowerShell 7 substituted file transport) and isolated Linux ARM64 (Bash local HTTPS). Complete/remove, complete/keep and partial/remove retained export folders and returned the original CLI status. Broader focused actual-package tests passed on macOS ARM64 (race-enabled, 57.590 s including installers), Rosetta AMD64 and Linux ARM64. A new projected-person fixture recovers the profile body through the combined annotation query, makes no event-count request, persists unknown connectivity, and retains partial status for missing person-to-summary projection. Markdown/PDF links pass after moving the archive. Native Windows, public HTTPS hosting and complete live migration remain open; bootstrap scripts and the published Gist are unchanged.

## Recovery retention (`0.15.0-dev`)

The macOS ARM64 actual-ZIP suite passed with the race detector in 41.399 s, covering complete/remove, complete/keep, partial/remove and recovery/remove. Bash used local HTTPS; PowerShell 7 used substituted file transport. In recovery/remove, the downloaded utility is deleted while the completed archive, separate private workspace and keys remain. Opening the retained workspace verifies a completed source capture is still replayable. The scripts were unchanged; the new scenario exercises their existing literal argument forwarding and cleanup boundary. Linux/Docker is currently unavailable, and native Windows plus GCP HTTPS remain pending.
