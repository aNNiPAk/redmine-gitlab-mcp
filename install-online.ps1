$ErrorActionPreference = 'Stop'

# GitHub's latest release supplies both the package and its checksum.
$releaseUrl = 'https://github.com/aNNiPAk/redmine-gitlab-mcp/releases/latest/download'
$archiveName = 'redmine-gitlab-mcp-windows-amd64.zip'
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ("redmine-gitlab-mcp-download-" + [guid]::NewGuid().ToString('N'))

try {
    New-Item -ItemType Directory -Path $tempDir | Out-Null
    $archive = Join-Path $tempDir $archiveName
    $checksums = Join-Path $tempDir 'SHA256SUMS'
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    Write-Host 'Downloading the latest release...'
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseUrl/$archiveName" -OutFile $archive
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseUrl/SHA256SUMS" -OutFile $checksums

    $matches = @(Get-Content -LiteralPath $checksums | Where-Object { $_ -match '^([a-fA-F0-9]{64})\s+\*?redmine-gitlab-mcp-windows-amd64\.zip$' })
    if ($matches.Count -ne 1) { throw 'SHA256SUMS must contain exactly one checksum for the release archive.' }
    $expectedHash = ($matches[0] -split '\s+')[0]
    $sha256 = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($archive)
    try { $actualHash = [BitConverter]::ToString($sha256.ComputeHash($stream)).Replace('-', '') }
    finally { $stream.Dispose(); $sha256.Dispose() }
    if ($actualHash -ne $expectedHash) { throw 'Release archive checksum does not match SHA256SUMS.' }

    $package = Join-Path $tempDir 'package'
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::ExtractToDirectory($archive, $package)
    $installer = Join-Path $package 'install.ps1'
    if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) { throw 'Release archive is missing install.ps1.' }
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $installer
    if ($LASTEXITCODE -ne 0) { throw "Installation failed (exit code $LASTEXITCODE)." }
} finally {
    if (Test-Path -LiteralPath $tempDir) { Remove-Item -LiteralPath $tempDir -Recurse -Force }
}
