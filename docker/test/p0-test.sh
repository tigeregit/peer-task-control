#!/usr/bin/env sh
# Segmented P0 mesh test against the docker/test compose topology.
# Run from the repo root after the stack is up:
#   sh docker/test/p0-test.sh
set -eu
COMPOSE="${COMPOSE:-docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.p0-test.yml}"
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
phase() { echo; echo "==== $* ===="; }

exec_in() {
  service=$1
  shift
  $COMPOSE exec -T "$service" "$@"
}

wait_api() {
  service=$1
  i=0
  while [ "$i" -lt 30 ]; do
    if exec_in "$service" ptc peers --data-dir /data >/dev/null 2>&1; then
      return 0
    fi
    i=$((i + 1))
    sleep 1
  done
  fail "$service API did not come up"
}

peers_json() {
  exec_in "$1" ptc peers --data-dir /data --json
}

phase "P0-C.1 网段隔离（计算节点不能直连另一网段）"
if exec_in worker-a tcpprobe 10.87.1.10:18765; then
  fail "worker-a should not reach worker-b (10.87.1.10:18765)"
fi
if exec_in worker-b tcpprobe 10.87.0.10:18765; then
  fail "worker-b should not reach worker-a (10.87.0.10:18765)"
fi
pass "worker-a and worker-b cannot connect across segments"

phase "P0-C.2 网关可达（白名单网关能拨两个网段）"
exec_in gateway tcpprobe 10.87.0.10:18765 || fail "gateway cannot reach worker-a"
exec_in gateway tcpprobe 10.87.1.10:18765 || fail "gateway cannot reach worker-b"
pass "gateway reaches both workers"

phase "P0-C.3 等待 daemon API"
wait_api worker-a
wait_api worker-b
wait_api gateway
pass "all three daemon APIs are up"

phase "P0-C.4 经网关发现完整邻居目录"
ok=0
i=0
while [ "$i" -lt 45 ]; do
  ja=$(peers_json worker-a || true)
  jb=$(peers_json worker-b || true)
  jg=$(peers_json gateway || true)
  echo "$ja" | grep -q '"name": "worker-b"' && echo "$ja" | grep -q '"name": "gateway"' && ja_ok=1 || ja_ok=0
  echo "$jb" | grep -q '"name": "worker-a"' && echo "$jb" | grep -q '"name": "gateway"' && jb_ok=1 || jb_ok=0
  echo "$jg" | grep -q '"name": "worker-a"' && echo "$jg" | grep -q '"name": "worker-b"' && jg_ok=1 || jg_ok=0
  if [ "${ja_ok:-0}" -eq 1 ] && [ "${jb_ok:-0}" -eq 1 ] && [ "${jg_ok:-0}" -eq 1 ]; then
    ok=1
    break
  fi
  i=$((i + 1))
  sleep 2
done
if [ "$ok" -ne 1 ]; then
  echo "worker-a:" >&2
  peers_json worker-a >&2 || true
  echo "worker-b:" >&2
  peers_json worker-b >&2 || true
  echo "gateway:" >&2
  peers_json gateway >&2 || true
  fail "catalog did not converge"
fi
pass "each node sees the other two via the gateway catalog"

phase "P0-C.5 角色：网关无 NVIDIA、不可作为执行目标；计算节点可执行"
ja=$(peers_json worker-a)
table=$(exec_in worker-a ptc peers --data-dir /data)
echo "$table"
echo "$table" | grep -E '^gateway' | grep -q 'no (' || echo "$table" | grep gateway | grep -qi 'no' || fail "gateway exec should be advertised as unavailable"
echo "$table" | grep -E '^worker-a' | grep -q 'yes' || fail "worker-a exec should be offered (mocked NVIDIA GPU)"
echo "$table" | grep -E '^worker-b' | grep -q 'yes' || fail "worker-b exec should be offered (mocked NVIDIA GPU)"
pass "roles look correct in ptc peers (see table above); JSON double-check follows"
echo "(JSON role assertions are applied by docker/test/p0-test.ps1 or docker/test/p0-assert.py)"

echo
echo "P0-C network simulation finished."
