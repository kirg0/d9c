---
description: "Base skill: list containers and compose projects on the demo host."
tags: [d9c, read]
max_turns: 30
timeout_seconds: 600
allowed_tools: [Skill, Read, Glob, Grep]
env:
  EVAL_D9C_SCENARIO: default
---

Which containers are running on my Docker host right now, and which compose projects does it have?
