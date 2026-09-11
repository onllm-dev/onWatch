#Requires -Version 5.1
<#
    install.ps1 Test Suite (Windows)

    Usage:
      powershell -NoProfile -ExecutionPolicy Bypass -File tests\test_install_ps1.ps1
      pwsh       -NoProfile -ExecutionPolicy Bypass -File tests\test_install_ps1.ps1

    Covers the optional GitHub star courtesy. gh writes to stderr on every
    `gh auth status`, logged in or not, and Windows PowerShell 5.1 turns
    redirected native stderr into a terminating NativeCommandError while
    $ErrorActionPreference is "Stop". That aborted the installer after the
    daemon had already started, so the run ended on a red gh.exe error instead
    of "Installation complete".
#>

$ErrorActionPreference = "Stop"

$script:Total = 0
$script:Failures = 0

function Invoke-TestCase {
    param([string]$Name, [scriptblock]$Body)

    $script:Total++
    try {
        & $Body
        Write-Host "  PASS  $Name"
    } catch {
        $script:Failures++
        Write-Host "  FAIL  $Name"
        Write-Host "        $($_.Exception.Message)"
    }
}

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

# ─── install.ps1 under test ────────────────────────────────────────────

# Load every function without running Main, the same way tests/test_install.sh
# trims the trailing call out of install.sh.
$installScript = Join-Path $PSScriptRoot "..\install.ps1"
$kept = New-Object System.Collections.Generic.List[string]
foreach ($line in (Get-Content -LiteralPath $installScript)) {
    if ($line -match '^\s*Main\s*$') { break }
    $kept.Add($line)
}
Invoke-Expression ($kept -join "`n")

# The star prompt needs a console. Stub it so the tests never block on input.
$script:StubAnswer = "Y"
function Read-PromptWithDefault {
    param([string]$Prompt, [string]$Default)
    return $script:StubAnswer
}

# ─── Fake gh ───────────────────────────────────────────────────────────

