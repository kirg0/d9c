---
description: "Logs: api restarts in a loop because the db container is down."
tags: [d9c-logs, read]
max_turns: 30
timeout_seconds: 600
allowed_tools: [Skill, Read, Glob, Grep]
env:
  EVAL_D9C_SCENARIO: restart-loop
---

Our site has been answering 502 for about half an hour. Dig through the logs and tell me what is going on.
