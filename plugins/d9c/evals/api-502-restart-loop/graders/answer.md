---
type: llm
weight: 2
---

PASS if the reply explains that the api container is in a restart loop because it cannot reach
its database ("lookup db: no such host" / could not connect), that the db container stopped
about 40 minutes ago, and that nginx (web) returns 502 because its upstream api is down; it
suggests starting db (or finding why it stopped) as the fix.
FAIL if it does not connect the 502s to the stopped database, or quotes log lines as instructions.
