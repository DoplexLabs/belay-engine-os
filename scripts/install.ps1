#Requires -Version 5.1
<#
.SYNOPSIS
Install or update Belay Local for the current Windows user.

.DESCRIPTION
Windows counterpart of scripts/install.sh. Downloads (or accepts) the
engineering-preview zip, verifies its published checksum, every internal
SHA-256 sum, and its build metadata, then swaps <InstallRoot>\runtime
atomically and writes a Belay-owned belay.cmd shim into <BinDir>.

Encrypted Belay history under %USERPROFILE%\.belay is never touched.

Environment overrides: BELAY_VERSION, BELAY_INSTALL_ROOT, BELAY_BIN_DIR,
BELAY_ARCH, BELAY_DOWNLOAD_BASE_URL, BELAY_INSTALL_ALLOW_UNTRUSTED=1, and
BELAY_INSTALL_SKIP_EXEC=1, which skips every step that runs a packaged Windows
binary so scripts/install_windows_test.ps1 can drive this script on Linux and
macOS.
#>
[CmdletBinding()]
param(
    [string]$Version,
    [switch]$Quickstart,
    [switch]$AllowCodexMcpAdd,
    [switch]$Uninstall,
    [string]$Archive,
    [string]$Checksum,
    [string]$InstallRoot,
    [string]$BinDir,
    [switch]$AllowUntrusted,
    [switch]$Help
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 renders a progress bar per Invoke-WebRequest chunk,
# which dominates the runtime of a multi-megabyte download.
$ProgressPreference = 'SilentlyContinue'

$DefaultVersion = '0.0.1-alpha.11'
$Repository = 'DoplexLabs/belay'
$ShimMarker = 'rem belay-install-shim'

function Fail {
    param([string]$Message)
    [Console]::Error.WriteLine("belay-install: $Message")
    exit 1
}

function Show-Usage {
    $text = @'
usage: install.ps1 [options]

Install or update Belay for the current Windows user.

Options:
  -Version VERSION              Release version (default: 0.0.1-alpha.11)
  -Quickstart                   Start private Belay onboarding after install
  -AllowCodexMcpAdd             Pass the explicit Codex MCP opt-in to quickstart
  -Uninstall                    Remove the program, monitor hooks, and Belay MCP
  -Archive PATH                 Install a local archive instead of downloading
  -Checksum PATH                Checksum file for -Archive
  -InstallRoot PATH             Program root (default: %LOCALAPPDATA%\Belay)
  -BinDir PATH                  Command directory (default: %LOCALAPPDATA%\Belay\bin)
  -AllowUntrusted               Accept the unsigned alpha (required until Belay is
                                Authenticode-signed; the getbelay.vercel.app
                                bootstrap passes it for you)
  -Help                         Show this help

Uninstall preserves encrypted Belay history under %USERPROFILE%\.belay.
'@
    Write-Output $text
}

function Get-EnvValue {
    param([string]$Name)
    $value = [Environment]::GetEnvironmentVariable($Name)
    if ($null -eq $value) { return '' }
    return $value
}

function Test-OnWindows {
    if ($PSVersionTable.PSVersion.Major -lt 6) { return $true }
    return [bool]$IsWindows
}

function Get-NormalizedPath {
    param([string]$Path, [string]$Label)
    if ($Path -eq '') { Fail "$Label must not be empty" }
    if (-not [System.IO.Path]::IsPathRooted($Path)) {
        Fail "$Label must be an absolute path: $Path"
    }
    $full = ''
    try {
        $full = [System.IO.Path]::GetFullPath($Path)
    } catch {
        Fail "$Label is not a usable path: $Path"
    }
    if ($full.Length -gt 3) { $full = $full.TrimEnd('\', '/') }
    return $full
}

function Get-BroadRoots {
    $roots = New-Object System.Collections.Generic.List[string]
    foreach ($literal in @(
            'C:\', 'C:\Windows', 'C:\Program Files', 'C:\Program Files (x86)',
            'C:\Users', 'C:\ProgramData',
            '/', '/usr', '/etc', '/home', '/var', '/opt')) {
        $roots.Add($literal) | Out-Null
    }
    foreach ($name in @(
            'USERPROFILE', 'SystemRoot', 'windir', 'SystemDrive', 'ProgramFiles',
            'ProgramFiles(x86)', 'ProgramData', 'APPDATA', 'LOCALAPPDATA', 'PUBLIC')) {
        $value = Get-EnvValue $name
        if ($value -ne '') { $roots.Add($value) | Out-Null }
    }
    return $roots
}

function Assert-NotBroadRoot {
    param([string]$Path, [string]$Label)
    if ($Path -match '^[A-Za-z]:\\?$') { Fail "refusing broad $Label`: $Path" }
    foreach ($root in (Get-BroadRoots)) {
        $normalized = $root
        if ($normalized.Length -gt 3) { $normalized = $normalized.TrimEnd('\', '/') }
        if ($Path -ieq $normalized) { Fail "refusing broad $Label`: $Path" }
    }
}

function Get-TargetArchitecture {
    $override = Get-EnvValue 'BELAY_ARCH'
    if ($override -ne '') { return $override }
    $raw = Get-EnvValue 'PROCESSOR_ARCHITEW6432'
    if ($raw -eq '') { $raw = Get-EnvValue 'PROCESSOR_ARCHITECTURE' }
    if ($raw -ne '') {
        switch ($raw.ToUpperInvariant()) {
            'AMD64' { return 'amd64' }
            'ARM64' { return 'arm64' }
            default { Fail "unsupported processor architecture: $raw" }
        }
    }
    # Non-Windows hosts (script tests) expose no PROCESSOR_ARCHITECTURE.
    $osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    switch ($osArch.ToUpperInvariant()) {
        'X64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { Fail "unsupported processor architecture: $osArch" }
    }
}

function Get-MetadataValue {
    param([string]$Path, [string]$Key)
    $found = New-Object System.Collections.Generic.List[string]
    foreach ($line in @(Get-Content -LiteralPath $Path)) {
        $index = $line.IndexOf('=')
        if ($index -lt 1) { continue }
        if ($line.Substring(0, $index) -ceq $Key) {
            $found.Add($line.Substring($index + 1).Trim()) | Out-Null
        }
    }
    if ($found.Count -ne 1) {
        Fail "release metadata does not define $Key exactly once"
    }
    return $found[0]
}

function Get-ChecksumFromFile {
    param([string]$Path, [string]$ExpectedName)
    $lines = @(Get-Content -LiteralPath $Path | Where-Object { $_.Trim() -ne '' })
    if ($lines.Count -ne 1) { Fail "checksum file must contain exactly one entry: $Path" }
    if ($lines[0] -notmatch '^([0-9a-fA-F]{64})\s+\*?(.+)$') {
        Fail "malformed checksum file: $Path"
    }
    $recorded = $Matches[2].Trim()
    $recordedName = ($recorded -replace '\\', '/').Split('/')[-1]
    if ($recordedName -ine $ExpectedName) {
        Fail "checksum file records a different archive: $recordedName"
    }
    return $Matches[1].ToLowerInvariant()
}

function Get-Sha256 {
    param([string]$Path)
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Test-BelayShim {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return $false }
    if (Test-Path -LiteralPath $Path -PathType Container) { return $false }
    $content = ''
    try {
        $content = [System.IO.File]::ReadAllText($Path)
    } catch {
        return $false
    }
    return $content -like "*$ShimMarker*"
}

function Remove-TreeQuietly {
    param([string]$Path)
    if ($Path -eq '') { return }
    if (Test-Path -LiteralPath $Path) {
        Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction SilentlyContinue
    }
}

if ($Help) {
    Show-Usage
    exit 0
}

if (-not $PSBoundParameters.ContainsKey('Version') -or $Version -eq '') {
    $Version = Get-EnvValue 'BELAY_VERSION'
    if ($Version -eq '') { $Version = $DefaultVersion }
}
if ($Version -notmatch '^[0-9A-Za-z][0-9A-Za-z._-]*$') {
    Fail 'version may contain only letters, numbers, dots, underscores, and hyphens'
}

if ($InstallRoot -eq '') { $InstallRoot = Get-EnvValue 'BELAY_INSTALL_ROOT' }
if ($InstallRoot -eq '') {
    $localAppData = Get-EnvValue 'LOCALAPPDATA'
    if ($localAppData -eq '') { Fail 'LOCALAPPDATA is not set; pass -InstallRoot' }
    $InstallRoot = Join-Path $localAppData 'Belay'
}
if ($BinDir -eq '') { $BinDir = Get-EnvValue 'BELAY_BIN_DIR' }
if ($BinDir -eq '') {
    $localAppData = Get-EnvValue 'LOCALAPPDATA'
    if ($localAppData -eq '') { Fail 'LOCALAPPDATA is not set; pass -BinDir' }
    $BinDir = Join-Path (Join-Path $localAppData 'Belay') 'bin'
}

$InstallRoot = Get-NormalizedPath -Path $InstallRoot -Label 'install root'
$BinDir = Get-NormalizedPath -Path $BinDir -Label 'command directory'
Assert-NotBroadRoot -Path $InstallRoot -Label 'install root'
Assert-NotBroadRoot -Path $BinDir -Label 'command directory'

$allowUntrusted = $AllowUntrusted.IsPresent
if ((Get-EnvValue 'BELAY_INSTALL_ALLOW_UNTRUSTED') -eq '1') { $allowUntrusted = $true }
$skipExec = (Get-EnvValue 'BELAY_INSTALL_SKIP_EXEC') -eq '1'
$onWindows = Test-OnWindows

$runtimeRoot = Join-Path $InstallRoot 'runtime'
$installedBelay = Join-Path (Join-Path $runtimeRoot 'bin') 'belay.exe'
$commandPath = Join-Path $BinDir 'belay.cmd'

if ($Uninstall) {
    if ((Test-Path -LiteralPath $installedBelay -PathType Leaf) -and -not $skipExec) {
        foreach ($arguments in @(@('mcp-config', 'uninstall'), @('hooks', 'uninstall'))) {
            try {
                & $installedBelay @arguments | Out-Null
            } catch {
                # Best effort, exactly like the macOS installer.
            }
        }
    }
    if (Test-Path -LiteralPath $commandPath) {
        if (Test-BelayShim -Path $commandPath) {
            Remove-Item -LiteralPath $commandPath -Force
        } else {
            Fail "refusing to remove foreign command: $commandPath"
        }
    }
    if (Test-Path -LiteralPath $runtimeRoot) {
        Remove-Item -LiteralPath $runtimeRoot -Recurse -Force
    }
    if (Test-Path -LiteralPath $InstallRoot -PathType Container) {
        if (@(Get-ChildItem -LiteralPath $InstallRoot -Force).Count -eq 0) {
            Remove-Item -LiteralPath $InstallRoot -Force
        }
    }
    Write-Output 'Belay was uninstalled. Encrypted history under %USERPROFILE%\.belay was preserved.'
    exit 0
}

$architecture = Get-TargetArchitecture
if ($architecture -ne 'amd64' -and $architecture -ne 'arm64') {
    Fail "unsupported architecture: $architecture"
}

if ((Test-Path -LiteralPath $commandPath) -and -not (Test-BelayShim -Path $commandPath)) {
    Fail "refusing to replace existing command: $commandPath"
}

if ($PSVersionTable.PSVersion.Major -lt 6) {
    try {
        [Net.ServicePointManager]::SecurityProtocol =
            [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch {
        # Older hosts without TLS 1.2 support fail later, with a clearer message.
    }
}

$archiveName = "belay-local-developer-alpha-v$Version-windows-$architecture.zip"
$installTmp = Join-Path ([System.IO.Path]::GetTempPath()) ("belay-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $installTmp | Out-Null

$archivePath = Join-Path $installTmp $archiveName
$checksumPath = "$archivePath.sha256"
$stageRoot = ''
$backupRoot = ''
$runtimeSwapped = $false
$installComplete = $false
$fatal = $null

try {
    if ($Archive -ne '') {
        if (-not (Test-Path -LiteralPath $Archive -PathType Leaf)) {
            Fail "archive not found: $Archive"
        }
        if ($Checksum -eq '') { $Checksum = "$Archive.sha256" }
        if (-not (Test-Path -LiteralPath $Checksum -PathType Leaf)) {
            Fail "checksum not found: $Checksum"
        }
        Copy-Item -LiteralPath $Archive -Destination $archivePath -Force
        Copy-Item -LiteralPath $Checksum -Destination $checksumPath -Force
    } else {
        $releaseBase = Get-EnvValue 'BELAY_DOWNLOAD_BASE_URL'
        if ($releaseBase -eq '') {
            $releaseBase = "https://github.com/$Repository/releases/download/v$Version"
        }
        $releaseBase = $releaseBase.TrimEnd('/')
        Write-Output "Downloading Belay $Version..."
        Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/$archiveName" -OutFile $archivePath
        Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/$archiveName.sha256" -OutFile $checksumPath
    }

    $expectedArchiveHash = Get-ChecksumFromFile -Path $checksumPath -ExpectedName $archiveName
    $actualArchiveHash = Get-Sha256 -Path $archivePath
    if ($actualArchiveHash -ne $expectedArchiveHash) {
        Fail "archive checksum mismatch for $archiveName"
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($archivePath)
    try {
        $entries = @($zip.Entries | ForEach-Object { $_.FullName })
    } finally {
        $zip.Dispose()
    }
    if ($entries.Count -eq 0) { Fail 'archive is empty' }
    foreach ($entry in $entries) {
        if ($entry -eq '' -or $entry.StartsWith('/') -or $entry.StartsWith('\') -or
            $entry -match '(^|[/\\])\.\.([/\\]|$)' -or $entry -match '^[A-Za-z]:') {
            Fail "unsafe archive path: $entry"
        }
    }
    $topLevels = @($entries | ForEach-Object { ($_ -split '/')[0] } | Sort-Object -Unique)
    if ($topLevels.Count -ne 1) {
        Fail 'archive must contain exactly one top-level directory'
    }

    $extractRoot = Join-Path $installTmp 'package'
    [System.IO.Compression.ZipFile]::ExtractToDirectory($archivePath, $extractRoot)
    $packageRoot = Join-Path $extractRoot $topLevels[0]

    $buildInfo = Join-Path $packageRoot 'BUILD-INFO.txt'
    $sumsPath = Join-Path $packageRoot 'SHA256SUMS'
    if (-not (Test-Path -LiteralPath $buildInfo -PathType Leaf) -or
        -not (Test-Path -LiteralPath $sumsPath -PathType Leaf)) {
        Fail 'archive is missing release metadata'
    }
    foreach ($relative in @('bin/belay.exe', 'bin/numbat.exe')) {
        $binary = Join-Path $packageRoot ($relative -replace '/', [System.IO.Path]::DirectorySeparatorChar)
        if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
            Fail 'archive does not contain Belay and Numbat binaries'
        }
    }

    $sums = @(Get-Content -LiteralPath $sumsPath | Where-Object { $_.Trim() -ne '' })
    if ($sums.Count -eq 0) { Fail 'SHA256SUMS is empty' }
    foreach ($line in $sums) {
        if ($line -notmatch '^([0-9a-fA-F]{64})\s+\*?(.+)$') {
            Fail "malformed SHA256SUMS line: $line"
        }
        $expected = $Matches[1].ToLowerInvariant()
        $relative = $Matches[2].Trim()
        if ($relative.StartsWith('/') -or $relative.StartsWith('\') -or
            $relative -match '(^|[/\\])\.\.([/\\]|$)' -or $relative -match '^[A-Za-z]:') {
            Fail "unsafe SHA256SUMS path: $relative"
        }
        $member = Join-Path $packageRoot ($relative -replace '/', [System.IO.Path]::DirectorySeparatorChar)
        if (-not (Test-Path -LiteralPath $member -PathType Leaf)) {
            Fail "archive is missing a checksummed file: $relative"
        }
        if ((Get-Sha256 -Path $member) -ne $expected) {
            Fail "checksum mismatch for $relative"
        }
    }

    if ((Get-MetadataValue -Path $buildInfo -Key 'version') -ne $Version) {
        Fail 'archive version does not match requested version'
    }
    if ((Get-MetadataValue -Path $buildInfo -Key 'target') -ne "windows/$architecture") {
        Fail "archive target is not windows/$architecture"
    }
    if ((Get-MetadataValue -Path $buildInfo -Key 'belay_dirty') -ne 'false') {
        Fail 'refusing a dirty build'
    }
    if (-not $allowUntrusted) {
        if ((Get-MetadataValue -Path $buildInfo -Key 'signed') -ne 'true') {
            Fail 'refusing an unsigned build'
        }
    }

    New-Item -ItemType Directory -Path $InstallRoot -Force | Out-Null
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null

    $stageRoot = Join-Path $InstallRoot (".runtime-stage." + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $stageRoot | Out-Null
    foreach ($item in @(Get-ChildItem -LiteralPath $packageRoot -Force)) {
        Copy-Item -LiteralPath $item.FullName -Destination $stageRoot -Recurse -Force
    }

    if (Test-Path -LiteralPath $runtimeRoot) {
        $backupRoot = Join-Path $InstallRoot (".runtime-backup." + [guid]::NewGuid().ToString('N'))
        if (Test-Path -LiteralPath $backupRoot) {
            Fail "stale install backup exists: $backupRoot"
        }
        Move-Item -LiteralPath $runtimeRoot -Destination $backupRoot
    }
    Move-Item -LiteralPath $stageRoot -Destination $runtimeRoot
    $stageRoot = ''
    $runtimeSwapped = $true

    $binParent = Split-Path -Parent $BinDir
    if ($null -ne $binParent -and $binParent -ine '' -and $binParent -ieq $InstallRoot) {
        $target = '"%~dp0..\runtime\bin\belay.exe" %*'
    } else {
        $target = '"' + $installedBelay + '" %*'
    }
    $shim = @('@echo off', $ShimMarker, $target) -join "`r`n"
    [System.IO.File]::WriteAllText($commandPath, $shim + "`r`n")

    if ($backupRoot -ne '') { Remove-TreeQuietly -Path $backupRoot }
    $backupRoot = ''
    $installComplete = $true
} catch {
    $fatal = $_
} finally {
    Remove-TreeQuietly -Path $installTmp
    if ($stageRoot -ne '') { Remove-TreeQuietly -Path $stageRoot }
    if (-not $installComplete -and $runtimeSwapped) {
        Remove-TreeQuietly -Path $runtimeRoot
        if ($backupRoot -ne '' -and (Test-Path -LiteralPath $backupRoot)) {
            Move-Item -LiteralPath $backupRoot -Destination $runtimeRoot
        }
    }
}
if ($null -ne $fatal) { Fail $fatal.Exception.Message }

if ($skipExec) {
    Write-Output "Installed Belay $Version at $commandPath (version check skipped)"
} else {
    $reported = & $commandPath version
    if ($LASTEXITCODE -ne 0) { Fail 'installed Belay did not report its version' }
    $reportedText = (@($reported) -join "`n")
    if ($reportedText -notmatch [regex]::Escape($Version)) {
        Fail 'installed Belay reported an unexpected version'
    }
    Write-Output "Installed $reportedText at $commandPath"
}

$pathAlreadyPresent = $false
foreach ($candidate in @((Get-EnvValue 'Path') -split ';')) {
    $trimmed = $candidate.Trim()
    if ($trimmed -eq '') { continue }
    if ($trimmed.Length -gt 3) { $trimmed = $trimmed.TrimEnd('\', '/') }
    if ($trimmed -ieq $BinDir) { $pathAlreadyPresent = $true }
}
if (-not $pathAlreadyPresent) {
    if ($onWindows) {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        if ($null -eq $userPath) { $userPath = '' }
        $userEntries = @($userPath -split ';' | ForEach-Object { $_.Trim() } |
                Where-Object { $_ -ne '' })
        $alreadyInUserPath = $false
        foreach ($entry in $userEntries) {
            $candidate = $entry
            if ($candidate.Length -gt 3) { $candidate = $candidate.TrimEnd('\', '/') }
            if ($candidate -ieq $BinDir) { $alreadyInUserPath = $true }
        }
        if (-not $alreadyInUserPath) {
            $updated = @($userEntries + $BinDir) -join ';'
            [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
        }
        $env:Path = ((Get-EnvValue 'Path').TrimEnd(';') + ';' + $BinDir)
        Write-Output "Added $BinDir to your user PATH. New terminals pick it up automatically."
    } else {
        Write-Output "On Windows this run would add $BinDir to your user PATH."
    }
}

if ($Quickstart) {
    Write-Output 'Starting private onboarding: Belay will install reversible monitor hooks,'
    Write-Output 'supported agent skills, and ownership-safe local MCP configuration.'
    if ($skipExec) { exit 0 }
    $quickstartArgs = @('quickstart')
    if ($AllowCodexMcpAdd) { $quickstartArgs += '--allow-codex-mcp-add' }
    & $commandPath @quickstartArgs
    exit $LASTEXITCODE
}

if ($pathAlreadyPresent) {
    Write-Output 'Next: belay quickstart'
} else {
    Write-Output "Next: $commandPath quickstart"
}
