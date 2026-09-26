param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Architecture = 'amd64'
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$savedEnvironment = @{}
foreach ($name in @('CGO_ENABLED', 'GOOS', 'GOARCH')) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}

Push-Location -LiteralPath $repoRoot
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'linux'
    $env:GOARCH = $Architecture
    $artifactName = "sepiida-agent-linux-$Architecture"
    $artifactPath = Join-Path $repoRoot "bin/$artifactName"
    New-Item -ItemType Directory -Path (Join-Path $repoRoot 'bin') -Force | Out-Null
    & go build -trimpath -buildvcs=true '-ldflags=-s -w' -o $artifactPath ./cmd/agent
    if ($LASTEXITCODE -ne 0) { throw "Agent build failed with exit code $LASTEXITCODE" }
    $digest = (Get-FileHash -LiteralPath $artifactPath -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText("$artifactPath.sha256", "$digest  $artifactName`n", [Text.Encoding]::ASCII)
    Write-Output "Built: $artifactPath"
    Write-Output "SHA256: $digest"
}
finally {
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
    }
    Pop-Location
}
