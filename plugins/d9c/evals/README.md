# d9c skill evals

Each directory here is a [`claude plugin eval`](https://code.claude.com/docs/en/plugin-evals) case:
a request a user might type (`prompt.md`) and the graders that score the reply and the tool calls
(`graders/*.md`). There is one case per skill plus `unrelated-request`, which checks that no d9c
skill fires for an unrelated request.

The cases run against the plugin's **real** MCP server (`d9c mcp`) on built-in demo data, never
on a real host: every case sets `EVAL_D9C_SCENARIO`, the plugin's `.mcp.json` passes it to the
server as `D9C_DEMO_SCENARIO`, and d9c then serves the named scenario — fake data with one planted
problem (an OOM kill, a restart loop, an unhealthy compose project, a full disk…). Writes there
only change the fake. `d9c skills ls` lists the scenarios; `d9c -demo-scenario oom` opens one in
the TUI, and `d9c -demo-scenario oom inspect worker` runs a command on it.

## Running

Every run is a real model call billed to your account. From the repository root:

```sh
go build -o d9c . && export PATH="$PWD:$PATH"      # the d9c on PATH is the server under test
claude plugin eval plugins/d9c \
  --allow-real-servers --allow-tools "mcp__plugin_d9c_d9c__*" \
  --ablation none --runs 1                       # quick pass; drop both for the full 3-run score
claude plugin eval plugins/d9c --case oom-killed-worker --allow-real-servers \
  --allow-tools "mcp__plugin_d9c_d9c__*" --ablation none --runs 1   # one case
```

- `--allow-real-servers` starts `d9c mcp` instead of a mock and `--allow-tools` lets the agent call
  it; without them the d9c tools are missing and every case fails.
- The no-plugin baseline (`--ablation with-without`) has no d9c tools at all, so its score says
  little; `--ablation none` halves the cost.
- Results go to `plugins/d9c/evals/results/` (ignored by git).

## What is checked without a model

`go test` (and CI) checks the suite and the scenarios for free:

- `TestEvalSuite` — the case and grader format, that every case names an existing scenario, that
  every grader names a real MCP tool and a shipped skill, and that every skill has a case;
- `TestSkillEvalScenarios` — for every scenario, the commands its skill prescribes run on it and
  their output shows the planted problem (exit 137 and the OOM flag, the unhealthy health log, the
  separate networks…).

## Adding a case

Write `<case>/prompt.md` (frontmatter: `description`, `tags`, `max_turns`, `timeout_seconds`,
`allowed_tools`, `env: {EVAL_D9C_SCENARIO: <scenario>}`; body: the request) and at least a
`skill-fired` grader (`tool_used` of `Skill`), one on the d9c tool calls and an `llm` rubric with
PASS and FAIL conditions. Tool names are `mcp__plugin_d9c_d9c__<tool>`; a "never called" grader
needs `min: 0` and `max: 0`, and `arm: both` to count in a two-arm run. A new scenario goes into
`internal/docker/fake_scenarios.go` with its steps in `main_eval_test.go`.
