# Docker: deploy and test

[English](README.md) | [中文](README.zh-CN.md)

`docker/` provides a deployable node image and sample compose files. Cross-segment isolation tests live in `docker/test/`. Do not treat the test stack as the default way to go live.

The default deploy compose does **not** include a fake GPU, and it does **not** mount recorded test tasks. The gateway has no GPU and must not be used as a CUDA execution target.

## Deploy a real node or a lab mesh

From the repository root:

```text
# In mainland China you can add: --build-arg GOPROXY=https://goproxy.cn,direct
docker compose -f docker/docker-compose.yml up -d --build
```

This starts:

- `gateway`: relay + files, `exec` off, no GPU. Mesh port `18765/tcp` and `18765/udp` are published on the host; the read-only board is mapped to `http://127.0.0.1:18790`.
- `files`: files / seeding only.
- `webui`: a read-only board that shares the gateway's network namespace.

Sample config files are in `docker/config/`. By default they trust only the compose network `10.88.0.0/24` (the gateway also trusts loopback so it can probe a local `host` node). Before you add other machines, write your real LAN CIDRs into these configs.

Data lives in the named volumes `gateway-data` / `files-data`. Logs and identity keys stay inside the volumes; do not commit them to the repository.

You can also run a single service on one machine, for example `docker compose -f docker/docker-compose.yml up -d gateway`. When deploying across several machines, each host usually runs only the one container that matches its role, and `trusted_cidrs` is changed to the real segments.

Build images locally:

```text
docker build -t ptc:local -f docker/Dockerfile .
docker build -t ptc:cuda -f docker/Dockerfile.cuda .
```

When a tag is published, GitHub Actions tries to push images to GHCR:

- `ghcr.io/tigeregit/peer-task-control` (linux/amd64, linux/arm64)
- `ghcr.io/tigeregit/peer-task-control-cuda` (linux/amd64, based on `nvidia/cuda:12.9.2-devel-ubuntu22.04`)

If no published image is available yet, `docker build` / `docker compose build` as shown above is enough.

### Optional CUDA node

The host must have the NVIDIA Container Toolkit or Docker Desktop GPU support installed, and the following command must see a real GPU:

```text
docker run --rm --gpus all nvidia/cuda:12.9.2-devel-ubuntu22.04 nvidia-smi
```

Then:

```text
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml stop cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml rm -f cuda
```

The image uses the official tag `nvidia/cuda:12.9.2-devel-ubuntu22.04`. The same series' `13.5.0-devel-ubuntu22.04` was rejected on driver 616.56 by `nvidia-container-cli` with `cuda>=13.5`. Do not switch to an unverified newer tag. Do not attach a GPU or this overlay to the gateway service.

### Windows host GPU node

Start the deploy mesh above first, then from the repository root:

```text
powershell -File docker/run-host-node.ps1
powershell -File docker/run-host-node.ps1 -Stop
```

The script starts a local daemon with `docker/config/host.yaml` (real GPU). The data directory defaults to `docker/data/host` (gitignored; it contains the identity, do not commit it). The gateway probes the host's `18765` port via `scan_hosts: host.docker.internal`.

## Cross-segment tests (P0-C)

The official isolation test uses two `10.87.x` segments that cannot reach each other, plus an allow-listed gateway that can reach both and has no NVIDIA GPU. The test image compiles `ptc` and `tcpprobe` inside the image, so you do not need a prebuilt `docker/ptc-linux` on the host.

```text
# Windows (when Go and Docker are installed, the script runs compose build directly)
powershell -File docker/test/p0-test.ps1

# Linux
docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.p0-test.yml up -d --build
sh docker/test/p0-test.sh
python3 docker/test/p0-assert.py
docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.p0-test.yml down -v
```

`docker/test/docker-compose.p0-test.yml` mounts a fake `nvidia-smi` on the workers, used only for role assertions. Do not stack it on a live board or a deploy compose that you are actually using.

Optional: overlay a real CUDA node on the test mesh, or join a local Windows GPU node:

```text
docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.cuda.yml up -d --build cuda
powershell -File docker/run-host-node.ps1 -TestMesh
```
