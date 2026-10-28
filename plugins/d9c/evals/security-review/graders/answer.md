---
type: llm
weight: 2
---

PASS if the reply flags, with severities: admin-panel running privileged with the Docker socket
mounted and port 9000 on 0.0.0.0; backup mounting the host root "/" writable with SYS_ADMIN and
running as root; redis published on 0.0.0.0:6379; mutable latest tags (portainer, redis); secrets
passed in environment variables (masked by d9c); and notes app as configured reasonably
(non-root, limits, bound to 127.0.0.1).
FAIL if it misses the privileged/socket finding or prints a secret value.
