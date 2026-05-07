#Requires -RunAsAdministrator

<#
.SYNOPSIS
Installs and configures onWatch as a Windows Service using NSSM.

.DESCRIPTION
This script safely sets up onWatch to run in the background as a robust Windows Service.
It handles common pitfalls such as:
  - Missing environment variables when running as LocalSystem.
  - Ensuring the application stays in the foreground (--debugstdout) so NSSM can manage its lifecycle.
  - Creating dedicated service log files.
  - Avoiding "marked for deletion" lockups during re-installation.

.PREREQUISITES
  - You must run this script from an Elevated (Administrator) PowerShell session.
  - NSSM (Non-Sucking Service Manager) must be installed (e.g. via `winget install nssm`).
  - onWatch must be downloaded/built in the target directory.
#>

param(
    [string]$ServiceName = "onWatchService",
    [string]$InstallDir  = "$env:USERPROFILE\.onwatch",
    [string]$Executable  = "$env:USERPROFILE\.onwatch\bin\onwatch.exe"
)

$ErrorActionPreference = 'Stop'

Write-Host "=============================================" -ForegroundColor Cyan
Write-Host " onWatch Windows Service Setup               " -ForegroundColor Cyan
Write-Host "=============================================" -ForegroundColor Cyan

# 1. Locate NSSM
$NSSM = Get-Command "nssm" -ErrorAction SilentlyContinue
if (-not $NSSM) {
    # Attempt to find it in common winget paths if the PATH hasn't updated yet
    $wingetPaths = Get-ChildItem -Path "$env:LOCALAPPDATA\Microsoft\WinGet\Packages" -Filter "nssm.exe" -Recurse -ErrorAction SilentlyContinue
    if ($wingetPaths) {
        $NSSM = $wingetPaths | Select-Object -First 1 | Select-Object -ExpandProperty FullName
    }
}

if (-not $NSSM) {
    Write-Error "NSSM is not installed or not in your PATH.`nPlease run: winget install nssm --accept-package-agreements --accept-source-agreements --silent"
    exit 1
}

Write-Host "Found NSSM at: $NSSM" -ForegroundColor DarkGray

# 2. Validate Executable
if (-not (Test-Path $Executable)) {
    Write-Error "onWatch executable not found at: $Executable.`nPlease install onWatch first or provide the correct path."
    exit 1
}

# 3. Prepare Paths
$LogFile = Join-Path $InstallDir "service.log"
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

# 4. Stop and Remove Existing Service (if any)
Write-Host "`nCleaning up existing service (if any)..."
$existingStatus = & $NSSM status $ServiceName 2>$null
if ($LASTEXITCODE -eq 0 -or $existingStatus) {
    Write-Host "Stopping $ServiceName..." -ForegroundColor Yellow
    & $NSSM stop $ServiceName 2>$null
    Start-Sleep -Seconds 2
    Write-Host "Removing $ServiceName..." -ForegroundColor Yellow
    & $NSSM remove $ServiceName confirm 2>$null
    
    # Give Windows SC a moment to clear the registry
    Start-Sleep -Seconds 2
}

# 5. Install the Service
Write-Host "Installing $ServiceName..." -ForegroundColor Cyan
& $NSSM install $ServiceName "$Executable"

# 6. Configure Service Parameters
Write-Host "Applying robust configuration..." -ForegroundColor DarkGray

# Force foreground execution and pipe all logs so NSSM can manage the process state
& $NSSM set $ServiceName AppParameters "--debugstdout"
& $NSSM set $ServiceName AppDirectory "$InstallDir"
& $NSSM set $ServiceName AppStdout "$LogFile"
& $NSSM set $ServiceName AppStderr "$LogFile"

# Set metadata
& $NSSM set $ServiceName DisplayName "onWatch API Quota Tracker"
& $NSSM set $ServiceName Description "Tracks AI API quota usage across providers"

# Inject current user environment so LocalSystem can find ~/.codex, ~/.claude, and ~/.gemini
$envExtra = "USERPROFILE=$env:USERPROFILE`0HOME=$env:USERPROFILE`0"
& $NSSM set $ServiceName AppEnvironmentExtra $envExtra

# Set startup type
& $NSSM set $ServiceName Start SERVICE_AUTO_START

# 7. Start the Service
Write-Host "`nStarting $ServiceName..." -ForegroundColor Cyan
& $NSSM start $ServiceName

# 8. Verify Status
Start-Sleep -Seconds 2
$finalStatus = & $NSSM status $ServiceName

if ($finalStatus -like "*SERVICE_RUNNING*") {
    Write-Host "`nSUCCESS! onWatch is now running as a background service." -ForegroundColor Green
    Write-Host "Dashboard: http://localhost:9211" -ForegroundColor Green
    Write-Host "Logs:      $LogFile" -ForegroundColor DarkGray
} else {
    Write-Host "`nWARNING: Service installation completed, but it is not in a RUNNING state." -ForegroundColor Yellow
    Write-Host "Current Status: $finalStatus" -ForegroundColor Yellow
    Write-Host "Check the logs at: $LogFile" -ForegroundColor Yellow
}
