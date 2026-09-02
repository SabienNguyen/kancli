# Install kancli on Windows.
#
#   irm https://raw.githubusercontent.com/SabienNguyen/kancli/main/install.ps1 | iex
#
# Downloads the latest release binary into %LOCALAPPDATA%\kancli\bin and
# adds it to your user PATH. Falls back to `go install` when there is no
# release. Set $env:KANCLI_VERSION to pin a release tag.
$ErrorActionPreference = 'Stop'
$repo = 'SabienNguyen/kancli'
$binDir = Join-Path $env:LOCALAPPDATA 'kancli\bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null

$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }
$version = $env:KANCLI_VERSION
if (-not $version) {
  try { $version = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name } catch { $version = $null }
}

$installed = $false
if ($version) {
  $file = "kancli_$($version.TrimStart('v'))_windows_$arch.zip"
  $url = "https://github.com/$repo/releases/download/$version/$file"
  $tmp = Join-Path ([System.IO.Path]::GetTempPath()) $file
  try {
    Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing
    Expand-Archive -Path $tmp -DestinationPath $binDir -Force
    Remove-Item $tmp -Force
    Write-Host "installed kancli $version to $binDir\kancli.exe"
    $installed = $true
  } catch {
    Write-Warning "no release asset for windows/$arch at $version; falling back to go install"
  }
}
if (-not $installed) {
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error "no release binary available and Go is not installed. Install Go from https://go.dev/dl/ and rerun, or clone the repo and run 'go build ./cmd/kancli'."
  }
  $env:GOBIN = $binDir
  go install "github.com/$repo/cmd/kancli@latest"
  Write-Host "installed kancli to $binDir\kancli.exe"
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $binDir) {
  [Environment]::SetEnvironmentVariable('Path', "$binDir;$userPath", 'User')
  Write-Host "added $binDir to your user PATH; open a new terminal to use it"
}
Write-Host "run: kancli -demo"
