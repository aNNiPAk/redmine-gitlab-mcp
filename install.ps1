param(
    [ValidateSet('copilot', 'opencode', 'pi', 'zed', 'manual')]
    [string]$Client,
    [ValidateSet('1', '2')]
    [string]$OpenCodeVersion
)

$ErrorActionPreference = 'Stop'

if (-not $Client) {
    Write-Host 'Choose an MCP client:'
    Write-Host '  1. VS Code Chat / GitHub Copilot CLI (Copilot Global)'
    Write-Host '  2. OpenCode (global)'
    Write-Host '  3. Pi (global, built-in MCP)'
    Write-Host '  4. Zed (global)'
    Write-Host '  5. Manual configuration'
    $choice = Read-Host 'Choice [1-5]'
    switch ($choice) {
        '1' { $Client = 'copilot' }
        '2' { $Client = 'opencode' }
        '3' { $Client = 'pi' }
        '4' { $Client = 'zed' }
        '5' { $Client = 'manual' }
        default { throw 'Choose a number from 1 to 5.' }
    }
}
if ($Client -eq 'opencode' -and -not $OpenCodeVersion) {
    $OpenCodeVersion = Read-Host 'OpenCode major version (1 or 2; check opencode --version)'
    if ($OpenCodeVersion -notin @('1', '2')) { throw 'OpenCode version must be 1 or 2.' }
}

$installDir = Join-Path $env:LOCALAPPDATA 'Programs\redmine-gitlab-mcp'
$binary = Join-Path $PSScriptRoot 'redmine-gitlab-mcp.exe'
if (-not (Test-Path -LiteralPath $binary)) { throw "Missing Windows executable: $binary" }
$configHome = if ($env:XDG_CONFIG_HOME) { $env:XDG_CONFIG_HOME } else { Join-Path $env:USERPROFILE '.config' }
$clientConfig = $null
switch ($Client) {
    'copilot' {
        $homeDir = if ($env:COPILOT_HOME) { $env:COPILOT_HOME } else { Join-Path $env:USERPROFILE '.copilot' }
        $clientConfig = Join-Path $homeDir 'mcp-config.json'
    }
    'opencode' {
        $homeDir = Join-Path $configHome 'opencode'
        $json = Join-Path $homeDir 'opencode.json'
        $jsonc = Join-Path $homeDir 'opencode.jsonc'
        if ((Test-Path -LiteralPath $json) -and (Test-Path -LiteralPath $jsonc)) { throw 'Both opencode.json and opencode.jsonc exist. Configure the server manually to avoid conflicting settings.' }
        $clientConfig = if (Test-Path -LiteralPath $jsonc) { $jsonc } else { $json }
    }
    'pi' {
        $homeDir = if ($env:PI_CODING_AGENT_DIR) { $env:PI_CODING_AGENT_DIR } else { Join-Path $env:USERPROFILE '.pi\agent' }
        $clientConfig = Join-Path $homeDir 'mcp.json'
    }
    'zed' { $clientConfig = Join-Path (Join-Path $configHome 'zed') 'settings.json' }
}

