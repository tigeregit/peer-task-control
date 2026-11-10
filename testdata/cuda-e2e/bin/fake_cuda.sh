#!/bin/sh
set -e
mkdir -p out/results out/logs tmp
log() { echo "[$(date -Iseconds 2>/dev/null || date)] $*" | tee -a out/logs/run.log; }
log "fake CUDA e2e start mode=${FAKE_CUDA_MODE:-} worktree=${PTC_WORKTREE:-}"
sdk=in/sdk/lib/libfake_cudart.bin
test -f "$sdk" || { log "missing SDK $sdk"; exit 1; }
test -d in/scene || { log "missing scene"; exit 1; }
sdk_bytes=$(wc -c < "$sdk")
scene_files=$(find in/scene -type f | wc -l)
scene_bytes=$(find in/scene -type f -printf '%s\n' | awk '{s+=$1} END {print s+0}')
log "sdk_payload_bytes=$sdk_bytes scene_files=$scene_files scene_bytes=$scene_bytes"
if command -v nvidia-smi >/dev/null 2>&1; then
  gpu=$(nvidia-smi --query-gpu=name,memory.total --format=csv,noheader | tr '\n' ';')
  log "nvidia-smi $gpu"
else
  gpu=none
  log "nvidia-smi not found"
  if [ "${FAKE_CUDA_ALLOW_NO_GPU:-}" != "1" ]; then exit 1; fi
fi
# Touch SDK ends and hash every 256th scene file.
dd if="$sdk" of=/dev/null bs=1M count=16 status=none
dd if="$sdk" of=/dev/null bs=1M count=16 seek=$((sdk_bytes/1048576 - 16)) status=none 2>/dev/null || true
checked=0
i=0
for f in in/scene/*; do
  if [ $((i % 256)) -eq 0 ]; then
    sha256sum "$f" >/dev/null
    checked=$((checked+1))
  fi
  i=$((i+1))
done
dd if=/dev/zero of=out/results/pred.bin bs=1M count=32 status=none
cat > out/results/metrics.json << JSON
{"ok":true,"gpu":"$gpu","sdk_payload_bytes":$sdk_bytes,"scene_files":$scene_files,"scene_bytes":$scene_bytes,"scanned_files":$checked,"pred_bytes":33554432}
JSON
cat > out/results/summary.md << MD
# Fake CUDA scene-a

- GPU: $gpu
- SDK payload: $sdk_bytes bytes
- Scene: $scene_files files, $scene_bytes bytes
- Scanned: $checked files
- Wrote out/results/pred.bin (33554432 bytes)
MD
log "fake CUDA e2e success"
