# Start or stop the native Windows ptc node that joins a Docker mesh.
# Usage (from the repo root):
#   .\docker\run-host-node.ps1              # deploy mesh (docker/config/host.yaml)
#   .\docker\run-host-node.ps1 -Stop
#   .\docker\run-host-node.ps1 -TestMesh    # P0-C test mesh (10.87.x)
#   .\docker\run-host-node.ps1 -TestMesh -Stop
param(
    [switch]$Stop,
    [switch]$TestMesh
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
if (-not $Root) { $Root = (Resolve-Path "$PSScriptRoot\..").Path }
$Exe = Join-Path $Root "docker\ptc-windows.exe"
if (-not (Test-Path $Exe)) { $Exe = Join-Path $Root "ptc.exe" }
if ($TestMesh) {
    $Config = Join-Path $Root "docker\test\testdata\host.yaml"
    $DataDir = Join-Path $Root "docker\test\testdata\host-data"
} else {
    $Config = Join-Path $Root "docker\config\host.yaml"
    $DataDir = Join-Path $Root "docker\data\host"
}
$OutLog = Join-Path $DataDir "daemon.out.log"
$ErrLog = Join-Path $DataDir "daemon.err.log"
$PidFile = Join-Path $DataDir "daemon.pid"

function Get-HostPid {
    if (Test-Path $PidFile) {
        $raw = (Get-Content $PidFile -ErrorAction SilentlyContinue | Select-Object -First 1)
        $p = 0
        if ([int]::TryParse($raw, [ref]$p) -and $p -gt 0) { return $p }
    }
    return 0
}

if ($Stop) {
    $p = Get-HostPid
    if ($p -gt 0) {
        try { Stop-Process -Id $p -Force -ErrorAction Stop; Write-Host "stopped host node pid $p" }
        catch { Write-Host "host node pid $p was not running" }
    } else {
        Write-Host "no host node pid file"
    }
    Remove-Item $PidFile -ErrorAction SilentlyContinue
    exit 0
}

if (-not (Test-Path $Exe)) { throw "missing $Exe — build with: go build -o docker/ptc-windows.exe ./cmd/ptc" }
if (-not (Test-Path $Config)) { throw "missing $Config" }
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

$existing = Get-HostPid
if ($existing -gt 0) {
    $proc = Get-Process -Id $existing -ErrorAction SilentlyContinue
    if ($proc) {
        Write-Host "host node already running as pid $existing"
        exit 0
    }
}

$ruleName = "ptc overlay 18765"
if (-not (Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue)) {
    try {
        New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Protocol TCP -LocalPort 18765 -Action Allow | Out-Null
        Write-Host "added firewall rule '$ruleName'"
    } catch {
        Write-Host "could not add firewall rule (may need elevation): $($_.Exception.Message)"
    }
}

$proc = Start-Process -FilePath $Exe -ArgumentList @("daemon","--config",$Config,"--data-dir",$DataDir) `
    -RedirectStandardOutput $OutLog -RedirectStandardError $ErrLog -WorkingDirectory $Root -WindowStyle Hidden -PassThru
Set-Content -Path $PidFile -Value $proc.Id -Encoding ascii
Write-Host "started host node pid $($proc.Id)"
Write-Host "api  http://127.0.0.1:18780"
Write-Host "log  $OutLog"
