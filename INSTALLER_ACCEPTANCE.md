# Installer acceptance

Recorded 2026-09-29–30 using the existing, unmodified `0.8.6-dev` release ZIPs. These checks execute the downloaded application binary, not the small fixture executable used by the installer unit tests. They do not read, launch, or stop the installed Pieces OS. The active live export remains independent.

## Verified behavior

`internal/exporter/installer_acceptance_test.go` supplies a synthetic OS with a summary, its annotation body, a user persona/profile, and referenced tags/source. The actual bootstrap verifies and extracts the actual release package, then invokes `export --scope summaries --format both --yes --launch-os=false --close-desktop=false --metadata off` against that fixture.

Every case checks the finalized manifest, summary scope, output retention, PDF index, credential filtering, and Markdown/PDF link audits after relocating the archive. Complete cases also check summary text and inventory reconciliation. The source fixture records requests so the test can reject event-body reads and unexpected inventories.

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
