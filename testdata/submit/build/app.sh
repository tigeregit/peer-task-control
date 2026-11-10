#!/bin/sh
set -e
mkdir -p out/results out/logs
echo "ok scene=$(cat in/scene/input.txt)" > out/results/summary.md
echo '{"ok":true}' > out/results/metrics.json
echo "ran at $(date -Iseconds 2>/dev/null || date)" > out/logs/run.log
