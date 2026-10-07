param([string]$Bundle = $PSScriptRoot)

# Maintainer acceptance bundle: compiled synthetic tests only. Never point this
# at an actual user's archive, Pieces OS, recovery workspace or keys.
$ErrorActionPreference = 'Stop'
$Bundle = (Resolve-Path -LiteralPath $Bundle).Path
$config = Get-Content -LiteralPath (Join-Path $Bundle 'bundle.json') -Raw | ConvertFrom-Json
$nativeArch = $env:PROCESSOR_ARCHITECTURE
if ($env:PROCESSOR_ARCHITEW6432) { $nativeArch = $env:PROCESSOR_ARCHITEW6432 }
$expectedArch = @{ amd64 = 'AMD64'; arm64 = 'ARM64' }[$config.architecture]
if (!$expectedArch -or $nativeArch -ne $expectedArch) {
    throw "Wrong native Windows architecture: expected $expectedArch, found $nativeArch"
}

$logs = Join-Path $Bundle 'results'
New-Item -ItemType Directory -Path $logs | Out-Null
foreach ($item in $config.files) {
    $actual = (Get-FileHash -LiteralPath (Join-Path $Bundle $item.path) -Algorithm SHA256).Hash
    if ($actual -ne $item.sha256) { throw "Bundle checksum mismatch: $($item.path)" }
}

$env:ZONEINFO = Join-Path $Bundle 'zoneinfo.zip'
$env:PIECES_EXPORT_TEST_BINARY = Join-Path $Bundle 'release\pieces-export.exe'
$env:PIECES_EXPORT_TEST_RELEASE = Join-Path $Bundle 'dist'
$env:PIECES_EXPORT_TEST_VERSION = $config.version
$env:PIECES_EXPORT_TEST_POWERSHELL = 'powershell.exe'
$summary = [ordered]@{
    architecture = $config.architecture
    source_commit = $config.source_commit
    version = $config.version
    windows = [Environment]::OSVersion.VersionString
    identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    started = [DateTime]::UtcNow.ToString('o')
    checks = @()
    limitations = @('Synthetic loopback fixtures only; no installed Pieces OS or recipient download trial.',
        'Compiled tests are not race-instrumented.',
        'PowerShell installer tests substitute local-file transport.',
        'Generic installer fixture tests requiring a Go compiler are not included.')
}
$failed = $false

function Invoke-TestSuite([string]$Name, [string]$Folder, [string]$Pattern) {
    Write-Output "Running $Name"
    $working = Join-Path $Bundle $Folder
    $stdout = Join-Path $logs "$Name.log"
    $stderr = Join-Path $logs "$Name.stderr.log"
    # Use the process result directly: pipelines and PowerShell 5.1's handling
    # of native stderr must not turn a failed test into an apparent pass.
    $process = Start-Process -FilePath (Join-Path $working 'acceptance.test.exe') `
        -WorkingDirectory $working -ArgumentList @('-test.v', '-test.timeout=25m', "-test.run=$Pattern") `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr -NoNewWindow -Wait -PassThru
    $code = $process.ExitCode
    Get-Content -LiteralPath $stdout
    Get-Content -LiteralPath $stderr
    $script:summary.checks += [ordered]@{ name = $Name; exit_code = $code }
    if ($code -ne 0) { $script:failed = $true }
    $script:summary | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $logs 'acceptance.json') -Encoding UTF8
}

foreach ($name in @('recovery', 'lifecycle', 'cli', 'exporter')) {
    Invoke-TestSuite $name "internal\$name" '.'
}

if (Get-Command pwsh -ErrorAction SilentlyContinue) {
    $env:PIECES_EXPORT_TEST_POWERSHELL = 'pwsh'
    Invoke-TestSuite 'packaged-installer-pwsh' 'internal\exporter' '^TestPackagedPowerShellInstaller$'
} else {
    $summary.limitations += 'PowerShell 7 is not installed; only Windows PowerShell 5.1 was tested.'
}
$summary['finished'] = [DateTime]::UtcNow.ToString('o')
$summary | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $logs 'acceptance.json') -Encoding UTF8
Write-Output "Results: $logs"
if ($failed) { exit 1 }
exit 0
