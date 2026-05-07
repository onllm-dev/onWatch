# Aggressive Service Cleanup Script
# Run this from an ELEVATED (Administrator) PowerShell session.

Write-Host "Please ensure the 'Services' (services.msc) window and 'Task Manager' are CLOSED." -ForegroundColor Yellow

$NSSM = "C:\Users\Paul\AppData\Local\Microsoft\WinGet\Packages\NSSM.NSSM_Microsoft.WinGet.Source_8wekyb3d8bbwe\nssm-2.24-101-g897c7ad\win64\nssm.exe"
$Services = @("onWatch", "onWatchApp", "onWatchService")

foreach ($svc in $Services) {
    Write-Host "`nCleaning up: $svc" -ForegroundColor Cyan
    
    # 1. Stop the service
    & $NSSM stop $svc 2>$null
    Start-Sleep -Seconds 1

    # 2. Try removing via NSSM
    & $NSSM remove $svc confirm 2>$null

    # 3. Try removing via SC
    sc.exe delete $svc 2>$null

    # 4. Force delete from the Windows Registry to clear "marked for deletion" lock
    $regPath = "HKLM:\SYSTEM\CurrentControlSet\Services\$svc"
    if (Test-Path $regPath) {
        Write-Host "Forcefully removing registry keys for $svc..." -ForegroundColor Yellow
        Remove-Item -Path $regPath -Recurse -Force -ErrorAction SilentlyContinue
    } else {
        Write-Host "Registry keys already gone for $svc." -ForegroundColor DarkGray
    }
}

Write-Host "`nCleanup complete! To fully flush the Windows cache of these services, you must RESTART YOUR COMPUTER." -ForegroundColor Green
