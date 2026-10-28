---
name: d9c-lifecycle
description: Start, stop, restart, kill or remove containers with d9c — by name, ID prefix or a filter such as label or image, with a dry-run plan, the user's approval for destructive steps, and a wait until the result is running or healthy. Use when someone asks to bring containers up or down, bounce a service, or delete containers.
---

# Start, stop and remove containers with d9c

Follow the base `d9c` skill for hosts, output and the write policy. These commands change
state: **safe** for start/stop/restart, **destructive** for kill and rm.

## 1. Pick the targets

- Name them: IDs, unique ID prefixes or names — `d9c restart web api`.
- Or select them with `--filter key=value` (repeatable, all must match, exact values):
  `name=<glob>`, `image=<glob>`, `status=<state>`, `label=<key>[=<value>]`, `network=<name>`,
  `health=<healthy|unhealthy|starting|none>`. Example: `d9c stop --filter label=env=staging`.
- Check what a filter selects before acting:
  `d9c ps -a --fields name,state,status,image` / `d9c_containers` `{action: "ps", all: true}`.
- Prefer names over filters for destructive commands; a filter that is too broad is the classic
  way to remove the wrong container.

## 2. Preview

- CLI: `d9c stop --filter label=env=staging --dry-run` prints the plan (which containers, what
  happens to each) and changes nothing.
- MCP: `d9c_lifecycle` `{action: "stop", filter: ["label=env=staging"], dry_run: true}`, or
  `d9c_remove` `{action: "rm", targets: ["old-job"], dry_run: true}`.
- Show the plan to the user when the request was vague, touches more than one container, or is
  destructive.

## 3. Run

| Request | CLI | MCP |
|---------|-----|-----|
| Start stopped containers | `d9c start db` | `d9c_lifecycle` `{action: "start", targets: ["db"]}` |
| Graceful stop | `d9c stop web` | `d9c_lifecycle` `{action: "stop", targets: ["web"]}` |
| Restart | `d9c restart web` | `d9c_lifecycle` `{action: "restart", targets: ["web"]}` |
| Signal / hard kill | `d9c kill web -s TERM --yes` | `d9c_remove` `{action: "kill", targets: ["web"], signal: "TERM"}` |
| Remove stopped containers | `d9c rm --filter status=exited --yes` | `d9c_remove` `{action: "rm", filter: ["status=exited"]}` |
| Remove a running container | `d9c rm web -f --yes` | `d9c_remove` `{action: "rm", targets: ["web"], force: true}` |

- Destructive commands (kill, rm) without `--yes` stop with `confirmation_required` and the plan:
  ask the user, then rerun with `--yes`. In MCP the client confirms the call — still say what will
  be removed before calling.
- `rm` deletes the container and its writable layer; named volumes stay. Mention data that lives
  only in the container.
- Prefer `stop` over `kill`: stop sends SIGTERM and waits 10 s before SIGKILL.

## 4. Check the result

- A multi-target command reports every target: `"partial": true` (CLI exit 8) means some failed —
  report which and why from `data.targets[].error`; do not retry blindly.
- After a start or restart, wait for the service instead of assuming it is up:
  `d9c wait web --until healthy --timeout 60` / `d9c_containers` `{action: "wait", container: "web", until: "healthy", timeout: 60}`
  (`--until running` for containers without a healthcheck). A `timeout` error (exit 9) means it
  never got there: switch to `d9c-troubleshoot`.
- Compose services are better handled per project (`d9c-compose`), so the project stays
  consistent.
