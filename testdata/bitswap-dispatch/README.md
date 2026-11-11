# bitswap dispatch check

Dispatches a real CUDA job to the optional `cuda` Ubuntu node. The Prepare
step references the fake-CUDA SDK seed (`third_party_sdk`, ~2 GiB) by digest
only, so the exec node must pull it with bitswap from the mesh holders and
verify its DirDigest before the run starts.

```text
ptc submit --workflow testdata/bitswap-dispatch/workflow.yaml --target cuda --json
```

Generate the SDK seed first with `testdata/cuda-e2e` if your mesh does not
have it; change the digest in `prepare.yaml` to match.
