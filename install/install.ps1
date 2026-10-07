# Pieces Export installer for Windows (Windows PowerShell 5.1 or PowerShell 7).
#
#   irm https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.ps1 | iex
#
# Downloads the pieces-export tool, checks its SHA-256 before running it, and
# exports your Pieces memories to Markdown in Documents\Pieces-Exports. The tool
# is kept so an interrupted export can be resumed. No administrator rights are
# needed. Pieces Export is open source under the MIT License:
# https://github.com/pieces-app/export-tool
[CmdletBinding()]
param(
    [string]$BaseUrl,
    [string]$Version,
    [string]$Output,
    [switch]$Resume,
    [switch]$DryRun,
    [switch]$NoOpen,
    [switch]$InstallOnly,
    [ValidateSet('Keep', 'Remove', 'Ask')][string]$Cleanup = 'Keep',
    [string[]]$ExportArgs = @()
)

# The built-in release lives in a public Google Drive folder. Each file is
# pinned by Drive ID and SHA-256, so a changed or substituted file never runs.
function Get-PiecesBuiltInRelease {
    @{
        Version      = '0.18.0-rc3'
        Folder       = 'https://drive.google.com/drive/folders/1lABvGdTeCue2AMRHSAS_cl4dQ9OYEiE4'
        InstallerUrl = 'https://gist.githubusercontent.com/tsavo-at-pieces/e6d4dd3419ace84d8ca7be085fee3bb1/raw/install.ps1'
        Files        = @{
            windows_amd64 = @{ Id = '1FUpqjfpXRfumjfECHJB3IMi2mD2zZ04Z'; Sha256 = 'fd8331e0730a8248df2c6a34abb6b06ff7342500bcfacc3dfd26b99c116ef7e6'; Name = 'pieces-export_0.18.0-rc3_windows_amd64.zip' }
            windows_arm64 = @{ Id = '1b7I4WOu1yl6JFROX6KNMwn5Aie0cMS33'; Sha256 = '538a4c1c475bfe46c6b2eb53de979c7d920982d1848267a1db6cdf690bd0cebf'; Name = 'pieces-export_0.18.0-rc3_windows_arm64.zip' }
            darwin_arm64  = @{ Id = '1RGIoNvro0AuyADwL3zKTKFZi1oDal9l-'; Sha256 = '88dc2067fb9e27321bdb37ec3f463ecf5b6a67d7282131b94fd64f78dafa9a1b'; Name = 'pieces-export_0.18.0-rc3_darwin_arm64_notarized.zip' }
            darwin_amd64  = @{ Id = '1vgoUYENQRqob845TYnXJzSi7jdUQJl3f'; Sha256 = '24f27445f0a769970ffc9b3a2e0a674cb83e1e86946fd63de1a004fcd71fa167'; Name = 'pieces-export_0.18.0-rc3_darwin_amd64_notarized.zip' }
            linux_amd64   = @{ Id = '1alrEphCpjI2l8A_vkQ3y3aKOkwt1a_LI'; Sha256 = '1579cf23a9c1be22967ece3ba5c1f1b6c88da5c0ddcc30c8f5916c0919905286'; Name = 'pieces-export_0.18.0-rc3_linux_amd64.zip' }
            linux_arm64   = @{ Id = '1ReiyL2qQRbDYjQMht1fEX5EQ8baCFTHY'; Sha256 = 'a6bc477aac963cd277890ba8b10de5edf6cb56ff5c5890da85cbbf2e489ef1cd'; Name = 'pieces-export_0.18.0-rc3_linux_arm64.zip' }
        }
    }
}

