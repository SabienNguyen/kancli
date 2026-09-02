# Build kancli from this checkout and put it on your PATH (Windows).
#
#   git clone https://github.com/SabienNguyen/kancli.git
#   cd kancli
#   .\install.ps1
#
# Installs to %LOCALAPPDATA%\kancli\bin and adds that to your user PATH.
# Needs Go (winget install GoLang.Go, or https://go.dev/dl/).
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  Write-Error "Go is not installed. Run 'winget install GoLang.Go' (or see https://go.dev/dl/) and rerun."
}
$binDir = if ($env:KANCLI_BINDIR) { $env:KANCLI_BINDIR } else { Join-Path $env:LOCALAPPDATA 'kancli\bin' }
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$version = try { (git describe --tags --always --dirty 2>$null) } catch { 'dev' }
if (-not $version) { $version = 'dev' }
Write-Host "building kancli $version ..."
go build -ldflags "-s -w -X main.version=$version" -o (Join-Path $binDir 'kancli.exe') ./cmd/kancli
Write-Host "installed $binDir\kancli.exe"

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $binDir) {
  [Environment]::SetEnvironmentVariable('Path', "$binDir;$userPath", 'User')
  Write-Host "added $binDir to your user PATH (open a new terminal)"
}
Write-Host "try it: kancli -demo"
