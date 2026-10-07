# Windows readiness — October 7, 2026

The current Windows candidate includes the summaries/profile exporter, interrupted-fetch recovery, localized dates and the compact Obsidian module. It is **not yet accepted for public Windows release**: native Windows execution has not been verified.

Consumer instructions: [Windows quickstart](WINDOWS_START_HERE.md). The general guide is included as `README.md` inside each ZIP. The repository remains private; distribute only binaries, checksums, notices and consumer instructions.

## Changes and checks in this pass

- Corrected two Windows COM property readback conversions flagged by `go vet`. Read pointers directly from the PROPVARIANT union instead of converting stored integers back into pointers. Native property-handler behavior still needs runtime verification; Markdown/sidecar metadata remains the fallback.
- Fixed release-notice collection for Homebrew's Go layout, where `LICENSE` is next to `libexec`, rather than inside GOROOT. Missing notices still stop packaging.
- Added a Windows-only private CI workflow for native x64 (`windows-2025`) and ARM64 (`windows-11-arm`), using synthetic loopback data. It builds ZIPs, checks architecture, runs all source tests/vet, executes all compiled-CLI cases, runs installer tests in PowerShell 5.1 and 7, and runs race tests on x64.
- Syntax-checked consumer PowerShell examples with PowerShell 7 on macOS. This checks syntax only; it does not establish Windows runtime acceptance.
- Cross-compilation and package verification are recorded below once complete. They do not execute Windows binaries on this Mac.

## GitHub startup blocker

The user confirmed credits should now be available and authorized Windows validation. The previous “do not retry Actions” hold is superseded.

The private validation branch is `export-cli/windows-validation-20261007`. GitHub reports `startup_failure` with **zero jobs** for the [acceptance attempt](https://github.com/pieces-app/export-tool/actions/runs/37627607834), an [additional diagnostic attempt](https://github.com/pieces-app/export-tool/actions/runs/37627781050), and a [minimal Windows-only job](https://github.com/pieces-app/export-tool/actions/runs/37627958451). The minimal job has no checkout, Go setup, matrix or exporter code. Repository and organization Actions permissions report enabled/all actions allowed. Local actionlint passes. GitHub's APIs provide no specific reason and reject retrying these startup failures.

This establishes that no exporter test ran on a Windows runner. It does **not** establish the precise account/workflow startup cause. Read the run-page annotations to resolve it; do not describe the current failure as confirmed billing debt or an exporter runtime failure. Detailed API evidence stays under `exports/windows-readiness-20261007/`.

## Remaining acceptance

- [ ] Run the restored Windows acceptance workflow after the GitHub startup issue is resolved; record both job URLs, commits, versions and hashes.
- [ ] Require all source tests/vet and packaged-CLI checks to pass on both architectures, including interrupted/completed-source recovery, private Windows ACLs, long paths, archive relocation, privacy and Obsidian conversion.
- [ ] Require PowerShell 5.1 and 7 installer/package checks on both architectures. Their present tests substitute local-file transport; they do not certify public HTTPS delivery.
- [ ] Require x64 race tests. Do not claim ARM64 race acceptance from cross-compilation or an unsupported race runtime.
- [ ] Trial the exact downloaded ZIP on Windows with real Pieces OS: discovery, production launch, optional Desktop quit, summary/profile export, timezone, coverage and viewer navigation. Resume under the same user. Check that the exporter neither requires admin rights nor changes system protection settings.
- [ ] Verify the actual recipient download experience and unsigned-executable handling on Windows/managed machines. Windows signing is not implemented; the Apple certificate does not sign Windows EXEs.
- [ ] Validate public download/checksum URLs when hosting is selected. No public upload is part of this pass.

Use the newest test-passed ZIP's bytes for handoff; don't relabel the old `0.18.0-rc2` binaries as an Obsidian-capable build. A fixture-only green CI run would satisfy the automated native checks, while leaving the real installed-OS and recipient-download trial explicit.
