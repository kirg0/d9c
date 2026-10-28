---
description: "Networks: frontend and backend are on different networks."
tags: [d9c-net-storage, read]
max_turns: 30
timeout_seconds: 600
allowed_tools: [Skill, Read, Glob, Grep]
env:
  EVAL_D9C_SCENARIO: isolated-network
---

The frontend container can't reach the backend — its requests fail with ENOTFOUND backend. Why?
