# Pieces Export

Export your local Pieces summaries and persona histories to Markdown files you can keep after Pieces is unavailable. The files include links between summaries, people, tags, sources and websites. No Go, Python, account with this tool, or separate scanner is required.

This release supports Markdown summaries, persona/profile history and their graph. PDF conversion, attachment/audio extraction and broader all-data exports are outside this release's supported scope. Source gaps are reported rather than filled with invented content.

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

Descriptions and tags appear in documents and portable metadata sidecars. Native attributes are attempted where supported; file-manager display varies by operating system and filesystem.

Read `coverage.md` and `manifest.json` for omissions and unavailable records. Exit codes:

- `0`: completed for the selected, implemented scope.
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

## Other useful options

```sh
./pieces-export doctor
./pieces-export export --dry-run --format markdown --launch-os=false
./pieces-export export --help
```

Use `--environment production` or `--environment staging` when both are installed. `--base-url http://127.0.0.1:39300` selects a particular running instance. `--os-path` can specify an installed OS executable or macOS application bundle.

The application source is private. This utility is unsigned and not notarized. Its use is covered by `LICENSE.txt`; keep `THIRD_PARTY_NOTICES.txt` with it. Only the finalized export folder should be shared—never the private recovery workspace or keys.
