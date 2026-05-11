# onWatch Service Setup Script
# Run this from an ELEVATED (Administrator) PowerShell session.

$NSSM = "C:\Users\Paul\AppData\Local\Microsoft\WinGet\Packages\NSSM.NSSM_Microsoft.WinGet.Source_8wekyb3d8bbwe\nssm-2.24-101-g897c7ad\win64\nssm.exe"
$BIN  = "C:\Projects\onllm\onwatch\onwatch.exe"
$DIR  = "C:\Projects\onllm\onwatch"
$LOG  = "C:\Projects\onllm\onwatch\service.log"

# Using a brand new name to bypass the "marked for deletion" lock on the old one
$SVC  = "onWatchService"

if (-not (Test-Path $NSSM)) {
    Write-Error "NSSM not found at $NSSM."
    exit 1
}

Write-Host "Configuring $SVC..." -ForegroundColor Cyan

# Stop and remove existing service if any
& $NSSM stop $SVC 2>$null
& $NSSM remove $SVC confirm 2>$null

# Install the service
& $NSSM install $SVC "$BIN"
if ($LASTEXITCODE -ne 0) { Write-Error "Failed to install service."; exit 1 }

# Set parameters
# --debugstdout forces all logs to the service.log file managed by NSSM
& $NSSM set $SVC AppParameters "--debugstdout"
& $NSSM set $SVC AppDirectory "$DIR"
& $NSSM set $SVC AppStdout "$LOG"
& $NSSM set $SVC AppStderr "$LOG"
& $NSSM set $SVC DisplayName "onWatch API Tracker"
& $NSSM set $SVC Description "Tracks AI API quota usage across providers"

# Configure environment variables so that LocalSystem accesses Paul's home directory
# This allows auto-detecting Claude/Codex/Gemini credentials and saves data to Paul's .onwatch folder
& $NSSM set $SVC AppEnvironmentExtra "USERPROFILE=C:\Users\Paul`0HOME=C:\Users\Paul`0"

# Use LocalSystem (the default, no password required)
& $NSSM set $SVC ObjectName LocalSystem ""
& $NSSM set $SVC Start SERVICE_AUTO_START

# Start the service
Write-Host "`nStarting $SVC..." -ForegroundColor Cyan
& $NSSM start $SVC

# Verify status
Start-Sleep -Seconds 2
$status = & $NSSM status $SVC
Write-Host "Current Service Status: $status" -ForegroundColor Green

if ($status -eq "SERVICE_RUNNING") {
    Write-Host "Success! onWatch is running at http://localhost:9211" -ForegroundColor Green
} else {
    Write-Host "Service is not running. Check logs at: $LOG" -ForegroundColor Red
}
