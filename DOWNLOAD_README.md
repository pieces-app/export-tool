# Pieces Export

Export your local Pieces summaries and persona histories to Markdown files you can keep after Pieces is unavailable. The files include links between summaries, people, tags, sources and websites. No Go, Python, account with this tool, or separate scanner is required.

This release supports Markdown summaries, persona/profile history and their graph. PDF conversion, attachment/audio extraction and broader all-data exports are outside this release's supported scope. Source gaps are reported rather than filled with invented content.

## Choose and open the download

Choose the ZIP for the computer where Pieces OS and your data live:

| Computer | ZIP filename ends with |
| --- | --- |
| Mac with Apple silicon (M-series) | `darwin_arm64_notarized.zip` |
| Mac with an Intel processor | `darwin_amd64_notarized.zip` |
| Linux with Intel/AMD 64-bit CPU (`uname -m`: `x86_64`) | `linux_amd64.zip` |
| Linux with ARM64 CPU (`uname -m`: `aarch64` or `arm64`) | `linux_arm64.zip` |
| Windows with Intel/AMD x64 processor | `windows_amd64.zip` |
| Windows with ARM64 processor | `windows_arm64.zip` |

On Mac, **Apple menu → About This Mac** identifies Apple silicon or Intel. On Windows, **Settings → System → About → System type** identifies the processor architecture. `amd64` includes Intel x64 processors. There is no 32-bit build. [Microsoft's architecture guide](https://support.microsoft.com/en-us/windows/experience/compatibility/32-bit-and-64-bit-windows-frequently-asked-questions).

Obtain the ZIP and `SHA256SUMS.txt` from the same trusted release. Before extracting, compute the ZIP's SHA-256 using `shasum -a 256 <zip-file>` on Mac, `sha256sum <zip-file>` on Linux, or `Get-FileHash -Algorithm SHA256 <zip-file>` in PowerShell. Replace `<zip-file>` with your downloaded filename; compare with that filename's entry in `SHA256SUMS.txt`. Only one platform ZIP is needed. Stop if the checksum differs.

Extract the ZIP fully; do not run inside the archive. The four files are the executable, this README, license and third-party notices. Open Terminal/PowerShell and change into the extracted folder (`cd` followed by the folder's quoted path). This is a terminal tool, not a double-click application. Run `./pieces-export version` on Mac/Linux or `.\pieces-export.exe version` on Windows first. No administrator account, `sudo`, developer tools or Python is needed.

On Mac/Linux, if extraction lost the executable permission, use `chmod u+x ./pieces-export`. This does not fix a wrong-architecture download or a macOS security block.

The current Mac ZIPs ending in `_notarized.zip` contain executables signed by **Developer ID Application: Mesh Intelligent Technologies, Inc. (287L9TU9JL)** and **accepted by Apple's notarization service**. Use these packages for Mac. The original Mac ZIPs without a signing/notarization suffix are unsigned; do not mix their checksums with the current packages.

Keep the Mac online for its first launch so Gatekeeper can retrieve Apple's notarization ticket. This ZIP contains a standalone command-line executable; Apple does not support attaching a notarization ticket directly to that file format. If macOS still blocks execution, first check your internet connection, checksum and selected download. Contact the sender with the exact message if it persists. Managed computers may require administrator approval. Do not disable Gatekeeper or endpoint protection globally; a damaged/malware warning requires investigation. [Apple's notarization guidance](https://developer.apple.com/videos/play/wwdc2019/703/), [macOS security guidance](https://support.apple.com/en-us/102445).

For a Mac package, `codesign --verify --strict --verbose=2 ./pieces-export` checks the embedded signature, and `codesign --display --verbose=4 ./pieces-export` shows its authority and team. To require a notarization ticket for this standalone executable, run `codesign --verify --strict --verbose=4 --check-notarization -R=notarized ./pieces-export`; success reports that its explicit requirement is satisfied. `spctl --assess --type execute` is an app assessment and can reject a valid standalone CLI because it is not an app bundle; use the notarization check above. These checks do not override a managed computer's security policy. The matching `.zip.sha256` file can also be checked with `shasum -a 256 -c <zip-file>.sha256`.

This candidate's export/recovery behavior has been exercised on Apple silicon with macOS 15.7.3. The Intel Mac executable is also checked under Rosetta on that machine, which is not native Intel hardware acceptance. Linux has prior runtime checks; native Windows and all OS-version combinations have not been certified. For a first trial, use the Markdown commands below.

## Start an export

Pieces OS must be installed and able to serve your local data. Extract the ZIP into a permanent folder, then open a terminal there.

On macOS or Linux:

```sh
./pieces-export export --format markdown --output ./my-pieces-export --work ./private-work --recovery-keys ./private-keys
```

On Windows, in PowerShell:

```powershell
.\pieces-export.exe export --format markdown --output .\my-pieces-export --work .\private-work --recovery-keys .\private-keys
```

The CLI finds Pieces OS on localhost ports 39300–39333, launches it if needed, scans your inventory, shows the destination and an approximate duration, and asks `Export now? [Y/n]`. After approval it closes Pieces Desktop gracefully; OS remains running. Add `--close-desktop=false` to keep Desktop open or `--launch-os=false` to disable OS launch. `--yes` approves an unattended run; EOF cancels an interactive prompt.

New exports default to summaries and people with profiles. Event history is not required for summary bodies and is skipped. The terminal reports progress through fetching, privacy checks, writing, metadata and validation. A phase reaching 100% does not mean the whole export is finished.

All paths above resolve from the terminal's current directory. Use new directory names for the export and workspace; their parent directories must exist. Output, workspace and keys must be separate, non-nested folders. Existing exports are never overwritten.

Use a writable local disk with space for the export, recovery workspace and a temporary working copy. There is no reliable fixed free-space estimate for every database. Keep the machine awake and the terminal open; on Mac, prefix the command with `caffeinate -i` to prevent idle sleep while it runs. Closing a laptop lid may still suspend it. Avoid updating/reinstalling Pieces OS or switching its account during the export or unfinished-source recovery. Recovery permissions and filesystem locking can fail on network, shared or removable filesystems; use a private local folder if that happens.

For the supported release, include `--format markdown` as shown. Advanced `--help` and the format chooser still expose experimental PDF/all-data features; they are not required for a summary export.

## Unattended runs and agents

`--yes` disables CLI confirmation, and the CLI works with closed stdin and without a terminal. It defaults to Markdown when no format is supplied, but specify `--format markdown` for a reproducible invocation. Omit `--timezone` to use this process's system timezone (`Local`); an environment's `TZ` setting may affect that on Unix. Use an explicit IANA timezone when an agent/container's timezone differs from the user's. Rebuild inherits the archive's timezone unless overridden; `Local` then means the rebuilding computer's timezone.

The example below assumes Pieces OS is already running. `--launch-os=false --close-desktop=false` prevents application launch/quit actions, so it works without a desktop session if the local OS service is reachable. With the defaults, app launch or first-run operating-system security prompts may still require a person. A container or remote agent must have access to the same local Pieces OS; its own `localhost` may be a different machine/network namespace.

macOS/Linux (also safe inside a script with `set -e`):

```sh
export_exit=0
./pieces-export export --yes --format markdown \
  --launch-os=false --close-desktop=false \
  --output ./my-pieces-export --work ./private-work --recovery-keys ./private-keys \
  </dev/null >export.stdout.log 2>export.progress.log || export_exit=$?
printf 'Export exit code: %s\n' "$export_exit"
```

Windows PowerShell:

```powershell
& .\pieces-export.exe export --yes --format markdown `
  --launch-os=false --close-desktop=false `
  --output .\my-pieces-export --work .\private-work --recovery-keys .\private-keys `
  1> export.stdout.log 2> export.progress.log
$exportExit = $LASTEXITCODE
"Export exit code: $exportExit"
```

Progress is plain, newline-delimited text on **stderr**, including stages and approximately two-second updates during export processing. Scan/connection/recovery setup may not have that heartbeat. **stdout** contains preflight information and the final result. From another terminal, use `tail -f export.progress.log` or `Get-Content .\export.progress.log -Wait` to watch. There is no JSON progress stream or stable machine-readable progress schema; use the finalized `manifest.json` and process exit code for automation.

Treat exit **2** as a finalized partial export to review, not an instruction to delete it or automatically start again. Shell pipelines such as `... | tee ...` can hide the export exit code; preserve the export process's status. PowerShell wrappers that throw on nonzero native exits must handle code 2 explicitly. Interactive cancellation or EOF can return 0 **without creating an archive**: agents should pass `--yes` and require the requested output's `manifest.json` and `index.md`, outside a `.partial` directory, before reporting completion. Missing `--yes` can wait indefinitely when stdin remains open.

Allow a long-lived process. A phase's ETA is not the total remaining time; some audits have unknown totals. As a reference, one large database with 11,751 summaries and about 198,000 supporting/selected records took 43m45s for an interrupted fetch/resume and 28m36s for an offline rebuild. Those are measured examples, not guarantees for another machine. Do not kill the process just because fetching finished or a counter paused during a large file. Ctrl-C requests cancellation; retain the workspace and keys to recover. Use `resume --yes` with a new output directory for unattended recovery.

## Resume after an interruption

Keep both `private-work` and `private-keys`. They contain encrypted recovery records and the separate keys needed to read them. Keep them private and outside the export you share.

```sh
./pieces-export resume --work ./private-work --recovery-keys ./private-keys --inspect
./pieces-export resume --work ./private-work --recovery-keys ./private-keys --output ./recovered-export
```

Use `.\pieces-export.exe` on Windows with the same flags. `--inspect` works without OS and tells you whether fetching remains. Resuming unfinished fetching verifies the same OS installation, version and user, checks whether saved records changed, reuses eligible records and repeats relationship reads. Once source collection is complete, recovery runs entirely offline. The original privacy policy and domain lists are retained.

Every recovery attempt writes a new output folder. Previous output and `.partial` folders remain untouched. Losing the keys prevents recovery. Without `--work` and `--recovery-keys` on the original export, an interrupted run must start again. Recovery adds disk work and does not recover deleted or unavailable source records. It currently supports Markdown without optional SDK-cache input.

## Open the result

Start at `my-pieces-export/index.md` in a Markdown viewer. Keep the entire export folder together so relative links continue to work after moving it.

Under `workstream_summaries/`:

- `timeline/` contains chronological summaries.
- `personas/users/` and `personas/related_persons/` contain profiles and their history.
- `single_click_summaries/` groups daily standups and other pipeline styles.

Summary names start with a zero-padded number: `000000.title.date.uuid.md`. Sorting filenames ascending shows the newest first. Related-summary lists live in sibling `.relationships_graph.md` files linked from each summary. Unavailable embedded Pieces destinations become plain labels; generated local links are validated before completion.

Document dates use readable month names and your computer's local timezone by default, including daylight saving time—for example, `April 23, 2025 at 11:42:26 AM EDT (UTC-04:00)`. Use `--timezone America/New_York` (or another IANA name) to choose a fixed zone, or `--timezone UTC`. Dates in filenames and daily indexes use the same zone. JSON retains the exact original timestamps. A summary's “Persona context” section links to the attached profile version instead of repeating its full report; the summary's own narrative stays inline.

Descriptions and tags appear in documents and portable metadata sidecars. Native attributes are attempted where supported; file-manager display varies by operating system and filesystem.

Read `coverage.md` and `manifest.json` for omissions and unavailable records. Exit codes:

- `0`: completed for the selected, implemented scope when an archive was produced; interactive cancellation can also return 0 without output.
- `2`: archive completed with reported source or coverage limitations.
- `1`: export failed; a `.partial` folder may remain.

Some source summaries have no attached body text, and some referenced people are unavailable. Their available records and warnings remain in the export. The tool reads exposed local data; it cannot reconstruct deleted history or provide an atomic database snapshot. Updates during an export may affect its contents.

## Privacy and website filtering

Filtered mode is the default. It checks for known secret patterns, credential fields and basic financial identifiers, applies filtering to exported content, and audits the final files. Detection is best effort; review the archive before sharing it.

To configure rules:

```sh
./pieces-export policy init --output export-policy.json
./pieces-export export --format markdown --policy export-policy.json --output ./filtered-export --work ./filtered-work --recovery-keys ./filtered-keys
```

Website allow/deny rules can be configured in that policy. Adult/banking categories require domain lists; they are not enabled automatically. The optional `lists fetch` command downloads public lists. Export-time filtering stays local. Skipped event history can contain website origins that the summaries export cannot see.

`--mode preserve` deliberately keeps sensitive source content. Keep preservation exports private.

## Troubleshooting a first run

| Symptom | What to do |
| --- | --- |
| Wrong architecture / cannot execute | Check the platform table, extract fully, and run `version`. Approve a trusted Mac binary through the per-app security UI if needed. |
| Pieces OS is not found | Open Pieces OS manually, wait for its database to be ready, then run `doctor --launch-os=false`. Choose `--environment production` or `staging` when both exist. |
| Desktop closure fails | Close Pieces Desktop manually and retry with `--close-desktop=false`. Leave Pieces OS running. |
| Output/workspace already exists | Use new paths for a new export; use `resume` with the existing workspace and keys to recover. Do not repeatedly restart into the same paths. |
| Permission, locking or disk-space error | Keep the partial folder and keys. Use a writable private local disk with sufficient free space; do not run as root to work around it. |
| Exit 2 or missing body/person | Read `coverage.md` and `manifest.json`. Some OS versions omit fields or records; the tool cannot recreate unavailable source data. |
| Markdown links do not open | Keep the whole folder together and use a Markdown viewer that supports relative local links. Finder/Explorer alone may display Markdown as plain text. |
| Banking/adult sites remain | These category lists are opt-in. Secret detection also cannot guarantee every sensitive fact was removed; review before sharing. |

To report a problem, start with the tool's `version`, OS version/CPU, process exit code and last progress stage. Retain both log files locally. Review any logs, manifests or coverage reports before sharing: paths, filenames and diagnostic material can be private. Never send recovery keys or the private workspace as a bug report.

## Other useful options

```sh
./pieces-export doctor
./pieces-export export --dry-run --format markdown --launch-os=false
./pieces-export export --help
```

Use `--environment production` or `--environment staging` when both are installed. `--base-url http://127.0.0.1:39300` selects a particular running instance. `--os-path` can specify an installed OS executable or macOS application bundle.

The application source is private. The current Mac packages are Developer ID signed and notarized as described above. Its use is covered by `LICENSE.txt`; keep `THIRD_PARTY_NOTICES.txt` with it. Only the finalized export folder should be shared—never the private recovery workspace or keys.
