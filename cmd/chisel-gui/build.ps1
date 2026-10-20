param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Architecture = 'amd64'
)
$ErrorActionPreference = 'Stop'
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
Push-Location (Join-Path $PSScriptRoot '../..')
try {
    $env:GOOS = 'windows'
    $env:GOARCH = $Architecture
    $env:CGO_ENABLED = '0'
    New-Item -ItemType Directory -Force -Path 'bin' | Out-Null
    go build -trimpath -ldflags '-H=windowsgui -s -w' -o "bin/chisel-gui-windows-$Architecture.exe" ./cmd/chisel-gui
    if ($LASTEXITCODE -ne 0) { throw 'GUI build failed' }
    Write-Host "Built bin/chisel-gui-windows-$Architecture.exe"
} finally {
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGO
    Pop-Location
}
