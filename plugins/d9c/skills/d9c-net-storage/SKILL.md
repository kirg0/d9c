---
name: d9c-net-storage
description: Networks and volumes on a d9c host — which containers share a network, why two containers cannot reach each other, which containers use a volume, and creating a network or volume. Use when someone reports connection refused or name resolution failures between containers, asks where data is stored, or needs a new network or volume.
---

# Networks and volumes with d9c

Follow the base `d9c` skill. Listing and inspecting are **read**; creating is **safe**; removing
volumes goes through `d9c-cleanup` (volume prune is destructive and loses data).

## Who is connected to what

- `d9c networks` / `d9c_resources` `{action: "networks"}`: name, driver, scope, subnet.
- `d9c ps -a --fields name,state,networks,ports` / `d9c_containers` `{action: "ps", all: true}`:
  the networks of every container — group by network to see who can talk to whom.
- `d9c inspect <network> --type network` / `d9c_containers` `{action: "inspect", target: "<network>", type: "network"}`:
  the attached containers with their addresses, the IPAM subnet and gateway, and `internal` (no
  outside access).

## Why can't A reach B

Work through these in order and stop at the first that explains it:

1. **Both running?** `d9c ps -a` — a stopped or restarting B refuses connections.
2. **A shared network?** Compare their `networks`. Containers on different user networks cannot
   reach each other; containers on the default `bridge` network cannot resolve each other by name
   (only user-defined networks have DNS). Fix: connect both to one user-defined network (in
   compose, the same `networks:` entry).
3. **The right name and port?** Use B's container or compose service name and its container port
   (the `80` of `0.0.0.0:8080->80/tcp`), not the published host port.
4. **Listening?** B must listen on `0.0.0.0` inside the container, not `127.0.0.1`. Check B's logs
   for the bind address: `d9c logs <B> --grep 'listen|bind'`.
5. **Internal or isolated network?** An `internal` network blocks traffic to the outside, not
   between its members.

To test from inside A, `d9c exec <A> -- getent hosts <B>` resolves the name — exec is
destructive-tier and needs the user's approval, so suggest it rather than run it unasked.

## Volumes

- `d9c volumes` / `d9c_resources` `{action: "volumes"}`: name, driver, mount point.
- Which containers use a volume: `d9c inspect <container>` / `d9c_containers` `{action: "inspect", target: "<container>"}`
  — `mounts` lists type, name, source and destination. A named volume no container mounts is a
  cleanup candidate; report it, do not remove it.
- `d9c inspect <volume> --type volume` shows the driver, mount point and labels (a compose
  project label tells which stack created it).

## Create

- Network: `d9c network create backend-net --subnet 172.28.0.0/16` /
  `d9c_create` `{action: "network", name: "backend-net", subnet: "172.28.0.0/16"}` (`--gateway`
  must lie inside `--subnet`; the driver defaults to bridge).
- Volume: `d9c volume create uploads` / `d9c_create` `{action: "volume", name: "uploads"}`.
- Preview with `--dry-run` / `dry_run: true`; a name that exists fails with `usage`.
- Attaching an existing container to a network is not available in d9c: propose the compose or
  `docker network connect` change to the user.
