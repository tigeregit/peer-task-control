# Real CUDA vector-add fixture

A tiny `vector_add.cu` compiled with `nvcc` inside an official `nvidia/cuda` Ubuntu container. This is not the fake-CUDA Windows e2e.

```text
# Join the live mesh (do not use the default compose alone for GPU):
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda

# From another mesh node (gateway sidecar, a worker, or the Windows host):
ptc submit --workflow testdata/cuda-vectoradd/workflow.yaml --target cuda --fetch-result /tmp/cuda-vector-add
```

`prepare.yaml` sets `require_nvidia: true`. The job is refused on the no-GPU gateway and Docker workers.
