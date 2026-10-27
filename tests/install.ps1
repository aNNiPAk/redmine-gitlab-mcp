param([Parameter(Mandatory = $true)][string]$Binary)
$ErrorActionPreference = 'Stop'
$root = Join-Path ([IO.Path]::GetTempPath()) ("redmine-gitlab-mcp-test-" + [guid]::NewGuid().ToString('N'))
$saved = @{}
foreach ($key in @('LOCALAPPDATA', 'USERPROFILE', 'XDG_CONFIG_HOME', 'COPILOT_HOME', 'PI_CODING_AGENT_DIR')) { $saved[$key] = [Environment]::GetEnvironmentVariable($key) }
try {
    New-Item -ItemType Directory -Force -Path $root | Out-Null
    $package = Join-Path $root 'package'
    New-Item -ItemType Directory -Force -Path $package | Out-Null
    Copy-Item -LiteralPath $Binary -Destination (Join-Path $package 'redmine-gitlab-mcp.exe')
    Copy-Item -LiteralPath 'install.ps1', 'configure.ps1', 'README.md' -Destination $package
    [IO.File]::WriteAllText((Join-Path $package 'config.local.json'), '{"redmineUrl":"https://example.test","redmineApiKey":"test","gitlabUrl":"https://example.test","gitlabToken":"test","gitlabProject":"test"}')
    $env:USERPROFILE = Join-Path $root 'home'
    $env:LOCALAPPDATA = Join-Path $root 'local'
    $env:XDG_CONFIG_HOME = Join-Path $root 'xdg'
    $env:COPILOT_HOME = Join-Path $root 'copilot'
    $env:PI_CODING_AGENT_DIR = Join-Path $root 'pi'
    $installer = Join-Path $package 'install.ps1'
    foreach ($case in @(
        @{ Client = 'copilot'; Path = (Join-Path $env:COPILOT_HOME 'mcp-config.json'); Key = 'mcpServers' },
        @{ Client = 'opencode'; Version = '1'; Path = (Join-Path $env:XDG_CONFIG_HOME 'opencode/opencode.json'); Key = 'mcp' },
        @{ Client = 'pi'; Path = (Join-Path $env:PI_CODING_AGENT_DIR 'mcp.json'); Key = 'mcpServers' },
        @{ Client = 'zed'; Path = (Join-Path $env:XDG_CONFIG_HOME 'zed/settings.json'); Key = 'context_servers' }
    )) {
        New-Item -ItemType Directory -Force -Path (Split-Path $case.Path -Parent) | Out-Null
        [IO.File]::WriteAllText($case.Path, '{"keep":"https://example.test","other":[1,2]}')
        $argsList = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $installer, '-Client', $case.Client)
        if ($case.Version) { $argsList += @('-OpenCodeVersion', $case.Version) }
        & powershell.exe @argsList
        if ($LASTEXITCODE -ne 0) { throw "Installer failed for $($case.Client)" }
        $actual = Get-Content -Raw -LiteralPath $case.Path | ConvertFrom-Json
        if ($actual.keep -ne 'https://example.test' -or $actual.other.Count -ne 2) { throw 'Existing settings lost' }
        $entry = $actual.($case.Key).'redmine-gitlab-mcp'
        if (-not $entry) { throw "Missing entry for $($case.Client)" }
        if ($case.Client -eq 'opencode') { if ($entry.type -ne 'local' -or $entry.command.Count -ne 1) { throw 'Invalid OpenCode v1 entry' } }
        elseif (-not $entry.command -or $entry.args.Count -ne 0) { throw 'Invalid stdio entry' }
    }
    $opencode = Join-Path $env:XDG_CONFIG_HOME 'opencode/opencode.json'
    [IO.File]::WriteAllText($opencode, '{"mcp":{"servers":{"other":{"type":"local","command":["other"]}}}}')
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $installer -Client opencode -OpenCodeVersion 2
    if ($LASTEXITCODE -ne 0) { throw 'Installer failed for OpenCode v2' }
    $actual = Get-Content -Raw -LiteralPath $opencode | ConvertFrom-Json
    if (-not $actual.mcp.servers.'redmine-gitlab-mcp' -or -not $actual.mcp.servers.other) { throw 'Invalid OpenCode v2 entry' }
    $jsonc = "{`n // retain this comment`n `"x`": 1`n}"
    [IO.File]::WriteAllText($opencode, $jsonc)
    $ErrorActionPreference = 'Continue'
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $installer -Client opencode -OpenCodeVersion 2 2>$null
    $ErrorActionPreference = 'Stop'
    if ($LASTEXITCODE -eq 0 -or [IO.File]::ReadAllText($opencode) -ne $jsonc) { throw 'JSONC was not protected' }
    Write-Host 'Installer tests passed.'
} finally {
    foreach ($key in $saved.Keys) { [Environment]::SetEnvironmentVariable($key, $saved[$key]) }
    if (Test-Path -LiteralPath $root) {
        $actualRoot = (Resolve-Path -LiteralPath $root).Path
        $tempRoot = (Resolve-Path -LiteralPath ([IO.Path]::GetTempPath())).Path.TrimEnd('\') + '\'
        if (-not $actualRoot.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Unexpected test path' }
        Remove-Item -LiteralPath $actualRoot -Recurse -Force
    }
}
