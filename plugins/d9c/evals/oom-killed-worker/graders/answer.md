---
type: llm
weight: 2
---

PASS if the reply concludes that worker is killed for running out of memory (OOM kill, exit code
137) against its 256 MiB memory limit, cites evidence (the OOM-killed flag or oom events, the
memory growing per batch in the logs), and proposes a fix such as raising the memory limit or
lowering the batch size.
FAIL if it blames something else, misses the memory limit, or says it already changed or restarted
anything.
