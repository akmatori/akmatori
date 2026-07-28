# SSH Tool Command Validation

The SSH tool implements a **4-stage command validation pipeline** using Claude Code wildcard syntax for flexible pattern matching.

## Overview

| Stage | Description | Configurable |
|-------|-------------|--------------|
| **1. Global Deny List** | Patterns that ALWAYS block commands | ✅ Tool settings |
| **2. Default Read-Only** | Whitelisted safe commands | ❌ Static |
| **3. Global Allow List** | Explicitly permitted commands | ✅ Tool settings |
| **4. Destructive Gate** | Allows non-read-only commands | ✅ Tool toggle |

---

## Pattern Syntax (Claude Code Wildcards)

Commands use **glob patterns** with `*`:

| Pattern | Matches | Does NOT Match |
|---------|---------|----------------|
| `rm` | `rm`, `rm ` | `rm -rf /`, `rmdir` |
| `rm *` | `rm -rf /`, `rm file.txt` | `rm` (no args), `rmdir` |
| `rm:*` | Same as `rm *` | `rm` (no args) |
| `shutdown` | `shutdown`, `shutdown ` | `shutdown -h now` |
| `shutdown *` | `shutdown -h now` | `shutdown` (no args) |
| `* --version` | `docker --version`, `git --version` | `docker ps` |
| `git push *` | `git push origin main` | `git pull` |

**Important:** Space before `*` matters:
- `ls *` matches `ls -la` but NOT `lsof`
- `ls*` matches both `ls -la` and `lsof`

---

## Stage 1: Global Deny List

**Configured in:** SSH Tool Settings → Command Policies → Deny List

**Rules:**
- Applied to ALL hosts in the tool instance
- **Always active** — cannot be bypassed, even with write commands enabled
- Commands matching any deny pattern are immediately blocked

**Example patterns:**
```
rm *
rmdir *
shred *
mv *
cp *
chmod *
chown *
kill *
killall *
pkill *
shutdown
reboot
halt
poweroff
init *
dd *
mkfs
fdisk *
parted *
mount *
umount *
useradd *
userdel *
usermod *
passwd *
groupadd *
apt install *
apt remove *
apt purge *
apt-get install *
yum install *
yum remove *
pip install *
npm install *
systemctl start *
systemctl stop *
systemctl restart *
systemctl enable *
systemctl disable *
docker rm *
docker rmi *
docker stop *
docker kill *
docker run *
docker exec *
kubectl delete *
kubectl apply *
kubectl create *
kubectl exec *
kubectl edit *
iptables
firewall-cmd
ufw *
su *
:(){ :|:& };:
mkfifo
mknod *
```

---

## Stage 2: Default Read-Only List

**Static list** — cannot be configured. Contains safe diagnostic commands:

### File Viewing
```
cat, head, tail, less, more
```

### Search and Find
```
grep, rg (ripgrep), fzf, find, locate, which, type
```

### Directory Listing
```
ls, pwd, tree
```

### System Information
```
whoami, uname, hostname, date, id
uptime, w, who, last
nproc, lscpu, getconf
```

### Process Information
```
ps, top, htop, pgrep, pstree
```

### Performance Monitoring
```
mpstat, sar, iostat, vmstat
nmon, iotop, pidstat
```

### Memory and Disk Information
```
df, du, free, lsblk
```

### Network Information
```
netstat, ss, ip, ifconfig, ping
traceroute, dig, nslookup, host
```

### Environment
```
env, printenv, echo
```

### Text Processing (Read-Only)
```
wc, sort, uniq, cut, awk
sed, tr, diff, comm
```

### File Information
```
stat, file, md5sum, sha256sum
```

### Logs
```
journalctl, dmesg
```

### Commands with Subcommand Restrictions
These commands are allowed but restricted to specific subcommands:

