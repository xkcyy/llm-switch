# LLM Switch build script
# Usage: powershell -ExecutionPolicy Bypass -File build.ps1 [-Version 0.2.0] [-SkipUI]

param(
    [string]$Version = "0.4.0",
    [switch]$SkipUI
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

Push-Location $root
try {
    if (-not $SkipUI) {
        Write-Host "Building UI..." -ForegroundColor Cyan
        Push-Location web
        if (-not (Test-Path node_modules)) { npm install --no-audit --no-fund | Out-Host }
        npm run build | Out-Host
        if ($LASTEXITCODE -ne 0) { throw "UI build failed" }
        Pop-Location
    }

    Write-Host "Running tests..." -ForegroundColor Cyan
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "tests failed" }

    Write-Host "Building release..." -ForegroundColor Cyan
    New-Item -ItemType Directory -Force dist | Out-Null
    go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$Version" -o dist/LLM-Switch.exe ./cmd/llm-switch
    if ($LASTEXITCODE -ne 0) { throw "build failed" }

    $size = [math]::Round((Get-Item dist/LLM-Switch.exe).Length / 1MB, 2)
    Write-Host "Done: dist/LLM-Switch.exe ($size MB)" -ForegroundColor Green
    if ($size -gt 45) { Write-Warning "Size close to the 50 MB limit, check dependencies." }
} finally {
    Pop-Location
}