# A gh that logs its arguments and always writes to stderr - real gh does too,
# including on a successful `gh auth status`.
function New-FakeGh {
    param([int]$AuthExit = 0, [int]$ApiExit = 1, [int]$StarExit = 0)

    $dir = Join-Path ([System.IO.Path]::GetTempPath()) ("onwatch-gh-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $log = Join-Path $dir "calls.log"

    $body = @"
@echo off
echo %* >> "$log"
if "%1"=="auth" (
  echo You are not logged into any GitHub hosts. To log in, run: gh auth login 1>&2
  exit /b $AuthExit
)
if "%1"=="api" (
  echo gh: Not Found HTTP 404 1>&2
  exit /b $ApiExit
)
if "%1"=="repo" (
  echo HTTP 401 Bad credentials 1>&2
  exit /b $StarExit
)
exit /b 0
"@
    Set-Content -LiteralPath (Join-Path $dir "gh.cmd") -Value $body -Encoding ASCII

    return [pscustomobject]@{ Dir = $dir; Log = $log }
}

function New-TestInstallDir {
    $dir = Join-Path ([System.IO.Path]::GetTempPath()) ("onwatch-install-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    return $dir
}

# Runs Offer-GitHubStar and reports what reached the console, what was thrown
# and where the marker landed. Nothing here is allowed to abort the caller.
function Invoke-StarOffer {
    param([object]$Gh, [string]$PathOverride)

    $script:INSTALL_DIR = New-TestInstallDir
    $previousPath = $env:PATH
    if ($PathOverride) {
        $env:PATH = $PathOverride
    } elseif ($Gh) {
        $env:PATH = "$($Gh.Dir);$env:PATH"
    }

    $thrown = $null
    $output = ""
    try {
        $output = (Offer-GitHubStar 2>&1 | Out-String)
    } catch {
        $thrown = $_
    } finally {
        $env:PATH = $previousPath
    }

    return [pscustomobject]@{
        Thrown     = $thrown
        Output     = $output
        InstallDir = $script:INSTALL_DIR
        Marker     = (Join-Path $script:INSTALL_DIR ".star-prompted")
    }
}

function Assert-Quiet {
    param([object]$Result)

    if ($Result.Thrown) {
        throw "Offer-GitHubStar threw: $($Result.Thrown.Exception.Message)"
    }
    foreach ($marker in @("NativeCommandError", "not logged into", "gh.exe :", "Bad credentials", "Not Found")) {
        Assert-True (-not ($Result.Output -match [regex]::Escape($marker))) `
            "gh output leaked to the console: '$marker' in: $($Result.Output)"
    }
}

# ─── Tests ─────────────────────────────────────────────────────────────

Write-Host ""
Write-Host "  install.ps1 tests ($($PSVersionTable.PSEdition) $($PSVersionTable.PSVersion))"
Write-Host ""

$env:ONWATCH_STAR = $null

Invoke-TestCase "gh installed but not logged in: no error, no prompt" {
    $gh = New-FakeGh -AuthExit 1
    $result = Invoke-StarOffer -Gh $gh
    Assert-Quiet $result
    Assert-True (-not (Test-Path -LiteralPath $result.Marker)) "marker written without asking"
    $calls = Get-Content -LiteralPath $gh.Log -Raw
    Assert-True ($calls -match "auth status") "gh auth status was never called"
    Assert-True (-not ($calls -match "repo star")) "starred despite a logged out gh"
}

Invoke-TestCase "already starred: no error, no prompt" {
    $gh = New-FakeGh -AuthExit 0 -ApiExit 0
    $result = Invoke-StarOffer -Gh $gh
    Assert-Quiet $result
    Assert-True (-not (Test-Path -LiteralPath $result.Marker)) "marker written for an already starred repo"
    $calls = Get-Content -LiteralPath $gh.Log -Raw
    Assert-True (-not ($calls -match "repo star")) "starred a repo that was already starred"
}

Invoke-TestCase "logged in and not starred: stars and thanks" {
    $gh = New-FakeGh -AuthExit 0 -ApiExit 1 -StarExit 0
    $script:StubAnswer = "Y"
    $result = Invoke-StarOffer -Gh $gh
    if ($result.Thrown) { throw "Offer-GitHubStar threw: $($result.Thrown.Exception.Message)" }
    Assert-True (Test-Path -LiteralPath $result.Marker) "marker not written after asking"
    $calls = Get-Content -LiteralPath $gh.Log -Raw
    Assert-True ($calls -match "repo star") "gh repo star was never called"
    Assert-True ($result.Output -match "Thanks for the star") "no thank you shown: $($result.Output)"
}

Invoke-TestCase "star request fails: warns and keeps going" {
    $gh = New-FakeGh -AuthExit 0 -ApiExit 1 -StarExit 1
    $script:StubAnswer = "Y"
    $result = Invoke-StarOffer -Gh $gh
    if ($result.Thrown) { throw "Offer-GitHubStar threw: $($result.Thrown.Exception.Message)" }
    Assert-True ($result.Output -match "Could not star the repo") "no warning shown: $($result.Output)"
    Assert-True (-not ($result.Output -match "NativeCommandError")) "gh stderr leaked: $($result.Output)"
}

Invoke-TestCase "declined: no star, marker still written" {
    $gh = New-FakeGh -AuthExit 0 -ApiExit 1
    $script:StubAnswer = "n"
    $result = Invoke-StarOffer -Gh $gh
    if ($result.Thrown) { throw "Offer-GitHubStar threw: $($result.Thrown.Exception.Message)" }
    $calls = Get-Content -LiteralPath $gh.Log -Raw
    Assert-True (-not ($calls -match "repo star")) "starred after the offer was declined"
    Assert-True (Test-Path -LiteralPath $result.Marker) "marker not written after declining"
}
$script:StubAnswer = "Y"

Invoke-TestCase "ONWATCH_STAR=no: gh is never run" {
    $gh = New-FakeGh
    $env:ONWATCH_STAR = "no"
    try {
        $result = Invoke-StarOffer -Gh $gh
    } finally {
        $env:ONWATCH_STAR = $null
    }
    Assert-Quiet $result
    Assert-True (-not (Test-Path -LiteralPath $gh.Log)) "gh ran despite ONWATCH_STAR=no"
}

Invoke-TestCase "marker present: gh is never run" {
    $gh = New-FakeGh
    $script:INSTALL_DIR = New-TestInstallDir
    Set-Content -LiteralPath (Join-Path $script:INSTALL_DIR ".star-prompted") -Value "asked"

    $previousPath = $env:PATH
    $env:PATH = "$($gh.Dir);$env:PATH"
    try {
        Offer-GitHubStar | Out-Null
    } finally {
        $env:PATH = $previousPath
    }
    Assert-True (-not (Test-Path -LiteralPath $gh.Log)) "gh ran despite an existing marker"
}

Invoke-TestCase "gh not installed: no error" {
    $empty = Join-Path ([System.IO.Path]::GetTempPath()) ("onwatch-nogh-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $empty | Out-Null
    $result = Invoke-StarOffer -PathOverride $empty
    Assert-Quiet $result
    Assert-True (-not (Test-Path -LiteralPath $result.Marker)) "marker written without gh"
}

# ─── Summary ───────────────────────────────────────────────────────────

Write-Host ""
if ($script:Failures -eq 0) {
    Write-Host "  $script:Total passed"
    Write-Host ""
    exit 0
}
Write-Host "  $script:Failures of $script:Total failed"
Write-Host ""
exit 1
