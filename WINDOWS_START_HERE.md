# Pieces Export on Windows

Use the ZIP ending in `windows_amd64.zip` for Intel/AMD x64, or `windows_arm64.zip` for an ARM64 computer. Check **Settings → System → About → System type**. The download includes `pieces-export.exe`; no Go, Python or administrator terminal is needed.

Download the matching `SHA256SUMS.txt`, compare the ZIP's checksum with `Get-FileHash -Algorithm SHA256 "YOUR-DOWNLOAD.zip"`, then **Extract All**. Open PowerShell in the extracted folder; do not double-click the EXE or run it inside the ZIP.

```powershell
.\pieces-export.exe version
```

## Export summaries and profiles

Install/start Pieces OS on the computer containing your data. The exporter can discover it on localhost ports 39300–39333 and launch the production installation if needed. It scans first, shows counts and an estimate, then asks for confirmation. It closes Pieces Desktop gracefully after approval; Pieces OS stays running. Use `--close-desktop=false` if you want Desktop to stay open.

These commands use a local folder outside the usual OneDrive Documents folder. Run them in the same PowerShell window, from the extracted download folder:

```powershell
$exportRoot = Join-Path $env:LOCALAPPDATA 'Pieces-Exports'
New-Item -ItemType Directory -Force -Path $exportRoot | Out-Null
$exportRun = Join-Path $exportRoot (Get-Date -Format 'yyyyMMdd-HHmmss')
New-Item -ItemType Directory -Path $exportRun | Out-Null

.\pieces-export.exe export --format markdown --output "$exportRun\export" --work "$exportRun\private-work" --recovery-keys "$exportRun\private-keys"
$exportExit = $LASTEXITCODE
"Export exit code: $exportExit"
"Your files: $exportRun"
```

The output, workspace and keys are separate sibling folders. Keep the whole `export` folder together when moving it. Keep `private-work` and `private-keys` private and outside anything you share; both are needed for recovery. Each new export must use new output/workspace names.

Dates use your Windows system timezone by default. Use `--timezone America/New_York`, another IANA timezone, or `--timezone UTC` to override it.

Keep the terminal open and the computer awake until the final result. Progress reports fetching, privacy checks, writing and validation; the end of fetching is not the end of the export. Summary exports skip event-history bodies. Secret filtering is enabled by default, but review the files before sharing them.

Exit `0` means completed within the implemented scope; `2` means a finalized partial export with reported omissions. Read `export\index.md`, `export\coverage.md` and `export\manifest.json`. Exit `1` means failure: retain recovery files and the error message. Do not treat code `2` as a reason to delete or repeat the export.

## Recover an interrupted export

If the PowerShell window was closed, set `$exportRun` to the exact folder printed by the original run first. Run from the extracted executable's folder:

```powershell
.\pieces-export.exe resume --work "$exportRun\private-work" --recovery-keys "$exportRun\private-keys" --inspect
.\pieces-export.exe resume --work "$exportRun\private-work" --recovery-keys "$exportRun\private-keys" --output "$exportRun\recovered-export"
$exportExit = $LASTEXITCODE
```

Use a new destination on every retry. Do not delete the original `.partial` folder or recovery files while investigating. Unfinished fetching needs the same Pieces OS installation/user; once fetching is complete, recovery works offline.

## Unattended export with progress logs

Use a fresh `$exportRun` as above. This example assumes Pieces OS is already running and avoids launch/quit actions:

```powershell
.\pieces-export.exe export --yes --format markdown --launch-os=false --close-desktop=false --output "$exportRun\export" --work "$exportRun\private-work" --recovery-keys "$exportRun\private-keys" 1> "$exportRun\export.stdout.log" 2> "$exportRun\export.progress.log"
$exportExit = $LASTEXITCODE
```

Watch from another window with `Get-Content "FULL-PATH-TO\export.progress.log" -Wait`. Pass `--yes` for agents; preserve `$LASTEXITCODE` immediately, accept finalized partial exports explicitly, and require `index.md` plus `manifest.json` in the final output before reporting success. Progress is text on stderr, not a stable JSON API.

## Obsidian in the current development candidate

The newer development candidate supports `--format obsidian` in place of `--format markdown`. Open the output's **`vault` subfolder** as an Obsidian vault, then `Start Here.md`. The complete archive stays in the parent folder. Indexing can take time.

To convert a finalized Markdown export without contacting Pieces OS:

```powershell
.\pieces-export.exe obsidian --source "$exportRun\export" --output "$exportRun\obsidian-export" --yes
```

The older `0.18.0-rc2` download does not have this feature.

## If something goes wrong

- **EXE closes immediately:** run it in PowerShell, with a command such as `version` or `export`.
- **Wrong architecture:** download the ZIP matching System type. There is no 32-bit build.
- **Windows blocks the download:** these Windows candidates are unsigned. Verify the origin/checksum and send the exact warning to support. Managed machines may require IT approval. Do not disable antivirus or SmartScreen globally.
- **Access denied/recovery permissions:** use a new private local NTFS folder, preferably the location above. Do not run as administrator just to work around a shared-folder problem; export/resume as the same user. Network/removable/cloud folders can have incompatible permissions/locking.
- **Deep paths or viewer trouble:** keep the export root short. The exporter handles extended Windows paths, but Explorer and Markdown viewers may have their own limits. `--naming opaque` shortens generated names at the expense of readable filenames.
- **Tags absent in Explorer:** Windows Markdown property handlers are optional. Metadata remains inside Markdown and portable metadata sidecars even if Explorer cannot display native tags.
- **Pieces OS cannot launch:** start Pieces OS manually and retry. Staging on Windows requires an explicit `--environment staging --os-path "FULL-PATH-TO-STAGING-EXE"`; production and staging share the Pieces URL scheme.

For a support report, include the exporter version, System type, exit code and exact error. Avoid sending recovery keys or personal exported content unless intentionally requested and approved.
