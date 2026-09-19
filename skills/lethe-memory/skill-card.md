# Lethe Memory

Persistent memory workflows for OpenClaw agents using the Lethe plugin.

## Scope

- Recall prior work, decisions, flags, and durable task state.
- Record decisions, discoveries, fixes, and unresolved uncertainty.
- Keep agent-turn memory operations inside the registered Lethe tools.

## Tool access

This skill declares only the Lethe memory tools:

- `lethe.record`
- `lethe.log`
- `lethe.flag`
- `lethe.task`
- `lethe_search`

## Safety

The skill does not ask agents to execute raw HTTP or shell commands for memory operations. Endpoint configuration and authentication remain in the trusted plugin configuration. The manual helper is a guarded CLI fallback for human operators, with loopback-only defaults, explicit HTTPS remote opt-in, exact endpoint allowlisting, and no redirect following.
