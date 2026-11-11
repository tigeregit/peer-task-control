# Resources, Bitswap, DirDigest, and board CID

Every resource has two identifiers; they are **not** interchangeable.

- **DirDigest** (or a raw sha256 for a blob) is the resource's identity and
  verification fingerprint. Use it to check that content is correct, and as
  the argument to `ptc resources show|get|pull`.
- The **board CID** is the address of the Bitswap transport manifest. Use it
  to find and pull blocks on the mesh. It is computed with raw SHA-256 and
  is **not** a UnixFS root CID.

Inputs remain sha256 digests. Direct CID or dataset-name resolution is
unsupported. Do not invent `ptc dataset` or claim UnixFS exists.

## Commands

`ptc resources` lists seeds on the mesh. `ptc resources show <digest>`
inspects one; `--list-files` can list paths in a very large directory tree.
`ptc resources get <digest> -o <file>` downloads. `ptc resources pull
<digest>` persists a verified replica on this node (tree under
`packages/replica-*`, blob in CAS) and advertises the transport-manifest
CID on the board. Prefer `--json` on list, show, and pull.

## Bitswap transport

Bitswap uses 256-KiB chunks and PTC raw-CID manifests, not UnixFS.
Resource-digest discovery still uses the trusted PTC mesh. Data moves in
256KiB blocks over standard Boxo Bitswap, with relay, verification,
persistent cache, and failover to another replica.

Resources above 10,000 chunks automatically use v2 multi-level Merkle
indexes with at most 256 links per page; no manual splitting or
whole-resource buffering is needed. Small resources retain v1 manifests,
and new nodes read both formats. Upgrade holders and readers for
large-resource transfers; format or verification errors never trigger
legacy fallback. Only peers that do not speak the blocks protocol fall
back to the old tar/blob path.

Sizes use int64; disk space, 20 seconds per block, and 30 minutes overall
still constrain transfers. Each relay connection resets after 30 minutes
or 64 GiB in either direction; uninterrupted recovery is not guaranteed.
Do not claim TB-scale testing.

The persistent `seeds_dir/blocks` cache has no GC; `ptc clean resource`
does not reclaim it. Cache and staging count toward seed disk usage.
Files policy is rechecked on block requests and sends, including existing
connections.

`OpenResource` uses the same pull path as `ptc resources pull` when it can.
A seed that exists only as a local path on the submitter is published
there first. The target tries holders by hop count and verifies DirDigest
or sha256 before delivering content.
