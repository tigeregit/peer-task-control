$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path out/results, out/logs | Out-Null
$scene = (Get-Content -Raw -Path in/scene/input.txt).Trim()
Set-Content -Path out/results/summary.md -Value "ok scene=$scene"
Set-Content -Path out/results/metrics.json -Value '{"ok":true}'
Set-Content -Path out/logs/run.log -Value ("ran at " + (Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"))
