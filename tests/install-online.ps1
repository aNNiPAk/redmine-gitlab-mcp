$ErrorActionPreference = 'Stop'
$root = Join-Path ([IO.Path]::GetTempPath()) ("redmine-gitlab-mcp-online-test-" + [guid]::NewGuid().ToString('N'))
$oldMarker = $env:REDMINE_GITLAB_MCP_TEST_MARKER
$oldArchive = $env:REDMINE_GITLAB_MCP_TEST_ARCHIVE
$oldBadHash = $env:REDMINE_GITLAB_MCP_TEST_BAD_HASH
$oldHash = $env:REDMINE_GITLAB_MCP_TEST_HASH
try {
    New-Item -ItemType Directory -Path $root | Out-Null
    $fixture = Join-Path $root 'fixture'
    New-Item -ItemType Directory -Path $fixture | Out-Null
    $env:REDMINE_GITLAB_MCP_TEST_MARKER = Join-Path $root 'installed'
    [IO.File]::WriteAllText((Join-Path $fixture 'install.ps1'), '[IO.File]::WriteAllText($env:REDMINE_GITLAB_MCP_TEST_MARKER, "installed")')
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $env:REDMINE_GITLAB_MCP_TEST_ARCHIVE = Join-Path $root 'fixture.zip'
    [IO.Compression.ZipFile]::CreateFromDirectory($fixture, $env:REDMINE_GITLAB_MCP_TEST_ARCHIVE)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($env:REDMINE_GITLAB_MCP_TEST_ARCHIVE)
    try { $env:REDMINE_GITLAB_MCP_TEST_HASH = [BitConverter]::ToString($sha256.ComputeHash($stream)).Replace('-', '') }
    finally { $stream.Dispose(); $sha256.Dispose() }
    $env:REDMINE_GITLAB_MCP_TEST_BAD_HASH = '0'
    function Invoke-WebRequest {
        param($Uri, $OutFile, [switch]$UseBasicParsing)
        if ($Uri.EndsWith('/redmine-gitlab-mcp-windows-amd64.zip')) {
            Copy-Item -LiteralPath $env:REDMINE_GITLAB_MCP_TEST_ARCHIVE -Destination $OutFile
        } elseif ($Uri.EndsWith('/SHA256SUMS')) {
            $hash = if ($env:REDMINE_GITLAB_MCP_TEST_BAD_HASH -eq '1') { '0' * 64 } else { $env:REDMINE_GITLAB_MCP_TEST_HASH }
            [IO.File]::WriteAllText($OutFile, "$hash  redmine-gitlab-mcp-windows-amd64.zip`n")
        } else { throw "Unexpected URL: $Uri" }
    }
    & (Join-Path $PSScriptRoot '../install-online.ps1')
    if (-not (Test-Path -LiteralPath $env:REDMINE_GITLAB_MCP_TEST_MARKER)) { throw 'Installer was not executed.' }
    Remove-Item -LiteralPath $env:REDMINE_GITLAB_MCP_TEST_MARKER
    $env:REDMINE_GITLAB_MCP_TEST_BAD_HASH = '1'
    $rejected = $false
    try { & (Join-Path $PSScriptRoot '../install-online.ps1') }
    catch { if ($_.Exception.Message -match 'checksum does not match') { $rejected = $true } else { throw } }
    if (-not $rejected -or (Test-Path -LiteralPath $env:REDMINE_GITLAB_MCP_TEST_MARKER)) { throw 'Invalid checksum was not rejected before execution.' }
    Write-Host 'Online installer tests passed.'
} finally {
    $env:REDMINE_GITLAB_MCP_TEST_MARKER = $oldMarker
    $env:REDMINE_GITLAB_MCP_TEST_ARCHIVE = $oldArchive
    $env:REDMINE_GITLAB_MCP_TEST_BAD_HASH = $oldBadHash
    $env:REDMINE_GITLAB_MCP_TEST_HASH = $oldHash
    if (Test-Path -LiteralPath $root) {
        $actualRoot = (Resolve-Path -LiteralPath $root).Path
        $tempRoot = (Resolve-Path -LiteralPath ([IO.Path]::GetTempPath())).Path.TrimEnd('\') + '\'
        if (-not $actualRoot.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Unexpected test path.' }
        Remove-Item -LiteralPath $actualRoot -Recurse -Force
    }
}
