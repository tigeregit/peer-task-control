# Discovery and scans

Configure only `trusted_cidrs`. Never ask the user for neighbor IPs.
Hosts outside those CIDRs are rejected. Same-segment discovery uses mDNS
and port probes; cross-segment discovery is done by a `services.relay:
true` gateway that scans trusted CIDRs and pushes the catalog.

## Scan interval

`scan_interval: 0s` (the default) means one startup scan, then passive
discovery; set a positive duration such as `5m` to repeat after each scan
completes. mDNS, incoming connections, and existing mesh maintenance remain
active. Explicit old `30s` settings retain periodic behavior. Policy
changes do not trigger scans.

## Manual scans

Use `ptc scan [--json]` for the local daemon, or
`ptc scan --node <known-relay-name|peer-id> [--json]` to request a relay
scan. Relay scans cover trusted CIDRs; normal local scans cover local
trusted subnets. Both include configured `scan_hosts`. Remote requests
require relay service and allow/deny permission, not files/exec.

`scan_min_interval` defaults to `60s`: one global manual-request cooldown
on the target, shared by local CLI and every remote peer. Pending/running
scans and requests within cooldown are rejected, never deferred; rejected
requests do not extend cooldown. Startup/periodic scans do not consume
manual quota. Restart resets the cooldown. Success means queued, not
completed. JSON refusals include status/error and applicable
`retry_after_seconds`; exit is nonzero. HTTP uses 202 accepted, 409 busy,
429 limited with Retry-After, and 403 denied.

## If a node is missing from the catalog

Wait for a scan or relay push; check `trusted_cidrs` and, across segments,
that a `relay` gateway is up. Do not invent neighbor IPs. Nodes may use
`scan_hosts` (e.g. `host.docker.internal` for the Windows `host` node, or
`127.0.0.1:port` for another local node). For a NAT-mapped relay, forward
both TCP and UDP on the mesh port (18765). Nodes re-reserve on relay
reconnection; check daemon warnings if reservation fails.