| Command | ✅ Allowed Subcommands |
|---------|----------------------|
| `docker` | ps, images, logs, inspect, stats, top, info, version, network ls, volume ls |
| `kubectl` | get, describe, logs, top, version, config view, cluster-info |
| `systemctl` | status, is-active, is-enabled, list-units, list-unit-files, show |
| `apt` | list, show, search, policy |
| `yum` | list, info, search |
| `dpkg` | -l, -L, -s, --list, --listfiles, --status |
| `rpm` | -qa, -qi, -ql, --query |

### Sudo
```
sudo
```
When `sudo` is used, the inner command is recursively validated against the same rules.

---

## Stage 3: Global Allow List

**Configured in:** SSH Tool Settings → Command Policies → Allow List

**Rules:**
- Applied to ALL hosts in the tool instance
- Commands matching these patterns are explicitly allowed
- **Deny list still applies** — denied commands are blocked first
- Useful for permitting commands not in the default read-only list

**Example patterns:**
```
curl *
wget *
scp *
rsync *
ssh *
```

---

## Stage 4: Destructive Gate

**Configured in:** SSH Tool Settings → Command Policies → "Allow Write/Destructive Commands" toggle

**Rules:**
- When **disabled** (default): commands not in read-only list and not in allow list are blocked
- When **enabled**: commands not in read-only list are permitted (except those in deny list)
- **Deny list still applies** — denied commands are always blocked

---

## Validation Pipeline Flow

```
Command Received
       │
       ▼
┌──────────────────────────────┐
│ Stage 1: Global Deny List    │
│ (Tool Settings)              │
├──────────────────────────────┤
│ Match? → BLOCK               │
│ No Match → continue          │
└──────────────────────────────┘
       │
       ▼
┌──────────────────────────────┐
│ Check Write Redirects (>)    │
├──────────────────────────────┤
│ Found? → BLOCK               │
│ None → continue              │
└──────────────────────────────┘
       │
       ▼
┌──────────────────────────────┐
│ Stage 2: Default Read-Only   │
│ (Static List)                │
├──────────────────────────────┤
│ In List? → ALLOW             │
│ Not in List → continue       │
└──────────────────────────────┘
       │
       ▼
┌──────────────────────────────┐
│ Stage 3: Global Allow List   │
│ (Tool Settings)              │
├──────────────────────────────┤
│ Match? → ALLOW               │
│ No Match → continue          │
└──────────────────────────────┘
       │
       ▼
┌──────────────────────────────┐
│ Stage 4: Destructive Gate    │
│ (Tool Toggle)                │
├──────────────────────────────┤
│ Write Enabled? → ALLOW       │
│ Write Disabled? → BLOCK      │
└──────────────────────────────┘
```

---

## Configuration

### Tool-Level Settings (apply to ALL hosts)

| Setting | Type | Description |
|---------|------|-------------|
| `ssh_deny_list` | array | Global deny patterns (Claude Code wildcard syntax) |
| `ssh_allow_list` | array | Global allow patterns (Claude Code wildcard syntax) |
| `ssh_allow_write_commands` | boolean | Allow destructive commands not in read-only list |

### Example Tool Configuration

```json
{
  "ssh_deny_list": [
    "rm *",
    "shutdown",
    "kill *",
    "docker stop *",
    "docker rm *",
    "kubectl delete *"
  ],
  "ssh_allow_list": [
    "curl *",
    "wget *",
    "scp *"
  ],
  "ssh_allow_write_commands": false
}
```

---

## Error Messages

When a command is blocked, the error message includes:
- The reason for blocking (deny list, read-only, etc.)
- List of allowed commands by category
- Instructions to enable write commands

---

## Command Chain Validation

Commands joined by separators are validated individually:

```bash
# Each command in the chain is checked separately
ls -la && grep "error" /var/log/syslog | sort | uniq -c
```

Separators that trigger split-validation:
- `;` (sequential execution)
- `&&` (and)
- `||` (or)
- `|` (pipe)

---

## Configuration

### Per-Host Settings

Each host in `ssh_hosts` can have independent security settings:

