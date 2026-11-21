#Requires -Version 5.1
<#
.SYNOPSIS
    Installs pingm as a system-wide command on Windows.

.DESCRIPTION
    Downloads the latest pingm release for this machine's architecture,
    verifies its SHA-256 checksum, installs pingm.exe to a folder under
    Program Files, and adds that folder to the system PATH.

    Must be run from an Administrator PowerShell, because it writes to
    Program Files and to the machine-wide PATH.

.PARAMETER InstallDir
    Where pingm.exe is installed. Defaults to %ProgramFiles%\pingm.

.PARAMETER Version
    Release to install, such as 2.2.0 or v2.2.0. Defaults to the latest.

.PARAMETER FromSource
    Build from the checkout containing this script instead of downloading
    a release. Requires Go 1.26 or newer.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\install.ps1

.EXAMPLE
    .\install.ps1 -Version 2.2.0

.EXAMPLE
    .\install.ps1 -FromSource
#>
[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path $env:ProgramFiles 'pingm'),
    [string]$Version = 'latest',
    [switch]$FromSource
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Repo = 'soundsofthesir/pingm'
$ExeName = 'pingm.exe'

function Test-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-ReleaseBaseUrl {
    if ($Version -eq 'latest') {
        return "https://github.com/$Repo/releases/latest/download"
    }
    $tag = if ($Version.StartsWith('v')) { $Version } else { "v$Version" }
    return "https://github.com/$Repo/releases/download/$tag"
}

function Get-Architecture {
    $raw = if ($env:PROCESSOR_ARCHITEW6432) {
        $env:PROCESSOR_ARCHITEW6432
    } else {
        $env:PROCESSOR_ARCHITECTURE
    }
    switch ($raw) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { throw "Unsupported architecture '$raw'. pingm ships for amd64 and arm64." }
    }
}

function Add-ToMachinePath {
    param([string]$Directory)

    $key = [Microsoft.Win32.Registry]::LocalMachine.OpenSubKey(
        'SYSTEM\CurrentControlSet\Control\Session Manager\Environment', $true)
    try {
        $current = $key.GetValue(
            'Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $parts = @($current -split ';' | Where-Object { $_ -ne '' })
        if ($parts -contains $Directory) {
            return
        }

        $kind = $key.GetValueKind('Path')
        $updated = ($parts -join ';') + ';' + $Directory
        $key.SetValue('Path', $updated, $kind)
    }
    finally {
        $key.Dispose()
    }

    # Let running shells and Explorer pick the change up without a sign-out.
    try {
        Add-Type -Namespace Win32 -Name NativeMethods -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(
    IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam,
    uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
        $result = [UIntPtr]::Zero
        [Win32.NativeMethods]::SendMessageTimeout(
            [IntPtr]0xffff, 0x1a, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result) | Out-Null
    }
    catch {
        # Not fatal: new terminals still see the updated PATH.
    }
}

if (-not (Test-Administrator)) {
    throw 'Installing pingm system-wide needs an Administrator PowerShell. ' +
        'Right-click PowerShell and choose "Run as administrator".'
}

if ($env:OS -ne 'Windows_NT') {
    throw 'This installer is for Windows. Use ./install.sh on macOS or Linux.'
}

if (Get-Process -Name 'pingm' -ErrorAction SilentlyContinue) {
    throw 'pingm is running. Close it, then run the installer again.'
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$target = Join-Path $InstallDir $ExeName

if ($FromSource) {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw 'Go is required to build from source (https://go.dev/dl/).'
    }
    Write-Host 'Building pingm ...'
    Push-Location $PSScriptRoot
    try {
        & go build -trimpath -ldflags '-s -w' -o $target .
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed with exit code $LASTEXITCODE."
        }
    }
    finally {
        Pop-Location
    }
}
else {
    $arch = Get-Architecture
    $zipName = "pingm-windows-$arch.zip"
    $base = Get-ReleaseBaseUrl
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('pingm-install-' + [Guid]::NewGuid())
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null

    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor `
            [Net.SecurityProtocolType]::Tls12

        $zipPath = Join-Path $tmp $zipName
        $sumsPath = Join-Path $tmp 'SHA256SUMS'
        Write-Host "Downloading $zipName ..."
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$zipName" -OutFile $zipPath
        Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sumsPath

        $line = Get-Content -Path $sumsPath |
            Where-Object { $_ -match ([regex]::Escape($zipName) + '$') } |
            Select-Object -First 1
        if (-not $line) {
            throw "SHA256SUMS does not list $zipName."
        }
        $expected = ($line -split '\s+')[0].ToLowerInvariant()
        $actual = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            throw "Checksum mismatch for ${zipName}: expected $expected, got $actual."
        }

        Unblock-File -Path $zipPath
        Expand-Archive -Path $zipPath -DestinationPath $tmp -Force
        $source = Join-Path $tmp $ExeName
        if (-not (Test-Path $source)) {
            throw "The archive did not contain $ExeName."
        }
        Unblock-File -Path $source
        Copy-Item -Path $source -Destination $target -Force
    }
    finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Add-ToMachinePath -Directory $InstallDir
$env:Path = $env:Path.TrimEnd(';') + ';' + $InstallDir

$installed = & $target -v
Write-Host ''
Write-Host "Installed: $installed"
Write-Host "Location:  $target"
Write-Host "PATH:      $InstallDir is on the system PATH."
Write-Host "           Open a new terminal to use 'pingm'."
