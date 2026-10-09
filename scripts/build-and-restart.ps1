# Build LLM Switch and restart it, blocking until the proxy answers again.
#
# One job, no parameters: stop the running tray app (it locks
# dist\LLM-Switch.exe), run build.ps1 (UI + tests + go build), start the new
# binary hidden, then poll the proxy healthcheck. The script returns only
# after the service is serving, so callers can run it and keep working
# afterwards.
#
#   powershell -ExecutionPolicy Bypass -File scripts/build-and-restart.ps1
#
# ASCII-only on purpose: Windows PowerShell 5.1 reads BOM-less .ps1 files as
# ANSI and would mangle non-ASCII characters.

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $root 'dist\LLM-Switch.exe'
$name = 'LLM-Switch'

function Get-Running {
    return @(Get-Process -Name $name -ErrorAction SilentlyContinue)
}

# Proxy port comes from the config; fall back to the default.
$port = 8317
$cfgPath = Join-Path $env:USERPROFILE '.llm-switch\config.json'
if (Test-Path -LiteralPath $cfgPath) {
    try {
        $cfg = Get-Content -LiteralPath $cfgPath -Raw | ConvertFrom-Json
        if ($cfg.settings.proxy.port) { $port = [int]$cfg.settings.proxy.port }
    } catch {
        Write-Warning "Could not read proxy port from config, using $port."
    }
}

# 1. Stop the running app so it releases dist\LLM-Switch.exe.
$running = Get-Running
if ($running.Count -gt 0) {
    Write-Host "Stopping $name (PID $($running.Id -join ', '))..." -ForegroundColor Cyan
    $running | Stop-Process -Force
    $deadline = (Get-Date).AddSeconds(15)
    while ((Get-Running).Count -gt 0 -and (Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 200
    }
    if ((Get-Running).Count -gt 0) {
        throw "$name did not exit within 15s; quit it from the tray and retry."
    }
}

# 2. Build: UI, tests, then go build into dist\LLM-Switch.exe.
Write-Host 'Building...' -ForegroundColor Cyan
& "$root\build.ps1"
if ($LASTEXITCODE -ne 0) {
    throw "build.ps1 failed with exit code $LASTEXITCODE; not restarting."
}
if (-not (Test-Path -LiteralPath $exe)) {
    throw "built binary not found: $exe"
}

# 3. Start the new binary hidden (tray only, no browser window).
Write-Host "Starting $exe..." -ForegroundColor Cyan
Start-Process -FilePath $exe -ArgumentList '-silent' -WindowStyle Hidden

# 4. Block until the proxy actually serves, so the caller can resume immediately.
$url = "http://127.0.0.1:$port/healthz"
$deadline = (Get-Date).AddSeconds(60)
$healthy = $false
while ((Get-Date) -lt $deadline) {
    if ((Get-Running).Count -eq 0) {
        throw "$name exited during startup; check logs in $env:USERPROFILE\.llm-switch\logs"
    }
    try {
        $r = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
        if ($r.StatusCode -eq 200 -and $r.Content -match 'ok') {
            $healthy = $true
            break
        }
    } catch {
        # not listening yet
    }
    Start-Sleep -Milliseconds 400
}
if (-not $healthy) {
    throw "proxy did not become healthy on $url within 60s."
}

Write-Host "LLM Switch is up ($url)." -ForegroundColor Green
