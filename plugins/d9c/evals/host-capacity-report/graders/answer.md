---
type: llm
weight: 2
---

PASS if the reply reports that memory is nearly exhausted (containers use about 7.6 GiB of the
8 GiB host, search alone about 5.5 GiB), that search uses about 1.8 of 4 CPUs, and that the
reports container is unhealthy; it notes that further hosts need their own d9c session or the
CLI with --all-hosts.
FAIL if it misses the memory pressure or the unhealthy container.
