# Installs deck on Windows from the latest GitHub release.
#
#   powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/dev-dami/deck-releases/main/install.ps1 | iex"
#
# Settings (environment):
#   DECK_VERSION      a release tag to install, e.g. v0.7.0 (default: the latest)
#   DECK_INSTALL_DIR  where deck.exe goes (default: %LOCALAPPDATA%\deck\bin)
#   DECK_REPO         the GitHub repository to install from (default: dev-dami/deck-releases)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo = if ($env:DECK_REPO) { $env:DECK_REPO } else { 'dev-dami/deck-releases' }
$dir = if ($env:DECK_INSTALL_DIR) { $env:DECK_INSTALL_DIR } else { Join-Path (Join-Path $env:LOCALAPPDATA 'deck') 'bin' }

# Fail stops the install. It throws rather than exits, so a run through
# "irm | iex" does not close the user's PowerShell window.
function Fail($msg) {
    throw "deck install: $msg"
}

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { Fail "unsupported processor $($env:PROCESSOR_ARCHITECTURE)" }
}

# A private repository answers plain downloads with 404; the GitHub CLI, when
# it is installed and signed in, still can.
function Have-Gh {
    if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { return $false }
    # Windows PowerShell turns a native command's stderr into errors, which
    # 'Stop' would throw; run it with errors allowed and read the exit code.
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { & gh auth status *> $null; return $LASTEXITCODE -eq 0 }
    catch { return $false }
    finally { $ErrorActionPreference = $prev }
}

function Gh {
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { & gh @args 2>$null } finally { $ErrorActionPreference = $prev }
}

$tag = $env:DECK_VERSION
if (-not $tag) {
    Write-Host "Finding the latest release of $repo"
    try {
        $latest = Invoke-RestMethod -UseBasicParsing -TimeoutSec 30 -Headers @{ 'User-Agent' = 'deck-install' } "https://api.github.com/repos/$repo/releases/latest"
        $tag = $latest.tag_name
    } catch {
        if (Have-Gh) { $tag = (Gh release view --repo $repo --json tagName -q .tagName) }
        if (-not $tag) { Fail "could not find the latest release of $repo (if it is private, install gh and run 'gh auth login')" }
    }
}
if (-not $tag.StartsWith('v')) { $tag = "v$tag" }
$version = $tag.TrimStart('v')

# Archive names carry build metadata with a "-" in place of "+".
$archive = "deck_$($version.Replace('+', '-'))_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/$([uri]::EscapeDataString($tag))"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("deck-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    Write-Host "Downloading deck $tag for windows/$arch"
    $zip = Join-Path $tmp $archive
    $sums = Join-Path $tmp 'checksums.txt'
    try {
        Invoke-WebRequest -UseBasicParsing -TimeoutSec 600 -Uri "$base/$archive" -OutFile $zip
        Invoke-WebRequest -UseBasicParsing -TimeoutSec 60 -Uri "$base/checksums.txt" -OutFile $sums
    } catch {
        if (-not (Have-Gh)) { Fail "could not download $base/$archive (if the repository is private, install gh and run 'gh auth login')" }
        Remove-Item -Force $zip, $sums -ErrorAction SilentlyContinue
        Gh release download $tag --repo $repo --pattern $archive --pattern checksums.txt --dir $tmp | Out-Null
        if ($LASTEXITCODE -ne 0) { Fail "could not download $archive from $repo $tag" }
    }

    $line = Get-Content $sums | Where-Object { $_ -match (' ' + [regex]::Escape($archive) + '$') } | Select-Object -First 1
    if (-not $line) { Fail "$archive is not in checksums.txt" }
    $want = ($line -split ' ')[0].ToLower()
    $got = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
    if ($got -ne $want) { Fail "checksum mismatch for $archive" }

    $out = Join-Path $tmp 'out'
    Expand-Archive -Path $zip -DestinationPath $out -Force
    $exe = Join-Path $out 'deck.exe'
    if (-not (Test-Path $exe)) { Fail 'the archive has no deck.exe' }

    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Copy-Item -Force $exe (Join-Path $dir 'deck.exe')
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Host "Installed deck $tag to $(Join-Path $dir 'deck.exe')"

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$parts = @()
if ($userPath) { $parts = $userPath -split ';' }
if ($parts -notcontains $dir) {
    $newPath = (@($parts | Where-Object { $_ }) + $dir) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$env:Path;$dir"
    Write-Host "Added $dir to your PATH. Open a new terminal for it to take effect everywhere."
}

Write-Host ''
Write-Host 'Deck drives the agent CLIs you have installed and signed in to: claude, codex, opencode, agy.'
Write-Host "Run 'deck doctor' to see which ones it found, then 'deck' in a project folder."
