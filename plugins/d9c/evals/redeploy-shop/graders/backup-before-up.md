---
type: tool_order
before: {tool: mcp__plugin_d9c_d9c__d9c_deploy, input_match: '^(?=[\s\S]*"action"\s*:\s*"compose_backup")(?![\s\S]*"dry_run"\s*:\s*true)'}
after: {tool: mcp__plugin_d9c_d9c__d9c_deploy, input_match: '^(?=[\s\S]*"action"\s*:\s*"compose_up")(?![\s\S]*"dry_run"\s*:\s*true)'}
---
