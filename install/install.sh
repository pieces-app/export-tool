#!/usr/bin/env bash
# Pieces Export installer for macOS and Linux.
#
#   curl -fsSL https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.sh | bash
#
# Downloads the pieces-export tool, checks its SHA-256 before running it, and
# exports your Pieces memories to Markdown in Documents/Pieces-Exports. The tool
# is kept so an interrupted export can be resumed. Pieces Export is open source
# under the MIT License: https://github.com/pieces-app/export-tool
# Bash 3.2+; no administrator rights, package manager, or PATH changes.
set -euo pipefail

PIECES_RELEASE_VERSION='0.18.0-rc3'
PIECES_RELEASE_FOLDER='https://drive.google.com/drive/folders/1lABvGdTeCue2AMRHSAS_cl4dQ9OYEiE4'
PIECES_INSTALLER_URL='https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.sh'

# The built-in release lives in a public Google Drive folder. Each file is
# pinned by Drive ID and SHA-256, so a changed or substituted file never runs.
# Prints "<drive id> <sha256> <file name>".
pieces_release_file() {
  case "$1" in
    darwin_arm64) echo '1RGIoNvro0AuyADwL3zKTKFZi1oDal9l- 88dc2067fb9e27321bdb37ec3f463ecf5b6a67d7282131b94fd64f78dafa9a1b pieces-export_0.18.0-rc3_darwin_arm64_notarized.zip' ;;
    darwin_amd64) echo '1vgoUYENQRqob845TYnXJzSi7jdUQJl3f 24f27445f0a769970ffc9b3a2e0a674cb83e1e86946fd63de1a004fcd71fa167 pieces-export_0.18.0-rc3_darwin_amd64_notarized.zip' ;;
    linux_amd64) echo '1alrEphCpjI2l8A_vkQ3y3aKOkwt1a_LI 1579cf23a9c1be22967ece3ba5c1f1b6c88da5c0ddcc30c8f5916c0919905286 pieces-export_0.18.0-rc3_linux_amd64.zip' ;;
    linux_arm64) echo '1ReiyL2qQRbDYjQMht1fEX5EQ8baCFTHY a6bc477aac963cd277890ba8b10de5edf6cb56ff5c5890da85cbbf2e489ef1cd pieces-export_0.18.0-rc3_linux_arm64.zip' ;;
    *) return 1 ;;
  esac
}

