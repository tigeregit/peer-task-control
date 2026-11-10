# Segmented P0 Docker mesh test. Run from the repo root on iamsbb2:
#   powershell -File docker/p0-test.ps1
$ErrorActionPreference = "Stop"
$Repo = Split-Path -Parent $PSScriptRoot
Set-Location $Repo

function Write-Phase($title) {
    Write-Host ""
    Write-Host "==== $title ===="
}
function Pass($msg) { Write-Host "PASS: $msg" }
function Fail($msg) { Write-Host "FAIL: $msg"; throw $msg }

$compose = @("docker", "compose", "-f", "docker/docker-compose.yml")

function Invoke-In($service, $args) {
    & docker compose -f docker/docker-compose.yml exec -T $service @args
    return $LASTEXITCODE
}

Write-Phase "P0-C.0 构建 Linux 二进制并启动两网段 + 网关"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
$env:GOPROXY = "https://goproxy.cn,direct"
& go build -o docker/ptc-linux ./cmd/ptc
if ($LASTEXITCODE -ne 0) { Fail "go build ptc-linux" }
& go build -o docker/tcpprobe-linux ./docker/cmd/tcpprobe
if ($LASTEXITCODE -ne 0) { Fail "go build tcpprobe-linux" }
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue

& docker compose -f docker/docker-compose.yml down -v --remove-orphans | Out-Host
& docker compose -f docker/docker-compose.yml build --quiet | Out-Host
if ($LASTEXITCODE -ne 0) { Fail "docker compose build" }
& docker compose -f docker/docker-compose.yml up -d | Out-Host
if ($LASTEXITCODE -ne 0) { Fail "docker compose up" }

try {
    Write-Phase "P0-C.1 网段隔离（计算节点不能直连另一网段）"
    & docker compose -f docker/docker-compose.yml exec -T worker-a tcpprobe 10.87.1.10:18765
    if ($LASTEXITCODE -eq 0) { Fail "worker-a should not reach worker-b" }
    & docker compose -f docker/docker-compose.yml exec -T worker-b tcpprobe 10.87.0.10:18765
    if ($LASTEXITCODE -eq 0) { Fail "worker-b should not reach worker-a" }
    Pass "worker-a and worker-b cannot connect across segments"

    Write-Phase "P0-C.2 网关可达（白名单网关能拨两个网段）"
    & docker compose -f docker/docker-compose.yml exec -T gateway tcpprobe 10.87.0.10:18765
    if ($LASTEXITCODE -ne 0) { Fail "gateway cannot reach worker-a" }
    & docker compose -f docker/docker-compose.yml exec -T gateway tcpprobe 10.87.1.10:18765
    if ($LASTEXITCODE -ne 0) { Fail "gateway cannot reach worker-b" }
    Pass "gateway reaches both workers"

    Write-Phase "P0-C.3 等待 daemon API"
    foreach ($svc in @("worker-a", "worker-b", "gateway")) {
        $up = $false
        for ($i = 0; $i -lt 30; $i++) {
            & docker compose -f docker/docker-compose.yml exec -T $svc ptc peers --data-dir /data | Out-Null
            if ($LASTEXITCODE -eq 0) { $up = $true; break }
            Start-Sleep -Seconds 1
        }
        if (-not $up) { Fail "$svc API did not come up" }
    }
    Pass "all three daemon APIs are up"

    Write-Phase "P0-C.4 经网关发现完整邻居目录"
    $ok = $false
    for ($i = 0; $i -lt 45; $i++) {
        $ja = & docker compose -f docker/docker-compose.yml exec -T worker-a ptc peers --data-dir /data --json 2>$null
        $jb = & docker compose -f docker/docker-compose.yml exec -T worker-b ptc peers --data-dir /data --json 2>$null
        $jg = & docker compose -f docker/docker-compose.yml exec -T gateway ptc peers --data-dir /data --json 2>$null
        if (($ja -match '"name": "worker-b"') -and ($ja -match '"name": "gateway"') -and
            ($jb -match '"name": "worker-a"') -and ($jb -match '"name": "gateway"') -and
            ($jg -match '"name": "worker-a"') -and ($jg -match '"name": "worker-b"')) {
            $ok = $true
            break
        }
        Start-Sleep -Seconds 2
    }
    if (-not $ok) {
        & docker compose -f docker/docker-compose.yml exec -T worker-a ptc peers --data-dir /data | Out-Host
        Fail "catalog did not converge"
    }
    Pass "each node sees the other two via the gateway catalog"

    Write-Phase "P0-C.5 角色：网关无 GPU 不可执行，计算节点可执行"
    function Get-PeerMap($svc) {
        $raw = & docker compose -f docker/docker-compose.yml exec -T $svc ptc peers --data-dir /data --json
        if ($LASTEXITCODE -ne 0) { Fail "$svc ptc peers failed" }
        $st = $raw | ConvertFrom-Json
        $map = @{}
        foreach ($e in $st.peers) { $map[$e.name] = $e }
        return $map
    }
    $a = Get-PeerMap worker-a
    $b = Get-PeerMap worker-b
    $g = Get-PeerMap gateway
    foreach ($pair in @(@("worker-a", $a), @("worker-b", $b), @("gateway", $g))) {
        $label = $pair[0]; $book = $pair[1]
        foreach ($n in @("worker-a", "worker-b", "gateway")) {
            if (-not $book.ContainsKey($n)) { Fail "$label catalog missing $n" }
        }
        Pass "$label catalog has all three nodes"
    }
    if (-not $a["worker-a"].services.exec.offered) { Fail "worker-a should offer exec (mocked NVIDIA GPU)" }
    Pass "worker-a offers exec (mocked NVIDIA GPU)"
    if (-not $b["worker-b"].services.exec.offered) { Fail "worker-b should offer exec (mocked NVIDIA GPU)" }
    Pass "worker-b offers exec (mocked NVIDIA GPU)"
    if ($g["gateway"].services.exec.offered) { Fail "gateway must not offer exec" }
    Pass "gateway does not offer exec (no NVIDIA GPU / exec disabled)"
    if ($g["gateway"].services.exec.enabled) { Fail "gateway exec should be switched off" }
    Pass "gateway has exec switched off"
    if (-not $g["gateway"].services.relay.offered) { Fail "gateway should offer relay" }
    Pass "gateway offers relay"
    if (-not $g["gateway"].services.files.offered) { Fail "gateway should offer files" }
    Pass "gateway offers files"
    if ($a["worker-a"].services.relay.offered) { Fail "worker-a should not offer relay" }
    Pass "worker-a does not offer relay"
    if ($b["worker-b"].services.relay.offered) { Fail "worker-b should not offer relay" }
    Pass "worker-b does not offer relay"
    $reason = [string]$g["gateway"].services.exec.reason
    if ([string]::IsNullOrWhiteSpace($reason)) { Fail "gateway exec refusal should include a reason" }
    Pass "gateway exec refusal has a reason: $reason"

    Write-Host ""
    Write-Host "P0-C Docker network simulation passed."
}
finally {
    if ($env:PTC_KEEP_STACK -ne "1") {
        & docker compose -f docker/docker-compose.yml down -v --remove-orphans | Out-Host
    }
}