function Get-PiecesReleaseFile {
    param([string]$Url, [string]$Destination, [long]$MaxBytes)
    Add-Type -AssemblyName System.Net.Http
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    $handler = New-Object Net.Http.HttpClientHandler
    $handler.AllowAutoRedirect = $false
    $client = New-Object Net.Http.HttpClient($handler)
    $client.Timeout = [TimeSpan]::FromMinutes(15)
    $deadline = New-Object Threading.CancellationTokenSource(900000)
    try {
        $uri = [Uri]$Url
        for ($redirects = 0; $redirects -le 5; $redirects++) {
            if ($uri.Scheme -ne 'https' -or $uri.UserInfo) { throw 'Only HTTPS downloads without URL credentials are supported.' }
            $response = $client.GetAsync($uri, [Net.Http.HttpCompletionOption]::ResponseHeadersRead, $deadline.Token).GetAwaiter().GetResult()
            try {
                $status = [int]$response.StatusCode
                if ($status -in @(301, 302, 303, 307, 308)) {
                    if (!$response.Headers.Location) { throw 'Download redirect has no destination.' }
                    $uri = New-Object Uri($uri, $response.Headers.Location)
                    continue
                }
                $null = $response.EnsureSuccessStatusCode()
                if ($response.Content.Headers.ContentLength -gt $MaxBytes) { throw 'Download exceeds the size limit.' }
                $source = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
                $target = [IO.File]::Open($Destination, [IO.FileMode]::CreateNew)
                try {
                    $buffer = New-Object byte[] 65536
                    [long]$total = 0
                    while (($read = $source.ReadAsync($buffer, 0, $buffer.Length, $deadline.Token).GetAwaiter().GetResult()) -gt 0) {
                        $total += $read
                        if ($total -gt $MaxBytes) { throw 'Download exceeds the size limit.' }
                        $target.Write($buffer, 0, $read)
                    }
                } finally { $target.Dispose(); $source.Dispose() }
                return
            } finally { $response.Dispose() }
        }
        throw 'Too many download redirects.'
    } finally { $deadline.Dispose(); $client.Dispose(); $handler.Dispose() }
}

# Quotes one argument for a Windows command line (CommandLineToArgvW rules).
function ConvertTo-PiecesArgument {
    param([AllowEmptyString()][string]$Value)
    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') { return $Value }
    $builder = New-Object Text.StringBuilder
    $null = $builder.Append('"')
    $slashes = 0
    foreach ($ch in $Value.ToCharArray()) {
        if ($ch -eq [char]92) { $slashes++; continue }
        if ($ch -eq [char]34) {
            $null = $builder.Append([char]92, 2 * $slashes + 1)
        } elseif ($slashes -gt 0) {
            $null = $builder.Append([char]92, $slashes)
        }
        $null = $builder.Append($ch)
        $slashes = 0
    }
    $null = $builder.Append([char]92, 2 * $slashes)
    $null = $builder.Append('"')
    $builder.ToString()
}

# Runs a program on this console without capturing its output, so prompts
# such as "Export now? [Y/n]" appear immediately and keyboard input reaches it.
function Start-PiecesProcess {
    param([string]$FilePath, [string[]]$Arguments = @(), [switch]$UseArgumentString)
    $info = New-Object Diagnostics.ProcessStartInfo
    $info.FileName = $FilePath
    $info.UseShellExecute = $false
    if (!$UseArgumentString -and $null -ne $info.PSObject.Properties['ArgumentList']) {
        foreach ($argument in $Arguments) { $info.ArgumentList.Add($argument) }
    } else {
        $info.Arguments = (@($Arguments | ForEach-Object { ConvertTo-PiecesArgument $_ }) -join ' ')
    }
    $process = [Diagnostics.Process]::Start($info)
    try {
        $process.WaitForExit()
        return $process.ExitCode
    } finally { $process.Dispose() }
}

