---
type: llm
weight: 2
---

PASS if the reply lists acme/web:3.1, acme/web:3.0, acme/report:1.3 and golang:1.22 as unused,
mentions the two dangling (<none>) images, and says acme/web:3.2 (container web) and
acme/report:1.4 (stopped container nightly-report) are in use and should stay.
FAIL if it calls acme/report:1.4 unused, or says it deleted anything.
