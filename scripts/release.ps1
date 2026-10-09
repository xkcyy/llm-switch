# LLM Switch release script. The version comes from the git tag, never typed by
# hand for the release itself. ASCII-only on purpose: Windows PowerShell 5.1
# reads BOM-less .ps1 files as ANSI and would mangle non-ASCII characters.
#
# Pipeline: check branch and working tree -> derive version -> check changelog
# -> run tests -> tag -> push. The tag triggers .github/workflows/release.yml,
# which builds the artifacts and publishes the GitHub Release.
#
#   scripts/release.ps1 -DryRun              show the recommended version, change nothing
#   scripts/release.ps1 -Yes                 confirm and run
#   scripts/release.ps1 -Bump major -Yes     override the suggested bump level
#   scripts/release.ps1 -Version 1.0.0 -Yes  pin the version explicitly

param(
    [ValidateSet('auto', 'major', 'minor', 'patch')][string]$Bump = 'auto',
    [string]$Version = "",
    [switch]$DryRun,
    [switch]$Yes,
    [switch]$NoPush,
    [switch]$NoFetch,
    [switch]$AllowMissingNotes
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. "$PSScriptRoot\lib\gitversion.ps1"

Push-Location $root
try {
    if (-not $NoFetch) {
        $fetch = Invoke-GitQuiet @('fetch', '--tags', '--quiet')
        if ($fetch.ExitCode -ne 0) { Write-Warning "git fetch --tags failed; working with local tags only: $($fetch.Text)" }
    }

    $b = Invoke-GitQuiet @('rev-parse', '--abbrev-ref', 'HEAD')
    if ($b.ExitCode -ne 0 -or -not $b.Text) { throw "git rev-parse failed; not a git repository?" }
    $branch = $b.Text
    if ($branch -ne 'main' -and $branch -notlike 'release/*') {
        throw "release from main or release/X.Y, current branch is $branch"
    }
    if (Test-GitDirty) { throw "working tree is dirty; commit code and changelog first" }

    $lastTag = Get-LastTag
    $current = if ($lastTag) { $lastTag -replace '^v', '' } else { '' }
    $messages = Get-ChangeMessages -SinceTag $lastTag
    $recommended = Get-BumpFromCommits -Messages $messages
    $target = if ($Bump -eq 'auto') { $recommended } else { $Bump }

    if ($Version) {
        if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "version must be X.Y.Z: $Version" }
        if ($current -and ([version]$Version -le [version]$current)) {
            throw "version must be greater than current $current`: $Version"
        }
    } else {
        $Version = Get-NextVersion -Current $current -Bump $target
    }
    $existing = Invoke-GitQuiet @('tag', '--list', "v$Version")
    if ($existing.ExitCode -eq 0 -and $existing.Text) { throw "tag v$Version already exists" }

    $notes = Get-ReleaseNotes -Version $Version -Path (Get-ChangelogPath -Root $root)
    if (-not $notes -and -not $AllowMissingNotes) {
        throw "changelog has no section for $Version (heading like '### ... (X.Y.Z)'). Add and commit it, or pass -AllowMissingNotes."
    }

    $currentLabel = if ($current) { $current } else { 'no tag' }
    Write-Host "Current version: $currentLabel"
    Write-Host "Commits since tag: $(@($messages).Count)"
    Write-Host "Suggested bump: $recommended; using: $target"
    Write-Host "Target version: v$Version" -ForegroundColor Cyan
    if (-not $notes) { Write-Warning "changelog has no section for this version; release notes fall back to a default line." }

    if ($DryRun) {
        Write-Host "DryRun: nothing was changed." -ForegroundColor Yellow
        return
    }
    if (-not $Yes) {
        $answer = Read-Host "Release v$Version? Type yes to continue"
        if ($answer -ne 'yes') { throw "cancelled" }
    }

    Write-Host "Running tests..." -ForegroundColor Cyan
    # The web package embeds web/dist, so a fresh clone cannot run go test
    # before the UI is built once.
    if (-not (Test-Path "$root\web\dist")) {
        Write-Host "Building UI first (web/dist is missing)..." -ForegroundColor Cyan
        Push-Location "$root\web"
        try {
            if (-not (Test-Path node_modules)) { npm install --no-audit --no-fund | Out-Host }
            npm run build | Out-Host
            if ($LASTEXITCODE -ne 0) { throw "UI build failed" }
        } finally {
            Pop-Location
        }
    }
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw "tests failed" }

    $tag = Invoke-GitQuiet @('tag', '-a', "v$Version", '-m', "LLM Switch v$Version")
    if ($tag.ExitCode -ne 0) { throw "git tag failed: $($tag.Text)" }
    Write-Host "Tagged v$Version"

    if ($NoPush) {
        Write-Host "Skipped push (-NoPush). Push with: git push origin $branch; git push origin v$Version" -ForegroundColor Yellow
        return
    }
    $pushBranch = Invoke-GitQuiet @('push', 'origin', $branch)
    if ($pushBranch.ExitCode -ne 0) { throw "push branch failed: $($pushBranch.Text)" }
    $pushTag = Invoke-GitQuiet @('push', 'origin', "v$Version")
    if ($pushTag.ExitCode -ne 0) { throw "push tag failed: $($pushTag.Text)" }
    Write-Host "Pushed v$Version; CI will build and publish the release." -ForegroundColor Green
} finally {
    Pop-Location
}
