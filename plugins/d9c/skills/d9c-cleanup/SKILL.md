---
name: d9c-cleanup
description: Free disk space on a container host with d9c — measure usage, plan prunes, get the user's approval, run them and report what was freed. Use when a host is low on disk or someone asks to clean up images, volumes or build cache.
---

# Clean up disk space with d9c

Follow the base `d9c` skill. Pruning is **destructive**: it needs the write policy to allow
destructive commands and the user's explicit approval of the plan.

## 1. Measure

- `d9c system df` / `d9c_system` `{action: "df"}`: size and reclaimable space per type (images,
  containers, local volumes, build cache).
- Look at what would go: `d9c images`, `d9c volumes`, `d9c ps -a` (stopped containers)
  (`d9c_resources` images/volumes, `d9c_containers` ps with `all: true`).

## 2. Plan

Choose the least destructive prune that frees enough:

| Command | Removes | Keeps |
|---------|---------|-------|
| `image prune` | dangling (untagged) images no container uses | tagged images |
| `system prune` | stopped containers, unused networks, dangling images, build cache | volumes |
| `volume prune` | volumes no container uses — **their data is lost** (Docker 23+: anonymous volumes only) | volumes in use |

- Preview each one: `d9c image prune --dry-run` / `d9c_prune` `{action: "images", dry_run: true}`.
- Never prune volumes without the user naming that as acceptable; list the volumes that would go
  and what they likely hold (names, the containers that used them).

## 3. Confirm and run

- Show the user the plan (the dry-run steps plus the expected space from `system df`) and wait for
  an explicit yes.
- CLI: `d9c system prune --yes` (without `--yes` it stops with `confirmation_required` and the plan).
  MCP: call `d9c_prune` with the approved action.
- A `forbidden` error means the policy does not allow it: tell the user which setting the hint
  names; do not look for another way to delete data.

## 4. Report

Run `d9c system df` again and report the space freed per type, plus anything large that remains
(e.g. tagged images no container uses) as a suggestion — not as an action.