```json
{
  "ssh_hosts": [
    {
      "hostname": "web-prod-1",
      "address": "10.0.1.5",
      "allow_write_commands": false,
      "allowed_commands": []
    },
    {
      "hostname": "db-prod-1",
      "address": "10.0.2.5",
      "allow_write_commands": true
    }
  ]
}
```

### Ad-Hoc Connections

When `allow_adhoc_connections` is enabled, ad-hoc connections use:

| Setting | Default | Description |
|---------|---------|-------------|
| `adhoc_default_user` | `root` | Default SSH user |
| `adhoc_default_port` | `22` | Default SSH port |
| `adhoc_allow_write_commands` | `false` | Read-only by default |

---

## Validation Flow

```
Command received
       │
       ▼
┌─────────────────────┐
│ Custom Allowlist?   │──Yes──► Check custom list + dangerous patterns
└─────────────────────┘         │
       │ No                     ▼
       ▼                  Validate against
┌─────────────────────┐   custom allowlist
│ Write Enabled?      │──Yes──► Allow all
└─────────────────────┘         │
       │ No                     ▼
       ▼                  Return success
┌─────────────────────┐
│ Check dangerous     │──Match──► Block with error
│ patterns            │
└─────────────────────┘
       │ No match
       ▼
┌─────────────────────┐
│ Check write         │──Found──► Block with error
│ redirects           │
└─────────────────────┘
       │ No redirect
       ▼
┌─────────────────────┐
│ Split on separators │
│ (; && || |)         │
└─────────────────────┘
       │
       ▼
┌─────────────────────┐
│ For each command:   │
│ ├─ Extract base cmd │
│ ├─ Check allowlist  │
│ ├─ Handle sudo      │
│ └─ Check subcmds    │
└─────────────────────┘
       │
       ▼
   Allow command
```

---

## Error Messages

### Read-Only Mode Violation
```
command blocked: 'rm -rf /tmp/*' contains dangerous pattern 'rm ' (read-only mode is enabled)

Allowed commands in read-only mode:
  File viewing: cat, head, tail, less, more
  Search: grep, rg, fzf, find, locate, which
  Directory: ls, pwd, tree
  System info: whoami, uname, hostname, date, id, uptime, nproc, lscpu, getconf
  Processes: ps, top, htop, pgrep, pstree
  Performance: mpstat, sar, iostat, vmstat, pidstat, nmon, iotop
  Resources: df, du, free, lsblk
  Network: netstat, ss, ip, ping, dig, traceroute
  Text processing: wc, sort, uniq, cut, awk, sed, tr
  File info: stat, file, md5sum, sha256sum
  Logs: journalctl, dmesg
  Containers: docker ps/images/logs/inspect/stats, kubectl get/describe/logs

To allow write commands, enable 'Allow Write Commands' for this host
```

### Custom Allowlist Violation
```
command blocked: 'curl' is not in the allowed command list (custom command allowlist is enabled)

Allowed commands: cat, ls, grep, journalctl

To add more commands, edit the 'Allowed Commands' list for this host in tool settings
```

---

## Implementation Details

- **Source:** `mcp-gateway/internal/tools/ssh/command_validator.go`
- **Tests:** `mcp-gateway/internal/tools/ssh/command_validator_test.go`
- **Integration:** `mcp-gateway/internal/tools/ssh/ssh.go`

### Key Functions

| Function | Purpose |
|----------|---------|
| `NewCommandValidator()` | Creates validator with default read-only list |
| `NewCommandValidatorWithAllowlist(cmds)` | Creates validator with custom allowlist |
| `ValidateCommand(cmd, allowWrite, useCustom)` | Main validation entry point |
| `extractBaseCommand(cmd)` | Extracts base command, handling paths and env vars |
| `extractCommandAfterSudo(cmd)` | Recursively extracts inner sudo command |
| `containsWriteRedirect(cmd)` | Detects `>` and `>>` redirects |