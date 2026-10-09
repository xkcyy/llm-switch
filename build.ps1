# LLM Switch build script. The version number comes from the git tag (vX.Y.Z);
# never hard-code it. ASCII-only on purpose: Windows PowerShell 5.1 reads
# BOM-less .ps1 files as ANSI and would mangle non-ASCII characters.
#
#   build.ps1                     auto version: tag when HEAD is tagged, dev version otherwise
#   build.ps1 -SkipUI             skip the UI build when web/dist is already current
#   build.ps1 -Version 0.7.0      explicit version, for local development builds only
#   build.ps1 -Release            release build: clean tree + HEAD on a tag, plus
#                                 SHA256SUMS / latest.json / RELEASE_NOTES.md / version.txt

param(
    [string]$Version = "",
    [switch]$SkipUI,
    [switch]$Release,
    [string]$Notes = ""
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
. "$root\scripts\lib\gitversion.ps1"

function Write-Utf8NoBom {
    param([string]$Path, [string]$Content)
    [System.IO.File]::WriteAllText($Path, $Content, (New-Object System.Text.UTF8Encoding($false)))
}

# Reads "LLM-Switch.exe --version" output. The EXE uses the Windows GUI
# subsystem, so Windows PowerShell 5.1 cannot capture its stdout through a
# pipeline; a redirected temp file works on every version.
function Get-EmbeddedVersion {
    param([string]$Exe)
    $tmp = [System.IO.Path]::GetTempFileName()
    try {
        Start-Process -FilePath $Exe -ArgumentList '--version' -NoNewWindow -Wait -RedirectStandardOutput $tmp
        return ((Get-Content -LiteralPath $tmp -Raw) -replace '\s+$', '')
    } finally {
        Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
    }
}

Push-Location $root
try {
    $tagVersion = Get-ExactTagVersion
    if (-not $Version) {
        $Version = if ($tagVersion) { $tagVersion } else { Get-DevVersion }
    }
    if ($Release) {
        if (Test-GitDirty) { throw "working tree is dirty; commit first, then tag with scripts/release.ps1" }
        if (-not $tagVersion) { throw "HEAD is not on a tag; release with scripts/release.ps1" }
        if ($Version -ne $tagVersion) { throw "version does not match tag: -Version=$Version, tag=v$tagVersion" }
        if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "release version must be X.Y.Z: $Version" }
    }
    Write-Host "Version: $Version" -ForegroundColor Cyan

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
    # Windows locks a running EXE, so a rebuild cannot overwrite it in place.
    if (Get-Process -Name 'LLM-Switch' -ErrorAction SilentlyContinue) {
        Write-Warning "LLM Switch is running; dist\LLM-Switch.exe is locked. Quit the tray app (right-click its icon), then rebuild."
        exit 1
    }
    go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$Version" -o dist/LLM-Switch.exe ./cmd/llm-switch
    if ($LASTEXITCODE -ne 0) { throw "build failed" }

    $size = [math]::Round((Get-Item dist/LLM-Switch.exe).Length / 1MB, 2)
    Write-Host "Done: dist/LLM-Switch.exe ($size MB)" -ForegroundColor Green
    if ($size -gt 45) { Write-Warning "Size close to the 50 MB limit, check dependencies." }

    if (-not $Release) { return }

    # Release gate: the embedded version must equal the tag, or tag and binary drift.
    $reported = Get-EmbeddedVersion "$root\dist\LLM-Switch.exe"
    if ($reported -notmatch [regex]::Escape($Version)) {
        throw "embedded version does not match tag: [$reported]"
    }

    $sha = (Get-FileHash -LiteralPath "$root\dist\LLM-Switch.exe" -Algorithm SHA256).Hash.ToLowerInvariant()
    Set-Content -LiteralPath "$root\dist\SHA256SUMS" -Value "$sha  LLM-Switch.exe" -Encoding ascii
    Set-Content -LiteralPath "$root\dist\version.txt" -Value $Version -Encoding ascii

    if (-not $Notes) {
        $Notes = Get-ReleaseNotes -Version $Version -Path (Get-ChangelogPath -Root $root)
    }
    if (-not $Notes) { $Notes = "LLM Switch v$Version" }
    $Notes = $Notes.Trim()
    Write-Utf8NoBom -Path "$root\dist\RELEASE_NOTES.md" -Content $Notes

    $repo = Get-GitHubSlug
    $manifest = [ordered]@{
        version = $Version
        url     = "https://github.com/$repo/releases/download/v$Version/LLM-Switch.exe"
        sha256  = $sha
        notes   = $Notes
    }
    Write-Utf8NoBom -Path "$root\dist\latest.json" -Content (($manifest | ConvertTo-Json) + "`n")
    Write-Host "Wrote dist/SHA256SUMS, dist/latest.json, dist/RELEASE_NOTES.md, dist/version.txt" -ForegroundColor Green
} finally {
    Pop-Location
}
