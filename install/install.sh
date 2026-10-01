#!/usr/bin/env bash
# Pieces Export bootstrap. The downloaded CLI is proprietary; see LICENSE.txt.
# Bash 3.2+; no administrator rights, package manager, or PATH changes.
set -euo pipefail

# Keep EXIT cleanup in this function's subshell so its local state is still
# available when an unchecked command (such as curl) fails under errexit.
main() (
  local base_url='' version='' output='' cleanup='ask' install_only=false
  local install_dir='' verified=false archive executable os_name arch expected actual entries code=0
  local -a cli_args=()
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
      --keep) cleanup='keep'; shift ;;
      --remove) cleanup='remove'; shift ;;
      --install-only) install_only=true; shift ;;
      --) shift; cli_args=("$@"); break ;;
      --help|-h)
        printf '%s\n' 'Usage: install.sh --base-url HTTPS_RELEASE_ROOT --version VERSION [--output DIRECTORY] [--keep|--remove] [--install-only] [-- CLI_EXPORT_FLAGS...]'
        printf '%s\n' 'Downloads a checksum-verified CLI, runs export, then asks whether to remove this installation. Export files are kept.'
        return 0 ;;
      *) printf 'Unknown installer option: %s\n' "$1" >&2; return 1 ;;
    esac
  done
  # Reject credentials, query strings, whitespace, and path traversal in URLs.
  case "$base_url" in https://*) ;; *) printf 'An HTTPS --base-url is required.\n' >&2; return 1 ;; esac
  case "$base_url" in *'@'*|*'?'*|*'#'*|*' '*|*$'\n'*|*$'\r'*|*'/../'*|*'/./'*) printf 'Invalid release base URL.\n' >&2; return 1 ;; esac
  [[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || { printf 'A safe, pinned --version is required.\n' >&2; return 1; }
  case "$version" in *'..'*) printf 'Invalid version.\n' >&2; return 1 ;; esac
  for arg in "${cli_args[@]+${cli_args[@]}}"; do
    case "$arg" in --output|--output=*) printf 'Use the installer --output option before --.\n' >&2; return 1 ;; esac
  done
  case "$(uname -s)" in Darwin) os_name=darwin ;; Linux) os_name=linux ;; *) printf 'Use install.ps1 on Windows.\n' >&2; return 1 ;; esac
  case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) printf 'Unsupported processor architecture.\n' >&2; return 1 ;; esac
  for command in curl unzip mktemp; do command -v "$command" >/dev/null || { printf 'Required command missing: %s\n' "$command" >&2; return 1; }; done
  if ! command -v sha256sum >/dev/null && ! command -v shasum >/dev/null; then printf 'A SHA-256 utility is required.\n' >&2; return 1; fi
  if [ -z "$output" ]; then output="$HOME/Documents/Pieces-Exports/$(date '+%Y%m%d-%H%M%S')"; fi
  case "$output" in /*) ;; *) output="$PWD/$output" ;; esac
  [ ! -e "$output" ] && [ ! -e "$output.partial" ] || { printf 'Export destination already exists. Choose a new --output directory.\n' >&2; return 1; }
  umask 077
  install_dir="$(mktemp -d "${TMPDIR:-/tmp}/pieces-export.XXXXXXXX")"
  # This path is created exclusively by mktemp and never comes from --output.
  trap 'finish_install "$?"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  # shellcheck disable=SC2329 # Invoked by the EXIT trap.
  finish_install() {
    local exit_code="$1" reply=''
    trap - EXIT
    if [ "$verified" = false ]; then
      rm -rf -- "$install_dir"
    elif [ "$install_only" = true ] || [ "$cleanup" = keep ]; then
      printf 'CLI kept at: %s\n' "$install_dir/pieces-export"
      printf 'To uninstall, remove only this installation directory: %s\n' "$install_dir"
    else
      if [ "$cleanup" = ask ]; then
        if [ -t 0 ]; then
          printf 'Remove the downloaded CLI and installer files? Export files will stay. [Y/n] '
          IFS= read -r reply || reply=n
        elif { exec 3<>/dev/tty; } 2>/dev/null; then
          printf 'Remove the downloaded CLI and installer files? Export files will stay. [Y/n] ' >&3
          IFS= read -r reply <&3 || reply=n
          exec 3>&-
        else
          reply=n
          printf 'No interactive terminal; keeping the CLI. Use --remove for unattended cleanup.\n'
        fi
      fi
      case "$cleanup:$reply" in remove:*|ask:|ask:y|ask:Y|ask:yes|ask:YES)
        rm -rf -- "$install_dir"
        printf 'Downloaded utility removed. Export destination: %s\n' "$output" ;;
        *) printf 'CLI kept at: %s\n' "$install_dir/pieces-export" ;;
      esac
    fi
    exit "$exit_code"
  }
  archive="pieces-export_${version}_${os_name}_${arch}.zip"
  executable="$install_dir/pieces-export"
  base_url="${base_url%/}/$version"
  printf 'Downloading Pieces Export %s for %s/%s...\n' "$version" "$os_name" "$arch"
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --connect-timeout 20 --max-time 300 --silent --show-error "$base_url/SHA256SUMS.txt" -o "$install_dir/SHA256SUMS.txt"
  expected="$(awk -v file="$archive" '$2 == file {print $1}' "$install_dir/SHA256SUMS.txt")"
  [[ "$expected" =~ ^[a-fA-F0-9]{64}$ ]] || { printf 'Release checksum is missing or ambiguous.\n' >&2; exit 1; }
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --connect-timeout 20 --max-time 900 --silent --show-error "$base_url/$archive" -o "$install_dir/$archive"
  if command -v sha256sum >/dev/null; then actual="$(sha256sum "$install_dir/$archive" | awk '{print $1}')"; else actual="$(shasum -a 256 "$install_dir/$archive" | awk '{print $1}')"; fi
  [ "$(printf '%s' "$expected" | tr 'A-F' 'a-f')" = "$actual" ] || { printf 'Download checksum mismatch; nothing was executed.\n' >&2; exit 1; }
  entries="$(unzip -Z1 "$install_dir/$archive" | LC_ALL=C sort)"
  [ "$entries" = "$(printf '%s\n' LICENSE.txt README.md THIRD_PARTY_NOTICES.txt pieces-export | LC_ALL=C sort)" ] || { printf 'Unexpected files in release archive.\n' >&2; exit 1; }
  # Stream only exact allowed entries into new files; never trust ZIP paths,
  # symlink attributes, or an archive-provided executable permission bit.
  for name in pieces-export LICENSE.txt README.md THIRD_PARTY_NOTICES.txt; do unzip -p "$install_dir/$archive" "$name" > "$install_dir/$name"; done
  chmod 700 "$executable"
  verified=true
  printf 'Verified CLI: %s\nExport destination: %s\n' "$executable" "$output"
  if [ "$install_only" = true ]; then exit 0; fi
  # Reading the bootstrap from curl must not consume the export confirmation.
  if [ -t 0 ]; then
    "$executable" export --output "$output" "${cli_args[@]+${cli_args[@]}}" || code=$?
  elif { exec 3</dev/tty; } 2>/dev/null; then
    "$executable" export --output "$output" "${cli_args[@]+${cli_args[@]}}" <&3 || code=$?
    exec 3<&-
  else
    "$executable" export --output "$output" "${cli_args[@]+${cli_args[@]}}" </dev/null || code=$?
  fi
  # Preserve CLI status: 0 complete, 2 partial, 1 failure, 130 interrupted.
  exit "$code"
)

main "$@"