usage() {
  cat <<EOF
Usage: install.sh [options] [-- pieces-export flags]

Downloads the Pieces Export tool, checks its SHA-256, and exports your Pieces
memories to Markdown in Documents/Pieces-Exports. The tool is kept afterward.

Options:
  --output DIR     Export into DIR instead of Documents/Pieces-Exports/<date_time>
  --resume         Continue the most recent export that did not finish
  --dry-run        Scan and estimate the export time without writing anything
  --no-open        Don't open the export folder when it finishes
  --install-only   Download and verify the tool, then stop
  --remove         Delete the tool after this run (it is kept by default)
  --ask            Ask whether to delete the tool after this run
  --version VER    Release to install (default: $PIECES_RELEASE_VERSION)
  --base-url URL   Download VER from URL/VER/ instead of the built-in release
  -h, --help       Show this help

Anything after -- goes to pieces-export, for example:  -- --format both
EOF
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

# fetch URL DEST MAX_SECONDS [progress]
fetch() {
  local meter='--silent'
  if [ "${4:-}" = progress ] && [ -t 2 ]; then meter='--progress-bar'; fi
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --connect-timeout 20 \
    --max-time "$3" "$meter" --show-error "$1" -o "$2"
}

# Prints the newest saved session that still has its recovery folders.
latest_session() {
  local dir found=''
  [ -d "$1" ] || return 1
  for dir in "$1"/*; do
    if [ -d "$dir/work" ] && [ -d "$dir/keys" ]; then found="$dir"; fi
  done
  [ -n "$found" ] || return 1
  printf '%s\n' "$found"
}

# Keep EXIT handling in this function's subshell so its local state is still
# available when an unchecked command (such as curl) fails under errexit.
main() (
  local base_url='' version='' output='' cleanup='keep' install_only=false resume=false dry_run=false no_open=false
  local data_root='' tool_dir='' staging='' session='' owns_session=false verified=false started=false code=0
  local os_name='' arch='' label='' archive='' expected='' actual='' entries='' executable='' docs='' dir='' entry=''
  local format_given=false format_value='' recovery_given=false sdk_cache=false dry_flag=false prev='' arg=''
  local -a cli_args=() run_args=()
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --base-url|--version|--output)
        [ "$#" -ge 2 ] || { printf 'Missing value for %s\n' "$1" >&2; return 1; }
        case "$1" in
          --base-url) base_url="$2" ;;
          --version) version="$2" ;;
          --output) output="$2" ;;
        esac
        shift 2 ;;
      --resume) resume=true; shift ;;
      --dry-run) dry_run=true; shift ;;
      --no-open) no_open=true; shift ;;
      --install-only) install_only=true; shift ;;
      --keep) cleanup='keep'; shift ;;
      --remove) cleanup='remove'; shift ;;
      --ask) cleanup='ask'; shift ;;
      --) shift; cli_args=("$@"); break ;;
      -h|--help) usage; return 0 ;;
      *) printf 'Unknown installer option: %s (see --help)\n' "$1" >&2; return 1 ;;
    esac
  done

  for arg in ${cli_args[@]+"${cli_args[@]}"}; do
    case "$arg" in
      --output|--output=*|-output|-output=*) printf 'Use the installer --output option before --.\n' >&2; return 1 ;;
      --format=*|-format=*) format_given=true; format_value="${arg#*=}" ;;
      --format|-format) format_given=true ;;
      --work|--work=*|-work|-work=*|--recovery-keys|--recovery-keys=*|-recovery-keys|-recovery-keys=*) recovery_given=true ;;
      --sdk-cache|--sdk-cache=*|-sdk-cache|-sdk-cache=*) sdk_cache=true ;;
      --dry-run|--dry-run=true|-dry-run|-dry-run=true) dry_run=true; dry_flag=true ;;
    esac
    case "$prev" in --format|-format) format_value="$arg" ;; esac
    prev="$arg"
  done
  if [ "$resume" = true ] && [ "$dry_run" = true ]; then printf 'Use either --resume or --dry-run, not both.\n' >&2; return 1; fi

  if [ -n "$base_url" ]; then
    # Reject credentials, query strings, whitespace, and path traversal in URLs.
    case "$base_url" in https://*) ;; *) printf 'An HTTPS --base-url is required.\n' >&2; return 1 ;; esac
    case "$base_url" in *'@'*|*'?'*|*'#'*|*' '*|*$'\n'*|*$'\r'*|*'/../'*|*'/./'*) printf 'Invalid release base URL.\n' >&2; return 1 ;; esac
    [[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || { printf 'A safe, pinned --version is required with --base-url.\n' >&2; return 1; }
    case "$version" in *'..'*) printf 'Invalid version.\n' >&2; return 1 ;; esac
  else
    [ -n "$version" ] || version="$PIECES_RELEASE_VERSION"
    if [ "$version" != "$PIECES_RELEASE_VERSION" ]; then
      printf 'Version %s is not available from this installer, which provides %s.\n' "$version" "$PIECES_RELEASE_VERSION" >&2
      return 1
    fi
  fi

  case "$(uname -s)" in
    Darwin) os_name=darwin ;;
    Linux) os_name=linux ;;
    *) printf 'This installer supports macOS and Linux. On Windows, use install.ps1.\n' >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64) arch=amd64 ;;
    *) printf 'Unsupported processor: %s\n' "$(uname -m)" >&2; return 1 ;;
  esac
  # A Terminal running under Rosetta reports x86_64 on Apple silicon.
  if [ "$os_name" = darwin ] && [ "$(sysctl -in sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then arch=arm64; fi
  case "${os_name}_$arch" in
    darwin_arm64) label='macOS (Apple silicon)' ;;
    darwin_amd64) label='macOS (Intel)' ;;
    linux_arm64) label='Linux (ARM64)' ;;
    *) label='Linux (x86-64)' ;;
  esac
  for dir in curl unzip mktemp; do
    command -v "$dir" >/dev/null 2>&1 || { printf 'This installer needs %s. Install it with your package manager, then run the command again.\n' "$dir" >&2; return 1; }
  done
  if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then printf 'A SHA-256 utility (sha256sum or shasum) is required.\n' >&2; return 1; fi

  if [ -n "${PIECES_EXPORT_HOME:-}" ]; then
    data_root="$PIECES_EXPORT_HOME"
  elif [ "$os_name" = darwin ]; then
    data_root="$HOME/Library/Application Support/Pieces Export"
  else
    data_root="${XDG_DATA_HOME:-$HOME/.local/share}/pieces-export"
  fi
  case "$data_root" in /*) ;; *) data_root="$PWD/$data_root" ;; esac

  if [ "$resume" = true ]; then
    session="$(latest_session "$data_root/recovery")" || session=''
    if [ -z "$session" ]; then printf 'No unfinished export was found to resume.\n' >&2; return 1; fi
  fi
  if [ "$install_only" = false ] && [ "$dry_run" = false ]; then
    docs="$HOME/Documents"
    if [ "$os_name" = linux ] && command -v xdg-user-dir >/dev/null 2>&1; then
      dir="$(xdg-user-dir DOCUMENTS 2>/dev/null || true)"
      if [ -n "$dir" ] && [ "$dir" != "$HOME" ]; then docs="$dir"; fi
    fi
    [ -n "$output" ] || output="$docs/Pieces-Exports/$(date '+%Y-%m-%d_%H-%M-%S')"
    case "$output" in /*) ;; *) output="$PWD/$output" ;; esac
    if [ -e "$output" ] || [ -e "$output.partial" ]; then
      printf 'The export folder already exists: %s\nChoose a new --output folder.\n' "$output" >&2
      return 1
    fi
  fi

  umask 077
  mkdir -p "$data_root/tool" 2>/dev/null || { printf 'Could not create the tool folder: %s\n' "$data_root/tool" >&2; return 1; }
  staging="$(mktemp -d "$data_root/tool/.download.XXXXXXXX")"

  # shellcheck disable=SC2329 # Invoked by the EXIT trap.
  report() {
    if [ "$dry_run" = true ]; then
      printf '\nDry run finished. Nothing was exported.\n'
      return 0
    fi
    if [ -f "$output/manifest.json" ]; then
      if [ -n "$session" ]; then rm -rf -- "$session"; fi
      printf '\nYour export is ready:\n  %s\n' "$output"
      if [ "$1" = 2 ]; then printf 'Some records were unavailable. coverage.md in that folder lists them.\n'; fi
      printf 'Start with index.md. Obsidian or VS Code are good ways to browse it.\n'
      if [ "$no_open" = false ] && [ -t 1 ]; then
        if [ "$os_name" = darwin ]; then
          open "$output" >/dev/null 2>&1 || true
        elif command -v xdg-open >/dev/null 2>&1 && { [ -n "${DISPLAY:-}" ] || [ -n "${WAYLAND_DISPLAY:-}" ]; }; then
          (xdg-open "$output" >/dev/null 2>&1 &)
        fi
      fi
      return 0
    fi
    if [ -n "$session" ] && [ -d "$session/work" ]; then
      printf '\nThe export did not finish, but your progress is saved.\nTo continue where it stopped, run:\n  curl -fsSL %s | bash -s -- --resume\n' "$PIECES_INSTALLER_URL"
      return 0
    fi
    if [ "$owns_session" = true ] && [ -n "$session" ]; then rm -rf -- "$session"; fi
    if [ "$1" = 0 ]; then
      printf '\nNo export was written.\n'
    else
      printf '\nThe export did not finish and nothing was saved yet. Fix the problem above, then run the same command again.\n'
    fi
  }

  # shellcheck disable=SC2329 # Invoked by the EXIT trap.
  finish() {
    local exit_code="$1" reply=''
    trap - EXIT
    rm -rf -- "$staging"
    if [ "$started" = true ]; then report "$exit_code"; fi
    if [ "$verified" = true ] && [ "$install_only" = false ]; then
      if [ "$cleanup" = ask ]; then
        if [ -t 0 ]; then
          printf 'Remove the export tool? Your exports will stay. [y/N] '
          IFS= read -r reply || reply=''
        elif { exec 3<>/dev/tty; } 2>/dev/null; then
          printf 'Remove the export tool? Your exports will stay. [y/N] ' >&3
          IFS= read -r reply <&3 || reply=''
          exec 3>&-
        else
          printf 'No interactive terminal; keeping the export tool.\n'
        fi
        case "$reply" in y|Y|yes|YES|Yes) cleanup='remove' ;; esac
      fi
      if [ "$cleanup" = remove ]; then
        rm -rf -- "$tool_dir"
        printf 'Removed the export tool. Your exports were not touched.\n'
      else
        printf '\nThe export tool is kept at:\n  %s\n' "$executable"
      fi
    fi
    exit "$exit_code"
  }
  trap 'finish "$?"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  printf 'Pieces Export %s for %s\n' "$version" "$label"
  if [ -n "$base_url" ]; then
    archive="pieces-export_${version}_${os_name}_${arch}.zip"
    base_url="${base_url%/}/$version"
    printf 'Downloading the export tool...\n'
    fetch "$base_url/SHA256SUMS.txt" "$staging/SHA256SUMS.txt" 120
    expected="$(awk -v file="$archive" '$2 == file {print $1}' "$staging/SHA256SUMS.txt")"
    [[ "$expected" =~ ^[a-fA-F0-9]{64}$ ]] || { printf 'Release checksum is missing or ambiguous.\n' >&2; exit 1; }
    fetch "$base_url/$archive" "$staging/$archive" 900 progress
  else
    entry="$(pieces_release_file "${os_name}_$arch")" || { printf 'There is no download for %s yet.\n' "$label" >&2; exit 1; }
    archive="${entry##* }"
    expected="${entry#* }"
    expected="${expected%% *}"
    printf 'Downloading the export tool from Google Drive...\n'
    fetch "https://drive.usercontent.google.com/download?id=${entry%% *}&export=download&confirm=t" "$staging/$archive" 900 progress
  fi
  actual="$(sha256_of "$staging/$archive")"
  if [ "$(printf '%s' "$expected" | tr 'A-F' 'a-f')" != "$actual" ]; then
    printf 'The download does not match its expected SHA-256, so nothing was run.\n' >&2
    if [ -z "$base_url" ]; then
      printf 'Google Drive may be limiting downloads. Wait a few minutes and try again, or download it from:\n  %s\n' "$PIECES_RELEASE_FOLDER" >&2
    fi
    exit 1
  fi
  entries="$(unzip -Z1 "$staging/$archive" | LC_ALL=C sort)"
  [ "$entries" = "$(printf '%s\n' LICENSE.txt README.md THIRD_PARTY_NOTICES.txt pieces-export | LC_ALL=C sort)" ] || { printf 'Unexpected files in the download, so nothing was run.\n' >&2; exit 1; }
  # Stream only exact allowed entries into new files; never trust ZIP paths,
  # symlink attributes, or an archive-provided executable permission bit.
  mkdir "$staging/tool"
  for dir in pieces-export LICENSE.txt README.md THIRD_PARTY_NOTICES.txt; do unzip -p "$staging/$archive" "$dir" > "$staging/tool/$dir"; done
  chmod 700 "$staging/tool/pieces-export"
  tool_dir="$data_root/tool/$version"
  rm -rf -- "$tool_dir"
  mv -- "$staging/tool" "$tool_dir"
  executable="$tool_dir/pieces-export"
  verified=true
  printf 'Verified the download (SHA-256 matches).\n'
  if [ "$install_only" = true ]; then
    printf 'The export tool is ready:\n  %s\n' "$executable"
    exit 0
  fi

  if [ "$resume" = true ]; then
    run_args=(resume --work "$session/work" --recovery-keys "$session/keys" --output "$output")
    printf 'Resuming the export saved in:\n  %s\n' "$session"
  elif [ "$dry_run" = true ]; then
    run_args=(export)
    if [ "$format_given" = false ]; then run_args+=(--format markdown); fi
    if [ "$dry_flag" = false ]; then run_args+=(--dry-run); fi
  else
    run_args=(export --output "$output")
    if [ "$format_given" = false ]; then run_args+=(--format markdown); format_value=markdown; fi
    # The CLI can resume Markdown exports without SDK caches. Keep its private
    # recovery folders outside Documents so they are not synced or shared.
    if [ "$recovery_given" = false ] && [ "$format_value" = markdown ] && [ "$sdk_cache" = false ]; then
      mkdir -p "$data_root/recovery"
      session="$(mktemp -d "$data_root/recovery/$(date '+%Y%m%d-%H%M%S').XXXXXX")"
      owns_session=true
      printf 'version=%s\noutput=%s\n' "$version" "$output" > "$session/session.txt"
      run_args+=(--work "$session/work" --recovery-keys "$session/keys")
    fi
  fi
  run_args+=(${cli_args[@]+"${cli_args[@]}"})
  if [ "$dry_run" = false ]; then printf 'Your export will be saved to:\n  %s\n\n' "$output"; fi

  started=true
  # Reading this script from curl must not consume the CLI's prompts.
  if [ -t 0 ]; then
    "$executable" "${run_args[@]}" || code=$?
  elif { exec 3</dev/tty; } 2>/dev/null; then
    "$executable" "${run_args[@]}" <&3 || code=$?
    exec 3<&-
  else
    "$executable" "${run_args[@]}" </dev/null || code=$?
  fi
  # Preserve CLI status: 0 complete, 2 partial, 1 failure, 130 interrupted.
  exit "$code"
)

main "$@"
