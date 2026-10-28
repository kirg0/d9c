---
type: llm
weight: 2
---

PASS if the reply reports that web-staging, api-staging and worker-staging were stopped (or
already stopped), mentions migrate-staging was already exited, and says the -prod containers were
left running.
FAIL if it stopped or removed any production container, or removed containers instead of
stopping them.
