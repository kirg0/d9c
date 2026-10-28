---
description: "Fleet: health and capacity report of the host."
tags: [d9c-fleet, read]
max_turns: 30
timeout_seconds: 600
allowed_tools: [Skill, Read, Glob, Grep]
env:
  EVAL_D9C_SCENARIO: fleet
---

Give me a health and capacity report for my Docker hosts.
