---
description: "Security audit: privileged, docker.sock, host root mount, public ports, latest tags, env secrets."
tags: [d9c-security-audit, read]
max_turns: 30
timeout_seconds: 600
allowed_tools: [Skill, Read, Glob, Grep]
env:
  EVAL_D9C_SCENARIO: insecure
---

Is this Docker host configured securely? Review the containers and tell me what to fix.
