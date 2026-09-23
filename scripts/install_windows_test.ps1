#Requires -Version 5.1
<#
.SYNOPSIS
Self-contained contract test for scripts/install.ps1.

.DESCRIPTION
Builds disposable engineering-preview zips in a temporary directory and drives
scripts/install.ps1 against them: install, upgrade, every refusal the macOS
installer makes, uninstall, and broad-root protection. Runs under Windows
PowerShell 5.1, and under pwsh on Linux and macOS, where the packaged stubs
cannot be executed and BELAY_INSTALL_SKIP_EXEC=1 is used instead.
#>
[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Failures = 0
$script:Version = '0.0.1-alpha.11'

function Write-Check {
    param([bool]$Condition, [string]$Message)
    if ($Condition) {
        Write-Output "ok   $Message"
    } else {
        Write-Output "FAIL $Message"
        $script:Failures = $script:Failures + 1
    }
}

function Test-OnWindows {
    if ($PSVersionTable.PSVersion.Major -lt 6) { return $true }
    return [bool]$IsWindows
}

function Get-Sha256 {
    param([string]$Path)
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

$onWindows = Test-OnWindows
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repositoryRoot = Split-Path -Parent $scriptDir
$installScript = Join-Path $scriptDir 'install.ps1'
if (-not (Test-Path -LiteralPath $installScript -PathType Leaf)) {
    throw "install-windows-test: install.ps1 not found at $installScript"
}

$hostPath = ([System.Diagnostics.Process]::GetCurrentProcess()).MainModule.FileName
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("belay-install-test-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null

# A real belay.exe is only usable when the test itself runs on Windows.
$script:RealBelay = ''
if ($onWindows -and $null -ne (Get-Command go -ErrorAction SilentlyContinue)) {
    $candidate = Join-Path $work 'belay.exe'
    $env:CGO_ENABLED = '0'
    & go build -trimpath -ldflags "-X main.buildVersion=$($script:Version)" -o $candidate (Join-Path $repositoryRoot 'cmd\belay')
    if ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        $script:RealBelay = $candidate
    }
}
if ($script:RealBelay -eq '') {
    Write-Output 'note: packaged stubs are not executable here; using BELAY_INSTALL_SKIP_EXEC=1'
    $env:BELAY_INSTALL_SKIP_EXEC = '1'
}
$env:BELAY_ARCH = 'amd64'
Remove-Item Env:BELAY_VERSION -ErrorAction SilentlyContinue
Remove-Item Env:BELAY_INSTALL_ROOT -ErrorAction SilentlyContinue
Remove-Item Env:BELAY_BIN_DIR -ErrorAction SilentlyContinue
Remove-Item Env:BELAY_INSTALL_ALLOW_UNTRUSTED -ErrorAction SilentlyContinue

function New-FakePackage {
    param(
        [string]$Name,
        [string]$PackageVersion = '',
        [string]$Dirty = 'false',
        [string]$Signed = 'true',
        [string]$Target = 'windows/amd64',
        [string]$Arch = 'amd64'
    )
    if ($PackageVersion -eq '') { $PackageVersion = $script:Version }
    $destination = Join-Path $work $Name
    $stage = Join-Path $destination 'stage'
    New-Item -ItemType Directory -Path $stage -Force | Out-Null
    $packageName = "belay-local-developer-alpha-v$PackageVersion-windows-$Arch"
    $root = Join-Path $stage $packageName
    New-Item -ItemType Directory -Path (Join-Path $root 'bin') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $root 'docs') -Force | Out-Null

    if ($script:RealBelay -ne '') {
        Copy-Item -LiteralPath $script:RealBelay -Destination (Join-Path (Join-Path $root 'bin') 'belay.exe')
    } else {
        [System.IO.File]::WriteAllText((Join-Path (Join-Path $root 'bin') 'belay.exe'), "stub belay $Name`n")
    }
    [System.IO.File]::WriteAllText((Join-Path (Join-Path $root 'bin') 'numbat.exe'), "stub numbat $Name`n")
    [System.IO.File]::WriteAllText((Join-Path $root 'LICENSE'), "MIT`n")
    [System.IO.File]::WriteAllText((Join-Path (Join-Path $root 'docs') 'windows-port.md'), "# windows port`n")

    $buildInfo = @(
        'Belay Local Developer Alpha',
        "version=$PackageVersion",
        "target=$Target",
        'belay_commit=0000000000000000000000000000000000000000',
        "belay_dirty=$Dirty",
        'numbat_commit=b5172bb8bb8f1d68edc4f3b9462de7e248dc5243',
        'numbat_version_marker=b5172bb8bb8f',
        'source_date_epoch=0',
        "signed=$Signed",
        'notarized=false'
    ) -join "`n"
    [System.IO.File]::WriteAllText((Join-Path $root 'BUILD-INFO.txt'), $buildInfo + "`n")

    $sums = New-Object System.Collections.Generic.List[string]
    $members = @(Get-ChildItem -LiteralPath $root -Recurse -Force |
            Where-Object { -not $_.PSIsContainer } |
            Where-Object { $_.Name -ne 'SHA256SUMS' })
    $relatives = @($members | ForEach-Object { $_.FullName.Substring($root.Length + 1) -replace '\\', '/' } |
            Sort-Object)
    foreach ($relative in $relatives) {
        $member = Join-Path $root ($relative -replace '/', [System.IO.Path]::DirectorySeparatorChar)
        $sums.Add((Get-Sha256 -Path $member) + '  ' + $relative) | Out-Null
    }
    [System.IO.File]::WriteAllText((Join-Path $root 'SHA256SUMS'), ($sums -join "`n") + "`n")

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = Join-Path $destination "$packageName.zip"
    if (Test-Path -LiteralPath $zip) { Remove-Item -LiteralPath $zip -Force }
    [System.IO.Compression.ZipFile]::CreateFromDirectory($stage, $zip)
    $companion = "$zip.sha256"
    [System.IO.File]::WriteAllText($companion, (Get-Sha256 -Path $zip) + '  ' + "$packageName.zip" + "`n")
    return $zip
}

function Invoke-Install {
    param([string[]]$Arguments, [hashtable]$Environment)
    $restore = @{}
    if ($null -ne $Environment) {
        foreach ($key in $Environment.Keys) {
            $restore[$key] = [Environment]::GetEnvironmentVariable($key)
            [Environment]::SetEnvironmentVariable($key, $Environment[$key])
        }
    }
    try {
        $stdoutPath = Join-Path $work ("stdout-" + [guid]::NewGuid().ToString('N') + '.txt')
        $stderrPath = Join-Path $work ("stderr-" + [guid]::NewGuid().ToString('N') + '.txt')
        $hostArgs = New-Object System.Collections.Generic.List[string]
        $hostArgs.Add('-NoProfile') | Out-Null
        $hostArgs.Add('-NoLogo') | Out-Null
        if ($onWindows) {
            $hostArgs.Add('-ExecutionPolicy') | Out-Null
            $hostArgs.Add('Bypass') | Out-Null
        }
        $hostArgs.Add('-File') | Out-Null
        $hostArgs.Add($installScript) | Out-Null
        foreach ($argument in $Arguments) { $hostArgs.Add($argument) | Out-Null }
        $process = Start-Process -FilePath $hostPath -ArgumentList $hostArgs.ToArray() `
            -NoNewWindow -Wait -PassThru `
            -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath
        $stdout = ''
        $stderr = ''
        if (Test-Path -LiteralPath $stdoutPath) { $stdout = [System.IO.File]::ReadAllText($stdoutPath) }
        if (Test-Path -LiteralPath $stderrPath) { $stderr = [System.IO.File]::ReadAllText($stderrPath) }
        return [pscustomobject]@{
            ExitCode = $process.ExitCode
            StdOut   = $stdout
            StdErr   = $stderr
        }
    } finally {
        foreach ($key in $restore.Keys) {
            [Environment]::SetEnvironmentVariable($key, $restore[$key])
        }
    }
}

function New-Roots {
    param([string]$Name)
    $root = Join-Path $work $Name
    $installRoot = Join-Path $root 'program'
    $binDir = Join-Path $installRoot 'bin'
    New-Item -ItemType Directory -Path $root -Force | Out-Null
    return [pscustomobject]@{ InstallRoot = $installRoot; BinDir = $binDir }
}

try {
    $goodZip = New-FakePackage -Name 'pkg-good'

    # 1. Fresh install from a local archive.
    $roots = New-Roots -Name 'case-install'
    $result = Invoke-Install -Arguments @(
        '-Archive', $goodZip,
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -eq 0) "install succeeds (exit $($result.ExitCode)) $($result.StdErr)"
    $runtimeBelay = Join-Path (Join-Path (Join-Path $roots.InstallRoot 'runtime') 'bin') 'belay.exe'
    $shim = Join-Path $roots.BinDir 'belay.cmd'
    Write-Check (Test-Path -LiteralPath $runtimeBelay -PathType Leaf) 'runtime holds bin/belay.exe'
    Write-Check (Test-Path -LiteralPath (Join-Path (Join-Path (Join-Path $roots.InstallRoot 'runtime') 'bin') 'numbat.exe') -PathType Leaf) 'runtime holds bin/numbat.exe'
    Write-Check (Test-Path -LiteralPath (Join-Path (Join-Path $roots.InstallRoot 'runtime') 'BUILD-INFO.txt') -PathType Leaf) 'runtime holds BUILD-INFO.txt'
    Write-Check (Test-Path -LiteralPath $shim -PathType Leaf) 'command shim was created'
    if (Test-Path -LiteralPath $shim -PathType Leaf) {
        $shimText = [System.IO.File]::ReadAllText($shim)
        Write-Check ($shimText -like '*rem belay-install-shim*') 'shim carries the Belay marker'
        Write-Check ($shimText -like '*%~dp0..\runtime\bin\belay.exe*') 'shim resolves belay.exe relative to itself'
    }

    # 2. Upgrade over an existing install leaves exactly one runtime.
    $result = Invoke-Install -Arguments @(
        '-Archive', $goodZip,
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -eq 0) "re-install succeeds (exit $($result.ExitCode)) $($result.StdErr)"
    $leftovers = @(Get-ChildItem -LiteralPath $roots.InstallRoot -Force |
            Where-Object { $_.Name -like '.runtime-*' })
    Write-Check ($leftovers.Count -eq 0) 'no staged or backup runtime directories remain'
    $runtimes = @(Get-ChildItem -LiteralPath $roots.InstallRoot -Force |
            Where-Object { $_.Name -like 'runtime*' })
    Write-Check ($runtimes.Count -eq 1) 'exactly one runtime directory remains'

    # 3. Uninstall removes the runtime and the shim, and preserves ~/.belay.
    $fakeBelayHome = Join-Path (Join-Path $work 'case-install') '.belay'
    New-Item -ItemType Directory -Path $fakeBelayHome -Force | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $fakeBelayHome 'belay.sqlite'), "encrypted`n")
    $result = Invoke-Install -Arguments @(
        '-Uninstall',
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -eq 0) "uninstall succeeds (exit $($result.ExitCode)) $($result.StdErr)"
    Write-Check (-not (Test-Path -LiteralPath (Join-Path $roots.InstallRoot 'runtime'))) 'uninstall removed the runtime'
    Write-Check (-not (Test-Path -LiteralPath $shim)) 'uninstall removed the command shim'
    Write-Check (Test-Path -LiteralPath (Join-Path $fakeBelayHome 'belay.sqlite') -PathType Leaf) 'uninstall preserved encrypted Belay history'
    Write-Check ($result.StdOut -like '*preserved*') 'uninstall reports preserved history'

    # 4. A tampered published checksum is refused.
    $badZip = New-FakePackage -Name 'pkg-checksum'
    $badCompanion = "$badZip.sha256"
    [System.IO.File]::WriteAllText($badCompanion,
        ('0' * 64) + '  ' + (Split-Path -Leaf $badZip) + "`n")
    $roots = New-Roots -Name 'case-checksum'
    $result = Invoke-Install -Arguments @(
        '-Archive', $badZip,
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -ne 0) 'checksum mismatch is refused'
    Write-Check ($result.StdErr -like '*belay-install:*') 'checksum refusal is prefixed'
    Write-Check (-not (Test-Path -LiteralPath (Join-Path $roots.InstallRoot 'runtime'))) 'checksum refusal installs nothing'

    # 5. A dirty build is refused, with or without -AllowUntrusted.
    $dirtyZip = New-FakePackage -Name 'pkg-dirty' -Dirty 'true'
    $roots = New-Roots -Name 'case-dirty'
    $result = Invoke-Install -Arguments @(
        '-Archive', $dirtyZip,
        '-AllowUntrusted',
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -ne 0) 'dirty build is refused'
    Write-Check ($result.StdErr -like '*dirty*') 'dirty refusal names the reason'

    # 6. An unsigned build needs the explicit development override.
    $unsignedZip = New-FakePackage -Name 'pkg-unsigned' -Signed 'false'
    $roots = New-Roots -Name 'case-unsigned'
    $result = Invoke-Install -Arguments @(
        '-Archive', $unsignedZip,
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -ne 0) 'unsigned build is refused by default'
    Write-Check ($result.StdErr -like '*unsigned*') 'unsigned refusal names the reason'
    $result = Invoke-Install -Arguments @(
        '-Archive', $unsignedZip,
        '-AllowUntrusted',
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -eq 0) "unsigned build installs with -AllowUntrusted (exit $($result.ExitCode)) $($result.StdErr)"
    Write-Check (Test-Path -LiteralPath (Join-Path $roots.BinDir 'belay.cmd') -PathType Leaf) 'untrusted install wrote the shim'

    # 7. A foreign command file is never replaced or removed.
    $roots = New-Roots -Name 'case-foreign'
    New-Item -ItemType Directory -Path $roots.BinDir -Force | Out-Null
    $foreign = Join-Path $roots.BinDir 'belay.cmd'
    [System.IO.File]::WriteAllText($foreign, "@echo off`r`necho not belay`r`n")
    $result = Invoke-Install -Arguments @(
        '-Archive', $goodZip,
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -ne 0) 'foreign belay.cmd blocks install'
    Write-Check ([System.IO.File]::ReadAllText($foreign) -like '*not belay*') 'foreign belay.cmd is untouched'
    $result = Invoke-Install -Arguments @(
        '-Uninstall',
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -ne 0) 'foreign belay.cmd blocks uninstall'
    Write-Check ([System.IO.File]::ReadAllText($foreign) -like '*not belay*') 'foreign belay.cmd survives uninstall'

    # 8. Broad install roots are refused.
    if ($onWindows) {
        $broadRoot = 'C:\Windows'
        $broadBin = 'C:\Program Files'
    } else {
        $broadRoot = '/usr'
        $broadBin = '/etc'
    }
    $roots = New-Roots -Name 'case-broad'
    $result = Invoke-Install -Arguments @(
        '-Archive', $goodZip,
        '-InstallRoot', $broadRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -ne 0) 'broad install root is refused'
    Write-Check ($result.StdErr -like '*refusing broad install root*') 'broad install-root refusal is explicit'
    $result = Invoke-Install -Arguments @(
        '-Archive', $goodZip,
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $broadBin)
    Write-Check ($result.ExitCode -ne 0) 'broad command directory is refused'
    Write-Check ($result.StdErr -like '*command directory*') 'broad command-directory refusal is explicit'

    # 9. A relative install root is refused.
    $result = Invoke-Install -Arguments @(
        '-Archive', $goodZip,
        '-InstallRoot', 'relative-root',
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -ne 0) 'relative install root is refused'

    # 10. A mismatched package target is refused.
    $wrongTarget = New-FakePackage -Name 'pkg-target' -Target 'windows/arm64'
    $roots = New-Roots -Name 'case-target'
    $result = Invoke-Install -Arguments @(
        '-Archive', $wrongTarget,
        '-AllowUntrusted',
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -ne 0) 'mismatched archive target is refused'

    # 11. A command directory outside the install root gets an absolute shim.
    $splitRoot = Join-Path $work 'case-split'
    $splitInstall = Join-Path $splitRoot 'program'
    $splitBin = Join-Path $splitRoot 'commands'
    New-Item -ItemType Directory -Path $splitRoot -Force | Out-Null
    $result = Invoke-Install -Arguments @(
        '-Archive', $goodZip,
        '-InstallRoot', $splitInstall,
        '-BinDir', $splitBin)
    Write-Check ($result.ExitCode -eq 0) "install with a separate command directory succeeds (exit $($result.ExitCode)) $($result.StdErr)"
    $splitShim = Join-Path $splitBin 'belay.cmd'
    if (Test-Path -LiteralPath $splitShim -PathType Leaf) {
        $splitText = [System.IO.File]::ReadAllText($splitShim)
        $expectedTarget = Join-Path (Join-Path (Join-Path $splitInstall 'runtime') 'bin') 'belay.exe'
        Write-Check ($splitText -like "*$expectedTarget*") 'separate command directory gets an absolute shim'
    } else {
        Write-Check $false 'separate command directory gets a shim'
    }

    # 12. The environment override accepts an unsigned build like -AllowUntrusted.
    $roots = New-Roots -Name 'case-env-untrusted'
    $result = Invoke-Install -Arguments @(
        '-Archive', $unsignedZip,
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir) -Environment @{ BELAY_INSTALL_ALLOW_UNTRUSTED = '1' }
    Write-Check ($result.ExitCode -eq 0) "BELAY_INSTALL_ALLOW_UNTRUSTED=1 accepts an unsigned build (exit $($result.ExitCode)) $($result.StdErr)"

    # 13. A version that does not match the archive is refused.
    $roots = New-Roots -Name 'case-version'
    $result = Invoke-Install -Arguments @(
        '-Archive', $goodZip,
        '-Version', '0.0.1-alpha.1099',
        '-InstallRoot', $roots.InstallRoot,
        '-BinDir', $roots.BinDir)
    Write-Check ($result.ExitCode -ne 0) 'archive version mismatch is refused'

    # 14. -Help is a documented, successful no-op.
    $result = Invoke-Install -Arguments @('-Help')
    Write-Check ($result.ExitCode -eq 0) 'help exits successfully'
    Write-Check ($result.StdOut -like '*-Uninstall*') 'help documents -Uninstall'
} finally {
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}

if ($script:Failures -ne 0) {
    [Console]::Error.WriteLine("install-windows-test: $($script:Failures) check(s) failed")
    exit 1
}
Write-Output 'install-windows-test passed'
