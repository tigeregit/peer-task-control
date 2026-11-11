#!/usr/bin/env python3
"""Read ptc peers --json from a compose service and assert P0-C roles and catalog."""
import json
import subprocess
import sys

COMPOSE = [
    "docker",
    "compose",
    "-f",
    "docker/test/docker-compose.yml",
    "-f",
    "docker/test/docker-compose.p0-test.yml",
]


def peers(service: str) -> dict:
    out = subprocess.check_output(
        COMPOSE + ["exec", "-T", service, "ptc", "peers", "--data-dir", "/data", "--json"],
        text=True,
    )
    return json.loads(out)


def by_name(st: dict) -> dict:
    return {p["name"]: p for p in st.get("peers", [])}


def must(cond: bool, msg: str) -> None:
    if not cond:
        raise SystemExit("FAIL: " + msg)
    print("PASS:", msg)


def offered(entry: dict, svc: str) -> bool:
    return bool(entry.get("services", {}).get(svc, {}).get("offered"))


def enabled(entry: dict, svc: str) -> bool:
    return bool(entry.get("services", {}).get(svc, {}).get("enabled"))


def main() -> int:
    a = by_name(peers("worker-a"))
    b = by_name(peers("worker-b"))
    g = by_name(peers("gateway"))
    for label, book in ("worker-a", a), ("worker-b", b), ("gateway", g):
        must(set(book) >= {"worker-a", "worker-b", "gateway"}, f"{label} catalog has all three nodes")

    must(offered(a["worker-a"], "exec"), "worker-a offers exec (mocked NVIDIA GPU)")
    must(offered(b["worker-b"], "exec"), "worker-b offers exec (mocked NVIDIA GPU)")
    must(not offered(g["gateway"], "exec"), "gateway does not offer exec (no NVIDIA GPU / exec disabled)")
    must(not enabled(g["gateway"], "exec"), "gateway has exec switched off")
    must(offered(g["gateway"], "relay"), "gateway offers relay")
    must(offered(g["gateway"], "files"), "gateway offers files")
    must(not offered(a["worker-a"], "relay"), "worker-a does not offer relay")
    must(not offered(b["worker-b"], "relay"), "worker-b does not offer relay")
    reason = g["gateway"]["services"]["exec"].get("reason") or ""
    must(bool(reason), f"gateway exec refusal has a reason (got {reason!r})")
    print("All role assertions passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
