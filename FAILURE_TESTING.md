# Failed exports and finalization tests

A completed export appears at the requested output path only after writing, link/privacy validation, and finalization succeed. Work before that point stays in the sibling `.partial` directory. A partial directory is never a completed archive, even when a provisional `manifest.json` has already been written. `rebuild` rejects it. Resume remains unimplemented; a retry needs a new output path.

The CLI exits 1 on a failed export and does not print `Export written`. Exit 2 is different: it identifies a finalized archive with explicitly recorded coverage/privacy limitations. An interrupted or disk-full `.partial` folder is not an exit-2 archive.

## Verified failure behavior

Real `ENOSPC` tests run inside an isolated Linux ARM64 container with a separate 8 MiB tmpfs. The test refuses a different mount name, a non-tmpfs filesystem, a filesystem larger than 32 MiB, or a nonempty mount. It never fills the host filesystem.

| Injection point | Observed behavior |
| --- | --- |
| First record write | Hydration stops after its first batch; no final directory or success manifest. |
| PDF byte write | Real short-write/exhaustion error; no final directory or success manifest. The incomplete PDF is removed by the ordinary writer. |
| Metadata sidecar output | Stops with `ENOSPC`; previously written documents remain under `.partial`. |
| Reconstruction-state output | Stops with `ENOSPC`; the returned manifest reports failure rather than provisional success. |
| Packaged CLI during parallel Markdown persistence (four workers) | Exits 1 with incomplete summary documents in staging; never starts PDF generation, announces success or finalizes. |
| Packaged CLI during PDF conversion | Actual executable exits 1 with an actionable error, no success announcement, and no finalized archive. |
| Cancellation entering PDF/metadata stages | Returns `context.Canceled`, failed status, and no success manifest. |
| Destination occupied after preflight | Source archive and existing file/empty directory remain unchanged; `.partial` cannot be opened as a finalized rebuild source. |

The packaged disk-full test pauses only its own synthetic child process inside the container, confirms that child's stopped state, fills the dedicated tmpfs, and resumes it. It does not signal an installed Pieces OS or a separately running exporter.

The first real exhaustion run uncovered a late status bug: archive-state writing could fail after the in-memory status was set to complete. The shared export/rebuild finalization path now resets the returned status to `failed` and clears the completion timestamp on error. Filesystem errors retain their underlying cause for `errors.Is`/`errors.As`, but the terminal message omits generated private paths.

## Destination protection

Finalization uses an exclusive rename instead of relying on a separate existence check: `RENAME_EXCL` on macOS, `RENAME_NOREPLACE` on Linux, and Windows `MoveFileExW` without replacement. An unsupported operation fails closed with the staged output retained; there is no overwrite fallback. Go's prior rename wrapper checked for an existing directory, but that check was separate from the syscall. The new operation closes that final check/rename interval.

The Windows implementation supplies extended-length paths for local and UNC destinations. Native Windows runtime and long-path tests remain pending; compilation alone does not prove those paths work on a user's filesystem. Microsoft documents the extended prefix and replacement behavior in [MoveFileExW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw).

## Reproduce locally

Portable cancellation and late-collision tests:

```sh
go test -race ./internal/exporter \
  -run 'TestDestinationFailureStopsHydrationWithoutFinalizing|TestLateFailureAndCancellationNeverReturnSuccess|TestRebuildFinalRenameFailurePreservesSourceAndExistingDestination' \
  -count=1
```

The real disk-full suite is opt-in and Linux-only. On the current macOS host, compile the test executable and use the already available local Docker VM. The release directory must contain the actual packaged Linux ARM64 binary:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c \
  -o exports/native-acceptance-linux-arm64/exporter.test ./internal/exporter

docker --context desktop-linux run --rm --platform linux/arm64 \
  --network none --read-only --cpus=2 --memory=512m \
  --tmpfs /tmp:rw,size=128m --tmpfs /enospc:rw,size=8m \
  --mount type=bind,source="$PWD/exports/native-acceptance-linux-arm64",target=/tests,readonly \
  --mount type=bind,source="$PWD/dist/0.13.0-dev/pieces-export_0.13.0-dev_linux_arm64",target=/app,readonly \
  --env PIECES_EXPORT_ENOSPC_ROOT=/enospc \
  --env PIECES_EXPORT_TEST_BINARY=/app/pieces-export \
  debian@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251 \
  /tests/exporter.test \
  -test.run '^Test(ActualENOSPCLeavesUnfinalizedArchive|PackagedDiskFullCLI)$' -test.v
```

Only synthetic test executables and the release package are mounted. No source, private cache, or live export directories are exposed to the container. Its filesystem is removed on exit. GitHub Actions is not required and remains on the billing hold.

Still required: native Windows runtime/long-path verification, wider filesystem coverage, crash/power-loss durability, strict cancellation during synchronous font/parser library calls, automatic splitting of oversized PDFs, and resume/recovery design. These tests do not certify those cases.

## PDF limits and cancellation (2026-09-30)

The PDF suite now covers exact input-file read bounds, oversized Markdown and metadata, page-limit failures, accumulated link payloads, actual writer-output limits, malformed/oversized fonts, invalid UTF-8, and glyphs wider than the page. A sticky writer error prevents the pinned PDF compiler's ignored write errors from producing a seemingly successful result. An export-level page-limit test verifies failed status, no final directory, retained untruncated Markdown, and rejection of the partial folder as rebuild input.

A 13 MB paragraph is canceled after its third wrapped line. A separate actual renderer test cancels at the page-25 progress event inside one large paragraph; neither test relies on cancellation between documents. Wrapping is compared with the previous library output at three font sizes for normal text, long words, whitespace/tabs, empty lines, and UTF-8 chunk boundaries. Font parsing itself remains synchronous; cancellation before/after that call is not mid-parse interruption evidence.

```sh
go test -race ./internal/exporter ./internal/cli -run '^TestPDF' -count=1
```

Defaults/ranges and retry behavior are in [the PDF contract](EXPORT_SPEC.md#pdf-resource-limits-and-cancellation). Input/page/output caps are not a strict RSS/CPU ceiling. Library-internal cancellation, adversarial fonts, oversized-index splitting, and native Windows execution still require further work.
