---
name: d9c-fleet
description: Survey all hosts d9c knows at once — reachability, engine versions, running and stopped containers, unhealthy services, CPU/memory capacity and disk usage per host — and compare environments such as staging and production. Use when someone asks for an overview, health report or capacity check across several hosts, or how two environments differ.
---

# Fleet overview with d9c

Follow the base `d9c` skill. Everything here is **read** only.

## Which channel

- The CLI fans a read command out to several saved hosts in one call: `--all-hosts`, or
  `--host a,b` for a subset (up to 8 hosts are queried in parallel).
- An MCP session serves one host. With MCP alone, list the hosts with `d9c_hosts`, report the
  session's own host with the tools below, and say which hosts need their own session
  (`d9c mcp --host <name>`) or the CLI.

## 1. The hosts

- `d9c hosts ls` / `d9c_hosts`: name, URL, `read_only` and `agent_tier` per host.

## 2. One summary per host

- `d9c info --all-hosts` / `d9c_system` `{action: "info"}`: engine version, container counts
  (running, paused, stopped), images, CPUs and total memory.
- The result is a list with one entry per host: `data` on success, `error` on failure. A
  `"partial": true` envelope (exit 8) means some hosts did not answer — report them as
  unreachable with their error code and hint (`connection`, `auth_required`…); never retry with a
  different host key or credentials.

## 3. Health

- `d9c ps -a --all-hosts --fields name,state,status,health,image` / `d9c_containers` `{action: "ps", all: true}`.
- Flag per host: containers with health `unhealthy`, state `restarting`, or `exited` with a
  non-zero code in `status`. Investigate one with `d9c-troubleshoot`.
- `d9c compose ls --all-hosts` / `d9c_compose` `{action: "ls"}`: projects that are `partial` or
  `stopped`.

## 4. Capacity

- `d9c stats --all-hosts` / `d9c_system` `{action: "stats"}`: one CPU/memory sample per running
  container. Sum memory per host and compare with `mem_total` from `info`; compare CPU percentages
  with `ncpu` (100% = one core).
- `d9c system df --all-hosts` / `d9c_system` `{action: "df"}`: image, container, volume and
  build-cache usage with the reclaimable part — point to `d9c-cleanup` for hosts with a lot to
  reclaim.
- Keep output small on large fleets: `--fields` and `--limit`, or one host at a time.

## 5. Compare environments

- Run the same read on both: `d9c ps --host staging,prod --fields name,image,state`.
- Compare by container or compose service name: image tags that differ, services missing on one
  side, different health. For compose projects, `d9c compose config <project> --host staging`
  and the same for prod show configuration drift (secrets stay masked).

## Report

A table per host — reachable, engine, running/total, unhealthy, CPU and memory headroom, disk
reclaimable — then the problems ordered by severity, each with the command that shows it.
