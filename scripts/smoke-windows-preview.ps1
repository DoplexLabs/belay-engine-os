#Requires -Version 5.1
<#
.SYNOPSIS
Disposable smoke test for a Belay Local Windows engineering-preview zip.

.DESCRIPTION
Verifies archive layout, internal SHA-256 sums, the pinned Numbat version
marker, Belay help output, and a read-only first run with disposable user and
Belay homes. It never touches the real user profile, installs hooks, opens the
Local database, or opens a browser.

.PARAMETER Archive
Path to belay-local-developer-alpha-v<version>-windows-<arch>.zip

.PARAMETER ArchiveOnly
Check archive layout, internal SHA-256 sums, and build metadata only. Skips
every step that executes a packaged Windows binary, so the packaging half of
this smoke test can also run under pwsh on Linux and macOS.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Archive,
    [switch]$ArchiveOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$NumbatVersionMarker = 'b5172bb8bb8f'

function Fail([string]$Message) {
    Write-Error "smoke-windows-preview: $Message"
    exit 1
}

if (-not (Test-Path -LiteralPath $Archive -PathType Leaf)) {
    Fail "archive not found: $Archive"
}
$Archive = (Resolve-Path -LiteralPath $Archive).Path

$smokeRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("belay-preview-smoke-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $smokeRoot | Out-Null
try {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($Archive)
    try {
        $entries = @($zip.Entries | ForEach-Object { $_.FullName })
    } finally {
        $zip.Dispose()
    }
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
    $packageName = $topLevels[0]

    $required = @(
        "$packageName/bin/belay.exe",
        "$packageName/bin/numbat.exe",
        "$packageName/LICENSE",
        "$packageName/licenses/numbat/LICENSE",
        "$packageName/licenses/numbat/THIRD_PARTY_LICENSES.txt",
        "$packageName/README.md",
        "$packageName/llms.txt",
        "$packageName/docs/developer-alpha.md",
        "$packageName/docs/clean-machine-alpha-qa.md",
        "$packageName/docs/windows-port.md",
        "$packageName/BUILD-INFO.txt",
        "$packageName/SHA256SUMS"
    )
    foreach ($item in $required) {
        if ($entries -notcontains $item) {
            Fail "archive is missing $item"
        }
    }

    [System.IO.Compression.ZipFile]::ExtractToDirectory($Archive, $smokeRoot)
    $packageRoot = Join-Path $smokeRoot $packageName

    # Verify every internal checksum exactly as the macOS smoke test does.
    $sums = @(Get-Content -LiteralPath (Join-Path $packageRoot 'SHA256SUMS'))
    if ($sums.Count -lt $required.Count - 1) {
        Fail 'SHA256SUMS is incomplete'
    }
    foreach ($line in $sums) {
        if ($line -notmatch '^([0-9a-f]{64})\s+\*?(.+)$') {
            Fail "malformed SHA256SUMS line: $line"
        }
        $expected = $Matches[1]
        $relative = $Matches[2] -replace '/', [System.IO.Path]::DirectorySeparatorChar
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $packageRoot $relative)).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            Fail "checksum mismatch for $relative"
        }
    }

    $buildInfo = @(Get-Content -LiteralPath (Join-Path $packageRoot 'BUILD-INFO.txt'))
    if (-not ($buildInfo -match '^target=windows/')) {
        Fail 'BUILD-INFO.txt does not describe a Windows target'
    }

    if ($ArchiveOnly) {
        Write-Output "windows-preview archive check passed: $Archive"
        return
    }

    $belay = Join-Path $packageRoot 'bin\belay.exe'
    $numbat = Join-Path $packageRoot 'bin\numbat.exe'

    $numbatVersion = & $numbat version
    if ($LASTEXITCODE -ne 0 -or ($numbatVersion -join "`n") -notmatch [regex]::Escape($NumbatVersionMarker)) {
        Fail 'packaged Numbat version marker mismatch'
    }
    $help = & $belay help
    if ($LASTEXITCODE -ne 0 -or ($help -join "`n") -notmatch 'quickstart') {
        Fail 'packaged Belay does not advertise quickstart'
    }

    # Read-only inventory with disposable homes. USERPROFILE redirection keeps
    # Numbat discovery away from the real profile; BELAY_HOME is explicit.
    $smokeUserHome = Join-Path $smokeRoot 'user-home'
    $smokeHome = Join-Path $smokeRoot 'belay-home'
    New-Item -ItemType Directory -Path $smokeUserHome | Out-Null
    $savedProfile = $env:USERPROFILE
    $savedHome = $env:HOME
    try {
        $env:USERPROFILE = $smokeUserHome
        $env:HOME = $smokeUserHome
        & $belay agents --home $smokeHome | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Fail 'packaged Belay read-only inventory failed'
        }
    } finally {
        $env:USERPROFILE = $savedProfile
        if ($null -eq $savedHome) { Remove-Item Env:HOME -ErrorAction SilentlyContinue } else { $env:HOME = $savedHome }
    }

    $config = Join-Path $smokeHome 'config.json'
    if (-not (Test-Path -LiteralPath $config -PathType Leaf)) {
        Fail 'safe first-run config was not created'
    }
    $configText = Get-Content -LiteralPath $config -Raw
    $numbatSha = (Get-FileHash -Algorithm SHA256 -LiteralPath $numbat).Hash.ToLowerInvariant()
    if ($configText -notmatch [regex]::Escape("`"numbat_sha256`": `"$numbatSha`"")) {
        Fail 'safe first-run config did not materialize the embedded checksum'
    }
    if ($configText -notmatch [regex]::Escape("`"numbat_version_marker`": `"$NumbatVersionMarker`"")) {
        Fail 'safe first-run config did not materialize the embedded version marker'
    }
    $materialized = @(Get-ChildItem -LiteralPath (Join-Path $smokeHome 'bin') -File -Filter 'numbat-*.exe')
    if ($materialized.Count -ne 1) {
        Fail 'safe first-run did not materialize exactly one pinned Numbat binary'
    }
    $materializedSha = (Get-FileHash -Algorithm SHA256 -LiteralPath $materialized[0].FullName).Hash.ToLowerInvariant()
    if ($materializedSha -ne $numbatSha) {
        Fail 'materialized Numbat checksum differs from packaged sibling'
    }
    foreach ($spool in @('live\codex.ndjson', 'live\claude.ndjson')) {
        if (Test-Path -LiteralPath (Join-Path $smokeHome $spool)) {
            Fail 'smoke test unexpectedly created live-hook spool files'
        }
    }
    if (@(Get-ChildItem -LiteralPath $smokeUserHome -Force).Count -ne 0) {
        Fail 'Numbat inventory unexpectedly wrote to the disposable user home'
    }

    Write-Output "windows-preview smoke test passed: $Archive"
} finally {
    Remove-Item -LiteralPath $smokeRoot -Recurse -Force -ErrorAction SilentlyContinue
}
