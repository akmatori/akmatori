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

Other supported keys are documented in the pi-subagents README
(<https://www.npmjs.com/package/pi-subagents>).
