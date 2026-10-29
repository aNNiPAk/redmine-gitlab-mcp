param([switch]$Staging)

$ErrorActionPreference = 'Stop'

function Read-SecretText([string]$Prompt) {
    $secure = Read-Host $Prompt -AsSecureString
    $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try { [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr) }
}

$redmineUrl = (Read-Host 'Redmine URL').Trim().TrimEnd('/')
$gitlabUrl = (Read-Host 'GitLab URL').Trim().TrimEnd('/')
$redmineApiKey = Read-SecretText 'Redmine API key'
$gitlabToken = Read-SecretText 'GitLab personal access token'
$gitlabProject = (Read-Host 'Default GitLab project ID or full namespace/path').Trim()
if (-not $gitlabProject) { throw 'Default GitLab project is required.' }

$gitlabProjects = [ordered]@{}
Write-Host 'Optional project aliases. Short names use the default project namespace when available.'
while ($true) {
    $alias = (Read-Host 'Project alias (press Enter to finish)').Trim()
    if (-not $alias) { break }
    if ($alias -cnotmatch '^[a-zA-Z0-9][a-zA-Z0-9_.-]*$') { throw "Invalid alias: $alias" }
    $project = (Read-Host "GitLab project ID or full namespace/path for $alias").Trim()
    if (-not $project) { throw "Missing project ID/path for $alias" }
    $gitlabProjects[$alias] = $project
}

$config = [ordered]@{
    redmineUrl = $redmineUrl
    redmineApiKey = $redmineApiKey
    gitlabUrl = $gitlabUrl
    gitlabToken = $gitlabToken
    gitlabProject = $gitlabProject
}
if ($gitlabProjects.Count) { $config.gitlabProjects = $gitlabProjects }

$destination = Join-Path $PSScriptRoot 'config.local.json'
$config | ConvertTo-Json -Depth 4 | Set-Content -Path $destination -Encoding UTF8
if ($Staging) { Write-Host 'Configuration prepared.' }
else { Write-Host "Configuration created: $destination" }
Write-Host 'File contains plaintext secrets: do not commit it or share it.'
