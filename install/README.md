# Pieces Export

Save your Pieces memories as Markdown files you keep: your summaries, your persona and profile histories, and the links between them. The tool only reads your local Pieces data. Nothing in Pieces is changed or deleted.

Version **0.18.0-rc6** (early access). Pieces Export is open source under the MIT License; the source code is at [github.com/pieces-app/export-tool](https://github.com/pieces-app/export-tool).

## Quick start

Keep Pieces installed. The tool starts PiecesOS if it isn't running.

**Mac or Linux.** Open Terminal and run:

```sh
curl -fsSL https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.sh | bash
```

**Windows.** Open PowerShell (Start menu, type "PowerShell") and run:

```powershell
irm https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.ps1 | iex
```

## What happens

1. It downloads the tool for your computer (about 8 MB) from our Google Drive folder. It checks the file's SHA-256 fingerprint before running anything.
2. It finds Pieces, scans your memories, and shows how long the export should take.
3. It asks `Export now? [Y/n]`. Press Return to start. Pieces Desktop closes during the export, and PiecesOS keeps running.
4. It shows progress as it works. Large libraries can take 30 minutes or more, so keep the window open and the computer awake and plugged in.
5. When it's done, it opens your export folder. Start with `index.md`. [Obsidian](https://obsidian.md) or [VS Code](https://code.visualstudio.com) are good ways to browse it. Keep the folder together so the links keep working.

On a Mac, you may see "Terminal would like to access files in your Documents folder." Click **Allow**, because that's where the export is saved.

## If the export is interrupted

Your progress is saved automatically. Run this to continue where it stopped:

```sh
curl -fsSL https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.sh | bash -s -- --resume
```

```powershell
& ([scriptblock]::Create((irm https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.ps1))) -Resume
```

The finished export goes into a new folder in Documents/Pieces-Exports. On a Mac whose Documents folder syncs with iCloud, it goes in Pieces-Exports in your home folder instead.

## Options

Add options to the end of the command:

- **Mac or Linux:** `curl -fsSL <url>/install.sh | bash -s -- OPTIONS`
- **Windows:** `& ([scriptblock]::Create((irm <url>/install.ps1))) OPTIONS`

| To do this | Mac or Linux | Windows |
| --- | --- | --- |
| Estimate the time without exporting | `--dry-run` | `-DryRun` |
| Continue an interrupted export | `--resume` | `-Resume` |
| Save the export somewhere else | `--output ~/Desktop/my-export` | `-Output "$HOME\Desktop\my-export"` |
| Also make PDFs (experimental) | `-- --format both` | `-ExportArgs '--format','both'` |
| Don't open the folder at the end | `--no-open` | `-NoOpen` |
| Delete the tool afterward | `--remove` | `-Cleanup Remove` |
| Only download and check the tool | `--install-only` | `-InstallOnly` |
| Skip the Export now? question | `-- --yes` | `-ExportArgs '--yes'` |
| Make an Obsidian vault instead | `-- --format obsidian` | `-ExportArgs '--format','obsidian'` |

PDF exports can't be resumed after an interruption. Markdown exports can.

## Where things are saved

| | Mac | Windows | Linux |
| --- | --- | --- | --- |
| Your export | `~/Documents/Pieces-Exports/<date_time>` | `Documents\Pieces-Exports\<date_time>` | `~/Documents/Pieces-Exports/<date_time>` |
| The tool | `~/Library/Application Support/Pieces Export/tool/` | `%LOCALAPPDATA%\Pieces Export\tool\` | `~/.local/share/pieces-export/tool/` |
| Resume data | `~/Library/Application Support/Pieces Export/recovery/` | `%LOCALAPPDATA%\Pieces Export\recovery\` | `~/.local/share/pieces-export/recovery/` |

When the installer detects that iCloud Drive syncs your Desktop & Documents folders, the export goes to `~/Pieces-Exports/<date_time>` instead, outside the synced folder. Use `--output` to choose another folder.

The resume data is private and encrypted. It stays on your computer and isn't saved in Documents, so it isn't synced. It's deleted when an export finishes. The tool is kept so you can resume or run it again. To remove everything except your exports, delete the `Pieces Export` folder (Linux: `pieces-export`) shown above.

## Open it in Obsidian

The export is plain Markdown, so you can open the folder in Obsidian as it is. For a large library, a compact vault browses better. The tool you installed can build one from a finished export:

- **Mac:** `"$HOME/Library/Application Support/Pieces Export/tool/0.18.0-rc6/pieces-export" obsidian --source <your export folder> --output ~/Pieces-Obsidian --yes`
- **Windows:** `& "$env:LOCALAPPDATA\Pieces Export\tool\0.18.0-rc6\pieces-export.exe" obsidian --source <your export folder> --output "$HOME\Pieces-Obsidian" --yes`
- **Linux:** `~/.local/share/pieces-export/tool/0.18.0-rc6/pieces-export obsidian --source <your export folder> --output ~/Pieces-Obsidian --yes`

Replace `<your export folder>` with the folder the installer opened when it finished. Then, in Obsidian, choose **Open folder as vault** and pick the `vault` folder inside `Pieces-Obsidian`. Start with `Start Here.md`. Keep the original export as your complete archive; the vault links back to it.

To get a vault straight from the export, add `-- --format obsidian` on Mac or Linux, or `-ExportArgs '--format','obsidian'` on Windows. That export can't be resumed if it's interrupted.

## Manual download

To skip the script, download the ZIP for your computer from the [Google Drive folder](https://drive.google.com/drive/folders/1G5GeGokdR5egJl383Uvj2KmV6x3B4fQg):

| Computer | Download |
| --- | --- |
| Mac with Apple silicon (M1 or newer) | [macOS ARM64](https://drive.google.com/file/d/1L4nPG40NuW75nSZ0Vac8MzsXjNu5esPN/view) |
| Mac with an Intel processor | [macOS x86-64](https://drive.google.com/file/d/1l0pscYFtcdpGnbO6BYi-hwF2Y2udmpAI/view) |
| Windows, Intel or AMD | [Windows x86-64](https://drive.google.com/file/d/1kyPy47nU1aX7M63QPBtGiKG0BX4Sbyfd/view) |
| Windows on ARM | [Windows ARM64](https://drive.google.com/file/d/1H8KwPyzbdU4GVNdV30_BUfdurGy8hYdE/view) |
| Linux, Intel or AMD | [Linux x86-64](https://drive.google.com/file/d/1xqAI4gqBR39j6dOIaEYMy6r8Rzk4h8HL/view) |
| Linux on ARM | [Linux ARM64](https://drive.google.com/file/d/1dans8QR1VKw-p-rdhWM1_BsEBnexvOs0/view) |

Not sure which one you need? On a Mac, open the Apple menu and choose About This Mac. "Chip: Apple M..." means Apple silicon. On Windows, open Settings, then System, then About, and check System type. On Linux, run `uname -m`. `x86_64` means x86-64, and `aarch64` means ARM64.

Open Pieces and wait until it has loaded, then run these commands in your Downloads folder. Swap in your ZIP's name where it differs.

Mac, in Terminal:

```sh
cd ~/Downloads
unzip pieces-export_0.18.0-rc6_darwin_arm64_notarized.zip -d pieces-export-tool
cd pieces-export-tool
./pieces-export version
./pieces-export export --dry-run --format markdown --launch-os=false
caffeinate -i ./pieces-export export --format markdown --launch-os=false --close-desktop=false --output ./my-pieces-export --work ./private-work --recovery-keys ./private-keys
```

Windows, in PowerShell:

```powershell
cd $HOME\Downloads
Expand-Archive .\pieces-export_0.18.0-rc6_windows_amd64.zip -DestinationPath .\pieces-export-tool
cd .\pieces-export-tool
.\pieces-export.exe version
.\pieces-export.exe export --dry-run --format markdown --launch-os=false
.\pieces-export.exe export --format markdown --launch-os=false --close-desktop=false --output .\my-pieces-export --work .\private-work --recovery-keys .\private-keys
```

Linux, in a terminal:

```sh
cd ~/Downloads
unzip pieces-export_0.18.0-rc6_linux_amd64.zip -d pieces-export-tool
cd pieces-export-tool
chmod u+x ./pieces-export
./pieces-export version
./pieces-export export --dry-run --format markdown --launch-os=false
./pieces-export export --format markdown --launch-os=false --close-desktop=false --output ./my-pieces-export --work ./private-work --recovery-keys ./private-keys
```

To continue an interrupted manual export, keep `private-work` and `private-keys` and run:

```sh
./pieces-export resume --work ./private-work --recovery-keys ./private-keys --output ./recovered-export
```

On Windows, use `.\pieces-export.exe` and `.\` paths. To check a manual download, save the matching `.sha256` file from the folder next to the ZIP and run `shasum -a 256 -c <file>.sha256` on a Mac or `sha256sum -c <file>.sha256` on Linux. Both should print OK. On Windows, compare the output of `Get-FileHash <zip>` with the `.sha256` file.

## Troubleshooting

- **Windows says `irm` isn't recognized.** You're in Command Prompt. Open PowerShell instead.
- **It stops with "recovery storage operation failed" on Windows.** Versions before 0.18.0-rc6 did this on every Windows machine. Run the install command again to get the current version. If it still fails, the text after the colon names the Windows error; send us that line.
- **Windows or antivirus warns about the tool.** The Windows build isn't code-signed yet. The script checks the download's SHA-256 before running it. If Windows still blocks it, send us the exact message.
- **It says Pieces OS was not found.** Open Pieces, wait until it has fully loaded, then run the command again.
- **The download does not match its expected SHA-256.** Google Drive may be limiting downloads. Wait a few minutes and try again. If it keeps happening, use the manual download above.
- **Linux says it needs curl or unzip.** Install them, for example with `sudo apt install curl unzip`.
- **Some records were unavailable.** The export still finished. `coverage.md` in the export folder lists what was missing.

To report a problem, send the tool version, your operating system and chip, and the last thing the tool printed. Please don't send your export or the resume data.

## Security

- The scripts download only the Google Drive files pinned in them. Each file must match the SHA-256 written in the script, and the ZIP must contain exactly the tool, its README, and its license files. Anything else is rejected before it runs.
- The Mac tool is signed with our Apple Developer ID (Mesh Intelligent Technologies, Inc.) and notarized by Apple. The Windows and Linux tools aren't code-signed yet.
- No administrator rights, PATH changes, or background services are involved. You can read both scripts in this Gist before running them.
- Pieces Export is open source under the [MIT License](https://github.com/pieces-app/export-tool/blob/main/LICENSE.txt). The license and third-party notices come with every download.