# Do not rewrite JSONC comments or trailing commas: editing them through
# ConvertFrom-Json would silently remove user content.
function Assert-PlainJson([string]$text) {
    $inString = $false; $escaped = $false; $last = ''
    for ($i = 0; $i -lt $text.Length; $i++) {
        $c = $text[$i]
        if ($inString) {
            if ($escaped) { $escaped = $false }
            elseif ($c -eq '\') { $escaped = $true }
            elseif ($c -eq '"') { $inString = $false }
            continue
        }
        if ($c -eq '"') { $inString = $true; $last = '"'; continue }
        if ($c -eq '/' -and $i + 1 -lt $text.Length -and ($text[$i + 1] -eq '/' -or $text[$i + 1] -eq '*')) { throw 'Client configuration contains comments. Add the MCP server manually to preserve them.' }
        if (($c -eq '}' -or $c -eq ']') -and $last -eq ',') { throw 'Client configuration contains a trailing comma. Add the MCP server manually to preserve it.' }
        if (-not [char]::IsWhiteSpace($c)) { $last = $c }
    }
}
function Ensure-Object($parent, [string]$key) {
    if (-not $parent.PSObject.Properties[$key]) { $parent | Add-Member -NotePropertyName $key -NotePropertyValue ([pscustomobject]@{}) }
    if ($parent.$key -isnot [pscustomobject]) { throw "Configuration field '$key' must be an object." }
    return $parent.$key
}

$original = $null; $clientExisted = $false; $newConfig = $null
$targetExe = (Join-Path $installDir 'redmine-gitlab-mcp.exe').Replace('\', '/')
if ($clientConfig) {
    $clientExisted = Test-Path -LiteralPath $clientConfig
    if ($clientExisted) {
        $original = [IO.File]::ReadAllText($clientConfig)
        Assert-PlainJson $original
        $config = $original | ConvertFrom-Json
        if ($config -isnot [pscustomobject]) { throw "Configuration must be a JSON object: $clientConfig" }
    } else { $config = [pscustomobject]@{} }
    if ($Client -eq 'opencode') {
        $servers = Ensure-Object $config 'mcp'
        if ($OpenCodeVersion -eq '2') { $servers = Ensure-Object $servers 'servers' }
        $entry = [ordered]@{ type = 'local'; command = @($targetExe) }
    } else {
        $key = if ($Client -eq 'zed') { 'context_servers' } else { 'mcpServers' }
        $servers = Ensure-Object $config $key
        $entry = [ordered]@{ command = $targetExe; args = @() }
    }
    $servers | Add-Member -NotePropertyName 'redmine-gitlab-mcp' -NotePropertyValue $entry -Force
    $newConfig = $config | ConvertTo-Json -Depth 100
}

$installParent = Split-Path $installDir -Parent
New-Item -ItemType Directory -Force -Path $installParent | Out-Null
$suffix = [guid]::NewGuid().ToString('N')
$stage = Join-Path $installParent "redmine-gitlab-mcp.stage-$suffix"
$backup = Join-Path $installParent "redmine-gitlab-mcp.old-$suffix"
New-Item -ItemType Directory -Force -Path $stage | Out-Null
$oldExists = Test-Path -LiteralPath $installDir
$installed = $false; $clientWriteAttempted = $false
$utf8 = New-Object System.Text.UTF8Encoding $false
try {
    Copy-Item -LiteralPath $binary -Destination (Join-Path $stage 'redmine-gitlab-mcp.exe')
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'configure.ps1') -Destination (Join-Path $stage 'configure.ps1')
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'README.md') -Destination (Join-Path $stage 'README.md')
    $existingConfig = Join-Path $installDir 'config.local.json'
    if (Test-Path -LiteralPath $existingConfig) {
        Copy-Item -LiteralPath $existingConfig -Destination (Join-Path $stage 'config.local.json')
    } elseif (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'config.local.json')) {
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'config.local.json') -Destination (Join-Path $stage 'config.local.json')
    } else {
        & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $stage 'configure.ps1') -Staging
        if ($LASTEXITCODE -ne 0) { throw 'Configuration was not created.' }
    }
    $cfg = Get-Content -LiteralPath (Join-Path $stage 'config.local.json') -Encoding UTF8 -Raw | ConvertFrom-Json
    foreach ($field in @('redmineUrl', 'redmineApiKey', 'gitlabUrl', 'gitlabToken', 'gitlabProject')) {
        if (-not $cfg.$field) { throw "Configuration field is missing: $field" }
    }
    & (Join-Path $stage 'redmine-gitlab-mcp.exe') --version | Out-Host
    if ($LASTEXITCODE -ne 0) { throw 'Executable failed to start.' }
    if ($oldExists) { Move-Item -LiteralPath $installDir -Destination $backup }
    try {
        Move-Item -LiteralPath $stage -Destination $installDir
        $installed = $true
        if ($clientConfig) {
            New-Item -ItemType Directory -Force -Path (Split-Path $clientConfig -Parent) | Out-Null
            $clientWriteAttempted = $true
            [IO.File]::WriteAllText($clientConfig, $newConfig + "`n", $utf8)
        }
    } catch {
        if ($installed -and (Test-Path -LiteralPath $installDir)) { Move-Item -LiteralPath $installDir -Destination "$stage.failed" -ErrorAction SilentlyContinue }
        if ($oldExists -and (Test-Path -LiteralPath $backup)) { Move-Item -LiteralPath $backup -Destination $installDir }
        if ($clientWriteAttempted) {
            if ($clientExisted) { [IO.File]::WriteAllText($clientConfig, $original, $utf8) }
            elseif (Test-Path -LiteralPath $clientConfig) { Remove-Item -LiteralPath $clientConfig -Force }
        }
        throw
    }
    Write-Host "Installed: $targetExe"
    Write-Host "Server configuration: $(Join-Path $installDir 'config.local.json')"
    if ($clientConfig) { Write-Host "Client configuration: $clientConfig" }
    if ($oldExists) { Write-Host "Previous installation retained: $backup" }
    if ($Client -eq 'pi') { Write-Host 'Pi requires built-in MCP support (0.99+) or a compatible MCP adapter; reload Pi after setup.' }
    if ($Client -eq 'manual') { Write-Host "Register this stdio command in your MCP client: $targetExe" }
    Write-Host 'Restart the selected client, then use --doctor to check API connectivity.'
} finally {
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
}
