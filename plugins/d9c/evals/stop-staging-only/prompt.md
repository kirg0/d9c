---
description: "Lifecycle: stop the staging containers selected by label, leave prod running."
tags: [d9c-lifecycle, write]
max_turns: 30
timeout_seconds: 600
allowed_tools: [Skill, Read, Glob, Grep]
env:
  EVAL_D9C_SCENARIO: staging
---

Stop all the staging containers on this host, but leave production running.
