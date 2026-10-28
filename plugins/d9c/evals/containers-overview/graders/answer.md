---
type: llm
weight: 2
---

PASS if the reply names web and api as the running containers and lists the compose projects
webapp, monitoring and legacy with their state (monitoring partly running, legacy stopped).
Mentioning the stopped db container is welcome but not required.
FAIL if it invents containers or projects, or claims it could not reach the host.
