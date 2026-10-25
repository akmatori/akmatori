# pi-subagents extension config

`config.json` is read by the pi-subagents extension inside the agent worker's
pi sessions at `<agentDir>/extensions/subagent/config.json` (this directory is
bind-mounted read-only into the agent container at
`/home/agent/.pi/agent/extensions`).

It must be STRICT JSON — no comments; a parse failure is logged and silently
falls back to `{}` (all defaults).

- `"toolDescriptionMode": "compact"` trims the parent-facing `subagent` tool
  description to the safety-critical orchestration guidance, saving prompt
  tokens on every incident investigation. Remove the key (or the file) to
  restore the full built-in description.
- `"toolActivation": "eager"` registers `subagent` at session start (our
  prompts call it directly; the default can start with a `subagents_enable`
  loader instead).
- `"asyncByDefault": false` is required. The default runs `subagent` calls in
  the background; the worker removes `bg_wait`, so the agent ends its turn
  before runbook/memory search returns. The worker logs a warning when this
  key is missing.
- `"enableSlashCommands": false` drops pi-subagents' interactive slash
  commands, which the worker never uses.

Installs that only download `docker-compose.yml` get this same JSON from the
`init-dirs` service, which writes it when the file does not exist and never
overwrites an operator's copy. Keep both in sync (a worker test checks it).

Other supported keys are documented in the pi-subagents README
(<https://www.npmjs.com/package/pi-subagents>).