function Get-PiecesPlatform {
    $windowsHost = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    $os = 'windows'
    if (!$windowsHost) {
        $kernel = (& uname -s).Trim()
        $os = switch ($kernel) { 'Darwin' { 'darwin' } 'Linux' { 'linux' } default { throw 'Unsupported operating system.' } }
    }
    $cpu = $null
    try { $cpu = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { $cpu = $null }
    if (!$cpu) {
        if ($windowsHost) {
            $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        } else { $cpu = (& uname -m).Trim() }
    }
    $arch = switch -Regex ($cpu) { '^(Arm64|ARM64|aarch64|arm64)$' { 'arm64'; break } '^(X64|AMD64|x86_64|amd64)$' { 'amd64'; break } default { throw "Unsupported processor: $cpu" } }
    $label = switch ("${os}_$arch") {
        'windows_amd64' { 'Windows (x86-64)' } 'windows_arm64' { 'Windows (ARM64)' }
        'darwin_arm64' { 'macOS (Apple silicon)' } 'darwin_amd64' { 'macOS (Intel)' }
        'linux_arm64' { 'Linux (ARM64)' } default { 'Linux (x86-64)' }
    }
    $executable = 'pieces-export'
    if ($windowsHost) { $executable = 'pieces-export.exe' }
    @{ Windows = $windowsHost; Os = $os; Arch = $arch; Label = $label; Executable = $executable }
}

function Get-PiecesDataRoot {
    param($Platform)
    if ($env:PIECES_EXPORT_HOME) { return $env:PIECES_EXPORT_HOME }
    if ($Platform.Windows) { return Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Pieces Export' }
    if ($Platform.Os -eq 'darwin') { return Join-Path $env:HOME 'Library/Application Support/Pieces Export' }
    $xdg = $env:XDG_DATA_HOME
    if (!$xdg) { $xdg = Join-Path $env:HOME '.local/share' }
    return Join-Path $xdg 'pieces-export'
}

function Get-PiecesDocuments {
    param($Platform)
    if ($Platform.Windows) {
        # The shell folder follows OneDrive and other Documents redirection.
        $documents = [Environment]::GetFolderPath('MyDocuments')
        if ($documents) { return $documents }
        return Join-Path $env:USERPROFILE 'Documents'
    }
    return Join-Path $env:HOME 'Documents'
}

# Returns the newest saved session that still has its recovery folders.
function Find-PiecesSession {
    param([string]$Root)
    if (!(Test-Path -LiteralPath $Root -PathType Container)) { return $null }
    $found = $null
    foreach ($dir in @(Get-ChildItem -LiteralPath $Root -Directory | Sort-Object Name)) {
        if ((Test-Path -LiteralPath (Join-Path $dir.FullName 'work') -PathType Container) -and (Test-Path -LiteralPath (Join-Path $dir.FullName 'keys') -PathType Container)) {
            $found = $dir.FullName
        }
    }
    return $found
}

function Invoke-PiecesBootstrap {
    param(
        [string]$BaseUrl, [string]$Version, [string]$Output,
        [switch]$Resume, [switch]$DryRun, [switch]$NoOpen, [switch]$InstallOnly,
        [ValidateSet('Keep', 'Remove', 'Ask')][string]$Cleanup = 'Keep',
        [string[]]$ExportArgs = @()
    )
    $ErrorActionPreference = 'Stop'
    $release = Get-PiecesBuiltInRelease
    $run = @{ Staging = $null; Session = $null; OwnsSession = $false; Started = $false; Verified = $false; ToolDir = $null; Executable = $null; Code = 1 }
    try {
        $formatGiven = $false; $formatValue = ''; $recoveryGiven = $false; $sdkCache = $false; $dryFlag = $false; $previous = ''
        foreach ($argument in $ExportArgs) {
            if ($argument -match '^--?output(=|$)') { throw 'Use the installer -Output parameter instead of --output.' }
            if ($argument -match '^--?format=(.*)$') { $formatGiven = $true; $formatValue = $Matches[1] }
            elseif ($argument -match '^--?format$') { $formatGiven = $true }
            if ($argument -match '^--?(work|recovery-keys)(=|$)') { $recoveryGiven = $true }
            if ($argument -match '^--?sdk-cache(=|$)') { $sdkCache = $true }
            if ($argument -match '^--?dry-run(=true)?$') { $DryRun = $true; $dryFlag = $true }
            if ($previous -match '^--?format$') { $formatValue = $argument }
            $previous = $argument
        }
        if ($Resume -and $DryRun) { throw 'Use either -Resume or -DryRun, not both.' }

        if ($BaseUrl) {
            $uri = $null
            if (![Uri]::TryCreate($BaseUrl, [UriKind]::Absolute, [ref]$uri) -or $uri.Scheme -ne 'https' -or !$uri.Host -or $uri.UserInfo -or $uri.Query -or $uri.Fragment -or $BaseUrl -match '\s|/\.\.?/') {
                throw 'An HTTPS release base URL without credentials, query, or fragment is required.'
            }
            if ($Version -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$' -or $Version.Contains('..')) { throw 'A safe, pinned version is required with -BaseUrl.' }
        } else {
            if (!$Version) { $Version = $release.Version }
            if ($Version -ne $release.Version) { throw "Version $Version is not available from this installer, which provides $($release.Version)." }
        }

        $platform = Get-PiecesPlatform
        $dataRoot = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath((Get-PiecesDataRoot $platform))
        if ($Resume) {
            $run.Session = Find-PiecesSession (Join-Path $dataRoot 'recovery')
            if (!$run.Session) { throw 'No unfinished export was found to resume.' }
        }
        if (!$InstallOnly -and !$DryRun) {
            if (!$Output) { $Output = Join-Path (Join-Path (Get-PiecesDocuments $platform) 'Pieces-Exports') (Get-Date -Format 'yyyy-MM-dd_HH-mm-ss') }
            $Output = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Output)
            if ((Test-Path -LiteralPath $Output) -or (Test-Path -LiteralPath "$Output.partial")) { throw "The export folder already exists: $Output. Choose a new -Output folder." }
        }

        $toolRoot = Join-Path $dataRoot 'tool'
        try { $null = [IO.Directory]::CreateDirectory($toolRoot) } catch { throw "Could not create the tool folder: $toolRoot" }
        $run.Staging = Join-Path $toolRoot ('.download.' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
        $null = [IO.Directory]::CreateDirectory($run.Staging)

        Write-Host "Pieces Export $Version for $($platform.Label)"
        if ($BaseUrl) {
            $archiveName = "pieces-export_${Version}_$($platform.Os)_$($platform.Arch).zip"
            $releaseUrl = $BaseUrl.TrimEnd('/') + '/' + $Version
            $checksums = Join-Path $run.Staging 'SHA256SUMS.txt'
            Write-Host 'Downloading the export tool...'
            Get-PiecesReleaseFile "$releaseUrl/SHA256SUMS.txt" $checksums (1MB)
            $matching = @(Get-Content -LiteralPath $checksums | Where-Object { $_ -match ('^[a-fA-F0-9]{64}\s+' + [Regex]::Escape($archiveName) + '$') })
            if ($matching.Count -ne 1) { throw 'Release checksum is missing or ambiguous.' }
            $expected = ($matching[0] -split '\s+')[0]
            $archivePath = Join-Path $run.Staging $archiveName
            Get-PiecesReleaseFile "$releaseUrl/$archiveName" $archivePath (256MB)
        } else {
            $file = $release.Files["$($platform.Os)_$($platform.Arch)"]
            if (!$file) { throw "There is no download for $($platform.Label) yet." }
            $expected = $file.Sha256
            $archivePath = Join-Path $run.Staging $file.Name
            Write-Host 'Downloading the export tool from Google Drive...'
            Get-PiecesReleaseFile ('https://drive.usercontent.google.com/download?id=' + $file.Id + '&export=download&confirm=t') $archivePath (256MB)
        }
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash
        if ($actual -ne $expected) {
            $message = 'The download does not match its expected SHA-256, so nothing was run.'
            if (!$BaseUrl) { $message += " Google Drive may be limiting downloads. Wait a few minutes and try again, or download it from $($release.Folder)" }
            throw $message
        }
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $extract = Join-Path $run.Staging 'tool'
        $null = [IO.Directory]::CreateDirectory($extract)
        $zip = [IO.Compression.ZipFile]::OpenRead($archivePath)
        try {
            $allowed = @($platform.Executable, 'LICENSE.txt', 'README.md', 'THIRD_PARTY_NOTICES.txt')
            $names = @($zip.Entries | ForEach-Object { $_.FullName })
            if ($names.Count -ne $allowed.Count -or @(Compare-Object ($names | Sort-Object) ($allowed | Sort-Object) -CaseSensitive).Count -ne 0) { throw 'Unexpected files in the download, so nothing was run.' }
            foreach ($entry in $zip.Entries) {
                if ($entry.Length -gt 256MB) { throw 'Release entry exceeds the size limit.' }
                # Copy bytes into newly created regular files; never extract ZIP
                # paths, symlinks, or other archive-supplied filesystem metadata.
                $target = [IO.File]::Open((Join-Path $extract $entry.FullName), [IO.FileMode]::CreateNew)
                $source = $entry.Open()
                try { $source.CopyTo($target) } finally { $source.Dispose(); $target.Dispose() }
            }
        } finally { $zip.Dispose() }
        if (!$platform.Windows) { & chmod 700 (Join-Path $extract $platform.Executable); if ($LASTEXITCODE -ne 0) { throw 'Could not make the tool executable.' } }
        $run.ToolDir = Join-Path $toolRoot $Version
        if (Test-Path -LiteralPath $run.ToolDir) { Remove-Item -LiteralPath $run.ToolDir -Recurse -Force }
        Move-Item -LiteralPath $extract -Destination $run.ToolDir
        $run.Executable = Join-Path $run.ToolDir $platform.Executable
        $run.Verified = $true
        Write-Host 'Verified the download (SHA-256 matches).'
        if ($InstallOnly) {
            Write-Host "The export tool is ready:`n  $($run.Executable)"
            $run.Code = 0
            return 0
        }

        if ($Resume) {
            $arguments = @('resume', '--work', (Join-Path $run.Session 'work'), '--recovery-keys', (Join-Path $run.Session 'keys'), '--output', $Output)
            Write-Host "Resuming the export saved in:`n  $($run.Session)"
        } elseif ($DryRun) {
            $arguments = @('export')
            if (!$formatGiven) { $arguments += @('--format', 'markdown') }
            if (!$dryFlag) { $arguments += '--dry-run' }
        } else {
            $arguments = @('export', '--output', $Output)
            if (!$formatGiven) { $arguments += @('--format', 'markdown'); $formatValue = 'markdown' }
            # The CLI can resume Markdown exports without SDK caches. Keep its
            # private recovery folders outside Documents so they are not synced.
            if (!$recoveryGiven -and $formatValue -eq 'markdown' -and !$sdkCache) {
                $sessionRoot = Join-Path $dataRoot 'recovery'
                $run.Session = Join-Path $sessionRoot ((Get-Date -Format 'yyyyMMdd-HHmmss') + '.' + [Guid]::NewGuid().ToString('N').Substring(0, 6))
                $null = [IO.Directory]::CreateDirectory($run.Session)
                $run.OwnsSession = $true
                [IO.File]::WriteAllText((Join-Path $run.Session 'session.txt'), "version=$Version`noutput=$Output`n")
                $arguments += @('--work', (Join-Path $run.Session 'work'), '--recovery-keys', (Join-Path $run.Session 'keys'))
            }
        }
        $arguments += $ExportArgs
        if (!$DryRun) { Write-Host "Your export will be saved to:`n  $Output`n" }
        $run.Started = $true
        $run.Code = Start-PiecesProcess -FilePath $run.Executable -Arguments $arguments
        return $run.Code
    } catch [System.Management.Automation.PipelineStoppedException] {
        $run.Code = 130
        return 130
    } catch {
        Write-Host ('Installer failed: ' + $_.Exception.Message) -ForegroundColor Red
        $run.Code = 1
        return 1
    } finally {
        if ($run.Staging -and (Test-Path -LiteralPath $run.Staging)) { Remove-Item -LiteralPath $run.Staging -Recurse -Force }
        if ($run.Started) {
            if ($DryRun) {
                Write-Host "`nDry run finished. Nothing was exported."
            } elseif (Test-Path -LiteralPath (Join-Path $Output 'manifest.json') -PathType Leaf) {
                if ($run.Session) { Remove-Item -LiteralPath $run.Session -Recurse -Force }
                Write-Host "`nYour export is ready:`n  $Output"
                if ($run.Code -eq 2) { Write-Host 'Some records were unavailable. coverage.md in that folder lists them.' }
                Write-Host 'Start with index.md. Obsidian or VS Code are good ways to browse it.'
                if (!$NoOpen -and [Environment]::UserInteractive -and ![Console]::IsOutputRedirected) {
                    try { Invoke-Item -LiteralPath $Output } catch { Write-Host "Open this folder to see your export: $Output" }
                }
            } elseif ($run.Session -and (Test-Path -LiteralPath (Join-Path $run.Session 'work') -PathType Container)) {
                Write-Host "`nThe export did not finish, but your progress is saved.`nTo continue where it stopped, run:"
                Write-Host "  & ([scriptblock]::Create((irm $($release.InstallerUrl)))) -Resume"
            } else {
                if ($run.OwnsSession -and $run.Session -and (Test-Path -LiteralPath $run.Session)) { Remove-Item -LiteralPath $run.Session -Recurse -Force }
                if ($run.Code -eq 0) { Write-Host "`nNo export was written." }
                else { Write-Host "`nThe export did not finish and nothing was saved yet. Fix the problem above, then run the same command again." }
            }
        }
        if ($run.Verified -and !$InstallOnly) {
            $remove = $Cleanup -eq 'Remove'
            if ($Cleanup -eq 'Ask') {
                if ([Console]::IsInputRedirected) {
                    Write-Host 'No interactive terminal; keeping the export tool.'
                } else {
                    $reply = Read-Host 'Remove the export tool? Your exports will stay. [y/N]'
                    $remove = $reply -match '^(y|yes)$'
                }
            }
            if ($remove) {
                Remove-Item -LiteralPath $run.ToolDir -Recurse -Force
                Write-Host 'Removed the export tool. Your exports were not touched.'
            } else {
                Write-Host "`nThe export tool is kept at:`n  $($run.Executable)"
            }
        }
    }
}

# Dot sourcing loads the functions for tests without downloading or running
# anything. A one-line install runs through Invoke-Expression in the user's own
# session, where exit would close their window, so only a script file exits.
if ($MyInvocation.InvocationName -ne '.') {
    $piecesExportExit = Invoke-PiecesBootstrap -BaseUrl $BaseUrl -Version $Version -Output $Output -Resume:$Resume -DryRun:$DryRun -NoOpen:$NoOpen -InstallOnly:$InstallOnly -Cleanup $Cleanup -ExportArgs $ExportArgs
    if ($PSCommandPath) { exit $piecesExportExit }
    $global:LASTEXITCODE = $piecesExportExit
    Remove-Variable -Name piecesExportExit -ErrorAction SilentlyContinue
}
