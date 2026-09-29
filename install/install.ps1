# Pieces Export bootstrap. Application binaries are proprietary; see LICENSE.txt.
# Supports Windows PowerShell 5.1 and PowerShell 7; no administrator rights.
[CmdletBinding()]
param(
    [string]$BaseUrl,
    [string]$Version,
    [string]$Output,
    [ValidateSet('Ask', 'Keep', 'Remove')][string]$Cleanup = 'Ask',
    [switch]$InstallOnly,
    [string[]]$ExportArgs = @()
)

function Get-PiecesReleaseFile {
    param([string]$Url, [string]$Destination, [long]$MaxBytes)
    Add-Type -AssemblyName System.Net.Http
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
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

function Invoke-PiecesBootstrap {
    param(
        [string]$BaseUrl, [string]$Version, [string]$Output,
        [ValidateSet('Ask', 'Keep', 'Remove')][string]$Cleanup = 'Ask',
        [switch]$InstallOnly, [string[]]$ExportArgs = @()
    )
    $ErrorActionPreference = 'Stop'
    $installDir = $null
    $verified = $false
    try {
        $uri = $null
        if (![Uri]::TryCreate($BaseUrl, [UriKind]::Absolute, [ref]$uri) -or $uri.Scheme -ne 'https' -or !$uri.Host -or $uri.UserInfo -or $uri.Query -or $uri.Fragment -or $BaseUrl -match '\s|/\.\.?/') {
            throw 'An HTTPS release base URL without credentials, query, or fragment is required.'
        }
        if ($Version -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$' -or $Version.Contains('..')) { throw 'A safe, pinned version is required.' }
        foreach ($argument in $ExportArgs) { if ($argument -eq '--output' -or $argument.StartsWith('--output=')) { throw 'Use the installer -Output parameter.' } }
        $windowsHost = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
        if ($windowsHost) {
            $osName = 'windows'
            $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
            $executableName = 'pieces-export.exe'
        } else {
            $kernel = (& uname -s).Trim()
            $osName = switch ($kernel) { 'Darwin' { 'darwin' } 'Linux' { 'linux' } default { throw 'Unsupported operating system.' } }
            $cpu = (& uname -m).Trim()
            $executableName = 'pieces-export'
        }
        $arch = switch -Regex ($cpu) { '^(ARM64|aarch64)$' { 'arm64'; break } '^(AMD64|x86_64)$' { 'amd64'; break } default { throw 'Unsupported processor architecture.' } }
        if (!$Output) {
            $documents = [Environment]::GetFolderPath([Environment+SpecialFolder]::MyDocuments)
            if (!$documents) { $documents = Join-Path $HOME 'Documents' }
            $Output = Join-Path (Join-Path $documents 'Pieces-Exports') (Get-Date -Format 'yyyyMMdd-HHmmss')
        }
        $Output = [IO.Path]::GetFullPath($Output)
        if ((Test-Path -LiteralPath $Output) -or (Test-Path -LiteralPath "$Output.partial")) { throw 'Export destination already exists; choose a new directory.' }
        $installDir = Join-Path ([IO.Path]::GetTempPath()) ('pieces-export.' + [Guid]::NewGuid().ToString('N'))
        $null = New-Item -ItemType Directory -Path $installDir
        $archiveName = "pieces-export_${Version}_${osName}_${arch}.zip"
        $releaseUrl = $BaseUrl.TrimEnd('/') + '/' + $Version
        $checksums = Join-Path $installDir 'SHA256SUMS.txt'
        $archivePath = Join-Path $installDir $archiveName
        Write-Host "Downloading Pieces Export $Version for $osName/$arch..."
        Get-PiecesReleaseFile "$releaseUrl/SHA256SUMS.txt" $checksums (1MB)
        $matching = @(Get-Content -LiteralPath $checksums | Where-Object { $_ -match ('^[a-fA-F0-9]{64}\s+' + [Regex]::Escape($archiveName) + '$') })
        if ($matching.Count -ne 1) { throw 'Release checksum is missing or ambiguous.' }
        $expected = ($matching[0] -split '\s+')[0]
        Get-PiecesReleaseFile "$releaseUrl/$archiveName" $archivePath (256MB)
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash
        if ($actual -ne $expected) { throw 'Download checksum mismatch; nothing was executed.' }
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $archive = [IO.Compression.ZipFile]::OpenRead($archivePath)
        try {
            $allowed = @($executableName, 'LICENSE.txt', 'README.md', 'THIRD_PARTY_NOTICES.txt')
            $names = @($archive.Entries | ForEach-Object { $_.FullName })
            if ($names.Count -ne $allowed.Count -or @(Compare-Object ($names | Sort-Object) ($allowed | Sort-Object) -CaseSensitive).Count -ne 0) { throw 'Unexpected files in release archive.' }
            foreach ($entry in $archive.Entries) {
                if ($entry.Length -gt 256MB) { throw 'Release entry exceeds the size limit.' }
                # Copy bytes into newly created regular files; never extract ZIP
                # paths, symlinks, or other archive-supplied filesystem metadata.
                $target = [IO.File]::Open((Join-Path $installDir $entry.FullName), [IO.FileMode]::CreateNew)
                $source = $entry.Open()
                try { $source.CopyTo($target) } finally { $source.Dispose(); $target.Dispose() }
            }
        } finally { $archive.Dispose() }
        $executable = Join-Path $installDir $executableName
        if (!$windowsHost) { & chmod 700 $executable; if ($LASTEXITCODE -ne 0) { throw 'Could not make the CLI executable.' } }
        $verified = $true
        Write-Host "Verified CLI: $executable"
        Write-Host "Export destination: $Output"
        if ($InstallOnly) { return 0 }
        $PSNativeCommandUseErrorActionPreference = $false
        $ErrorActionPreference = 'Continue'
        & $executable export --output $Output @ExportArgs | Out-Host
        $result = $LASTEXITCODE
        $ErrorActionPreference = 'Stop'
        return [int]$result
    } catch [System.Management.Automation.PipelineStoppedException] {
        return 130
    } catch {
        Write-Host ('Installer failed: ' + $_.Exception.Message) -ForegroundColor Red
        return 1
    } finally {
        if ($installDir -and (Test-Path -LiteralPath $installDir)) {
            $remove = !$verified -or (!$InstallOnly -and $Cleanup -eq 'Remove')
            if ($verified -and !$InstallOnly -and $Cleanup -eq 'Ask') {
                if ([Console]::IsInputRedirected) {
                    Write-Host 'No interactive terminal; keeping the CLI. Use -Cleanup Remove for unattended cleanup.'
                } else {
                    $reply = Read-Host 'Remove the downloaded CLI and installer files? Export files will stay. [Y/n]'
                    $remove = $reply -eq '' -or $reply -match '^(y|yes)$'
                }
            }
            if ($remove) {
                Remove-Item -LiteralPath $installDir -Recurse -Force
                Write-Host "Downloaded utility removed. Export destination: $Output"
            } else {
                Write-Host "CLI kept in: $installDir"
                Write-Host 'To uninstall, remove only that installation directory.'
            }
        }
    }
}

# Dot sourcing loads functions for tests without downloading or executing files.
if ($MyInvocation.InvocationName -ne '.') {
    exit (Invoke-PiecesBootstrap -BaseUrl $BaseUrl -Version $Version -Output $Output -Cleanup $Cleanup -InstallOnly:$InstallOnly -ExportArgs $ExportArgs)
}
