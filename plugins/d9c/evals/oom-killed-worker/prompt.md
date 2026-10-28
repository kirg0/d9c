---
description: "Troubleshoot: worker is OOM-killed under a 256 MiB limit."
tags: [d9c-troubleshoot, read]
max_turns: 30
timeout_seconds: 600
allowed_tools: [Skill, Read, Glob, Grep]
env:
  EVAL_D9C_SCENARIO: oom
---

The worker container keeps dying every few minutes. Find out why and tell me how to fix it — don't change anything yet.
