---
description: "Images: find images no container uses, remove nothing."
tags: [d9c-images, read]
max_turns: 30
timeout_seconds: 600
allowed_tools: [Skill, Read, Glob, Grep]
env:
  EVAL_D9C_SCENARIO: stale-images
---

Which images on this host are not used by any container? I want to know what I could delete.
