$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path out/results, out/logs, tmp | Out-Null
$log = "out/logs/run.log"
function Log($m) {
  $line = "[{0}] {1}" -f (Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"), $m
  Add-Content -Path $log -Value $line
  Write-Host $line
}

Log "fake CUDA e2e start mode=$env:FAKE_CUDA_MODE worktree=$env:PTC_WORKTREE"

$sdkPayload = "in/sdk/lib/libfake_cudart.bin"
$sceneDir = "in/scene"
if (-not (Test-Path $sdkPayload)) { throw "missing SDK payload $sdkPayload" }
if (-not (Test-Path $sceneDir)) { throw "missing scene $sceneDir" }

$sdkBytes = (Get-Item $sdkPayload).Length
$sceneFiles = @(Get-ChildItem $sceneDir -File)
$sceneBytes = ($sceneFiles | Measure-Object -Property Length -Sum).Sum
Log "sdk_payload_bytes=$sdkBytes scene_files=$($sceneFiles.Count) scene_bytes=$sceneBytes"

$linkCount = 0
$probe = $sceneFiles[0].FullName
try {
  $links = fsutil hardlink list $probe 2>$null
  if ($links) { $linkCount = @($links).Count }
} catch { $linkCount = 0 }
Log "hardlink_probe=$($sceneFiles[0].Name) count=$linkCount"

$gpu = "none"
$smi = Get-Command nvidia-smi -ErrorAction SilentlyContinue
if ($smi) {
  $gpu = (& nvidia-smi --query-gpu=name,memory.total --format=csv,noheader).Trim()
  Log "nvidia-smi $gpu"
} else {
  Log "nvidia-smi not found"
  if ($env:FAKE_CUDA_ALLOW_NO_GPU -ne "1") { throw "nvidia-smi not found" }
}

# Read a slice of the SDK and every 256th scene file so the job touches large data
# without spending minutes hashing the full 4 GiB.
$sw = [System.Diagnostics.Stopwatch]::StartNew()
$fs = [System.IO.File]::OpenRead((Resolve-Path $sdkPayload))
try {
  $buf = New-Object byte[] (16MB)
  [void]$fs.Read($buf, 0, $buf.Length)
  $tail = [int64]$buf.Length
  if ($tail -gt $fs.Length) { $tail = $fs.Length }
  [void]$fs.Seek(-$tail, 'End')
  [void]$fs.Read($buf, 0, [int]$tail)
} finally { $fs.Close() }

$sha = [System.Security.Cryptography.SHA256]::Create()
$checked = 0
$checkedBytes = [int64]0
for ($i = 0; $i -lt $sceneFiles.Count; $i += 256) {
  $hash = $sha.ComputeHash([System.IO.File]::ReadAllBytes($sceneFiles[$i].FullName))
  $checked++
  $checkedBytes += $sceneFiles[$i].Length
}
$sw.Stop()
Log ("kernel_scan files={0} bytes={1} elapsed_ms={2}" -f $checked, $checkedBytes, $sw.ElapsedMilliseconds)

# A stand-in "prediction" artifact (~32 MiB) plus a text summary.
$pred = New-Object byte[] (32MB)
$rng = [System.Random]::new(0xC0DA)
$rng.NextBytes($pred)
[System.IO.File]::WriteAllBytes((Join-Path "out/results" "pred.bin"), $pred)

$metrics = @{
  ok = $true
  gpu = $gpu
  sdk_payload_bytes = $sdkBytes
  scene_files = $sceneFiles.Count
  scene_bytes = $sceneBytes
  hardlink_count = $linkCount
  scanned_files = $checked
  scanned_bytes = $checkedBytes
  kernel_ms = $sw.ElapsedMilliseconds
  pred_bytes = $pred.Length
} | ConvertTo-Json
Set-Content -Path out/results/metrics.json -Value $metrics

@(
  "# Fake CUDA scene-a",
  "",
  "- GPU: $gpu",
  "- SDK payload: $sdkBytes bytes",
  "- Scene: $($sceneFiles.Count) files, $sceneBytes bytes",
  "- Hardlink count for first scene file: $linkCount",
  "- Scanned: $checked files ($checkedBytes bytes) in $($sw.ElapsedMilliseconds) ms",
  "- Wrote out/results/pred.bin ($($pred.Length) bytes)"
) -join "`n" | Set-Content -Path out/results/summary.md

Log "fake CUDA e2e success"
