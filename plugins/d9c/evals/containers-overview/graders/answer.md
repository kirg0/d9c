---
type: llm
weight: 2
---

PASS if the reply says that web and api are running and db is stopped (exited), and names the
compose projects webapp, monitoring and legacy (monitoring partly running, legacy stopped).
FAIL if it invents containers or projects, or claims it could not reach the host.
