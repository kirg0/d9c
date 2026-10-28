---
type: llm
weight: 2
---

PASS if the reply explains that frontend is only on web-net while backend (and postgres) are on
api-net, so they share no network and frontend cannot resolve the name backend, and proposes
connecting frontend and backend to a common user-defined network (for example in the compose
file), possibly noting api-net is internal.
FAIL if it blames DNS or the backend process without noticing the separate networks.
