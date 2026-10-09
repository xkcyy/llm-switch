# Single source of truth for version derivation: the git tag (vX.Y.Z).
# Dot-sourced by build.ps1 and scripts/release.ps1. Keep this file ASCII-only:
# Windows PowerShell 5.1 reads BOM-less .ps1 files as ANSI, so non-ASCII
# characters would break parsing and corrupt adjacent ASCII text.

function Test-GitAvailable {
    return [bool](Get-Command git -ErrorAction SilentlyContinue)
}

# Runs git without letting stderr become a terminating error. Windows
# PowerShell 5.1 turns native stderr into an error record, and both build
# scripts run with $ErrorActionPreference = 'Stop', so every git call goes
# through here. Returns output lines, the exit code and the trimmed text.
function Invoke-GitQuiet {
    param([string[]]$GitArgs)
    if (-not (Test-GitAvailable)) { throw "git is not available on PATH" }
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & git @GitArgs 2>$null
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $prev
    }
    $items = if ($null -eq $out) { @() } else { @($out) }
    return [pscustomobject]@{
        Output   = $items
        ExitCode = [int]$code
        Text     = (($items -join "`n").Trim())
    }
}

function Test-GitDirty {
    $r = Invoke-GitQuiet @('status', '--porcelain')
    if ($r.ExitCode -ne 0) { throw "git status failed: $($r.Text)" }
    return [bool]$r.Text
}

# Version of the tag that HEAD points at (no "v" prefix), or $null.
function Get-ExactTagVersion {
    $r = Invoke-GitQuiet @('tag', '--points-at', 'HEAD')
    if ($r.ExitCode -ne 0) { return $null }
    $tag = @($r.Output | Where-Object { $_ -match '^v\d+\.\d+\.\d+$' } | Select-Object -First 1)
    if (-not $tag) { return $null }
    return ($tag.Trim() -replace '^v', '')
}

# Highest version tag (with "v" prefix), or $null when the repo has none.
function Get-LastTag {
    $r = Invoke-GitQuiet @('tag', '--list', 'v*', '--sort=-v:refname')
    if ($r.ExitCode -ne 0) { return $null }
    $tag = @($r.Output | Where-Object { $_ -match '^v\d+\.\d+\.\d+$' } | Select-Object -First 1)
    if (-not $tag) { return $null }
    return $tag.Trim()
}

# Development version for a commit that is not on a tag, e.g. 0.6.0-dev.3+g37e88a0.
function Get-DevVersion {
    $r = Invoke-GitQuiet @('rev-parse', '--short', 'HEAD')
    if ($r.ExitCode -ne 0 -or -not $r.Text) { return 'dev' }
    $sha = $r.Text
    $last = Get-LastTag
    if (-not $last) { return "dev+g$sha" }
    $c = Invoke-GitQuiet @('rev-list', '--count', "$last..HEAD")
    $count = if ($c.ExitCode -eq 0 -and $c.Text) { $c.Text } else { '0' }
    return "$($last -replace '^v', '')-dev.$count+g$sha"
}

# Commit subjects from the given tag (exclusive) to HEAD; all commits when no tag.
function Get-ChangeMessages {
    param([string]$SinceTag = "")
    $range = if ($SinceTag) { "$SinceTag..HEAD" } else { 'HEAD' }
    $r = Invoke-GitQuiet @('log', '--pretty=%s', $range)
    if ($r.ExitCode -ne 0) { return @() }
    return @($r.Output | Where-Object { $_ })
}

# Derive the bump from commit subjects: breaking change > feat > anything else.
function Get-BumpFromCommits {
    param([string[]]$Messages = @())
    $bump = 'patch'
    foreach ($m in @($Messages)) {
        if (-not $m) { continue }
        if ($m -match '^[a-zA-Z]+(\([^)]*\))?!:' -or $m -match 'BREAKING') { return 'major' }
        if ($m -match '^feat(\([^)]*\))?:') { $bump = 'minor' }
    }
    return $bump
}

function Get-NextVersion {
    param([string]$Current = "", [string]$Bump = 'patch')
    if (-not $Current) { $Current = '0.0.0' }
    $m = [regex]::Match($Current, '^(\d+)\.(\d+)\.(\d+)$')
    if (-not $m.Success) { throw "cannot parse current version: $Current" }
    $major = [int]$m.Groups[1].Value
    $minor = [int]$m.Groups[2].Value
    $patch = [int]$m.Groups[3].Value
    switch ($Bump) {
        'major' { $major++; $minor = 0; $patch = 0 }
        'minor' { $minor++; $patch = 0 }
        'patch' { $patch++ }
        default { throw "unknown bump level: $Bump" }
    }
    return "$major.$minor.$patch"
}

# owner/repo parsed from remote.origin.url, with a fallback.
function Get-GitHubSlug {
    $fallback = 'xkcyy/llm-switch'
    $r = Invoke-GitQuiet @('config', '--get', 'remote.origin.url')
    if ($r.ExitCode -ne 0 -or -not $r.Text) { return $fallback }
    if ($r.Text -match 'github\.com[:/](?<owner>[^/]+)/(?<repo>[^/]+?)(?:\.git)?$') {
        return "$($Matches.owner)/$($Matches.repo)"
    }
    return $fallback
}

# Path of the changelog under docs/design. The file name is CJK, so it is
# assembled from code points: a literal would make this file non-ASCII.
function Get-ChangelogPath {
    param([string]$Root)
    $name = "$([char]0x53D8)$([char]0x66F4)$([char]0x8BB0)$([char]0x5F55).md"
    return (Join-Path $Root "docs\design\$name")
}

# Body of the changelog section whose heading carries the version.
# Returns an empty string when the section is missing.
function Get-ReleaseNotes {
    param([string]$Version, [string]$Path)
    if (-not $Version -or -not (Test-Path -LiteralPath $Path)) { return '' }
    # -Encoding UTF8 is required: Windows PowerShell 5.1 would otherwise read
    # the changelog as ANSI and corrupt CJK text next to the version digits.
    $lines = @(Get-Content -LiteralPath $Path -Encoding UTF8)
    $pattern = "^###\s.*$([regex]::Escape($Version))"
    $start = -1
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match $pattern) { $start = $i; break }
    }
    if ($start -lt 0) { return '' }
    $body = @()
    for ($i = $start + 1; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match '^##\s') { break }
        $body += $lines[$i]
    }
    return (($body -join "`n").Trim())
}
