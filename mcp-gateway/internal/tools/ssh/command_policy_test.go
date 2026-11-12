package ssh

import (
	"context"
	"strings"
	"testing"
)

func testPolicy() *CommandPolicy {
	return NewCommandPolicy(
		[]string{
			"systemctl restart nginx",
			"journalctl -u nginx *",
			"sudo systemctl reload haproxy",
			"docker restart app-*",
			"# a comment",
			"",
		},
		nil,
	)
}

func TestCommandPolicy_AllowInReadOnlyMode(t *testing.T) {
	v := NewCommandValidator()
	p := testPolicy()
	for _, cmd := range []string{
		"systemctl restart nginx",
		"  systemctl   restart\tnginx ",
		"journalctl -u nginx",
		"journalctl -u nginx --since today -n 100",
		"sudo systemctl reload haproxy",
		"docker restart app-web",
		"systemctl restart nginx && systemctl status nginx",
		"systemctl restart nginx; uptime",
		"systemctl restart nginx || journalctl -u nginx -n 50",
		"journalctl -u nginx -n 50 | grep error",
		"cat /etc/hosts | grep localhost", // read-only rules still apply
	} {
		if err := v.ValidateCommandWithPolicy(cmd, false, p); err != nil {
			t.Errorf("%q must be allowed, got: %v", cmd, err)
		}
	}
}

func TestCommandPolicy_AllowBypassAttempts(t *testing.T) {
	v := NewCommandValidator()
	p := testPolicy()
	for _, cmd := range []string{
		// Not the pattern
		"systemctl restart nginx2",
		"systemctl restart nginx postgres",
		"systemctl restart postgres",
		"sudo systemctl restart nginx",
		"/bin/systemctl restart nginx",
		"LD_PRELOAD=/tmp/x.so systemctl restart nginx",
		"systemctl restart ngin\\x",
		// Chaining with a write command
		"systemctl restart nginx; reboot",
		"systemctl restart nginx && rm -rf /",
		"systemctl restart nginx || reboot",
		"systemctl restart nginx | sh",
		"systemctl restart nginx & reboot",
		"systemctl restart nginx\nreboot",
		"systemctl restart nginx\r\nreboot",
		// Substitution, redirects, subshells inside an allowed command
		"systemctl restart nginx $(reboot)",
		"systemctl restart nginx `reboot`",
		"journalctl -u nginx $(rm -rf /)",
		"journalctl -u nginx `rm -rf /`",
		// (journalctl is read-only anyway, so these use systemctl restart)
		"systemctl restart nginx ${IFS}x",
		"journalctl -u nginx > /etc/passwd",
		"journalctl -u nginx >> /etc/passwd",
		"systemctl restart nginx < /dev/zero",
		"journalctl -u nginx <(reboot)",
		"(reboot)",
		"{ reboot; }",
		"docker restart app-$(reboot)",
	} {
		if err := v.ValidateCommandWithPolicy(cmd, false, p); err == nil {
			t.Errorf("%q must be blocked", cmd)
		}
	}
}

func TestCommandPolicy_DenyAlsoWithWriteAccess(t *testing.T) {
	v := NewCommandValidator()
	p := NewCommandPolicy(nil, []string{"reboot", "rm -rf *", "systemctl stop *"})
	for _, cmd := range []string{
		"reboot",
		"reboot now",
		"sudo reboot",
		"sudo -u root /sbin/reboot now",
		"FOO=1 reboot",
		"uptime && reboot",
		"uptime; reboot",
		"uptime & reboot",
		"uptime\nreboot",
		"echo $(reboot)",
		"echo `reboot`",
		"rm -rf /tmp/x",
		"systemctl stop nginx",
	} {
		err := v.ValidateCommandWithPolicy(cmd, true, p)
		if err == nil || !strings.Contains(err.Error(), "denied pattern") {
			t.Errorf("%q must be denied, got: %v", cmd, err)
		}
	}
	for _, cmd := range []string{
		"rebooter",
		"echo reboot",
		"rm -r /tmp/x",
		"systemctl status nginx",
		"systemctl restart nginx",
	} {
		if err := v.ValidateCommandWithPolicy(cmd, true, p); err != nil {
			t.Errorf("%q must run with write access, got: %v", cmd, err)
		}
	}
}

func TestCommandPolicy_DenyWinsOverAllow(t *testing.T) {
	v := NewCommandValidator()
	p := NewCommandPolicy([]string{"systemctl * nginx"}, []string{"systemctl stop *"})
	if err := v.ValidateCommandWithPolicy("systemctl restart nginx", false, p); err != nil {
		t.Errorf("restart must be allowed: %v", err)
	}
	if err := v.ValidateCommandWithPolicy("systemctl stop nginx", false, p); err == nil || !strings.Contains(err.Error(), "denied pattern") {
		t.Errorf("stop must be denied, got: %v", err)
	}
}

func TestCommandPolicy_NoPolicyIsUnchanged(t *testing.T) {
	v := NewCommandValidator()
	empty := NewCommandPolicy(nil, nil)
	for _, cmd := range []string{
		"cat /etc/hosts", "rm -rf /", "systemctl restart nginx", "ls > /tmp/x",
		"docker ps", "docker run x", "sudo cat /etc/shadow", ":(){ :|:& };:",
	} {
		for _, write := range []bool{false, true} {
			want := v.ValidateCommand(cmd, write)
			for _, p := range []*CommandPolicy{nil, empty} {
				got := v.ValidateCommandWithPolicy(cmd, write, p)
				if (want == nil) != (got == nil) {
					t.Errorf("%q write=%v policy=%v: got %v, want %v", cmd, write, p != nil, got, want)
				}
			}
		}
	}
}

func TestCommandPolicy_BlockedErrorListsAllowPatterns(t *testing.T) {
	v := NewCommandValidator()
	err := v.ValidateCommandWithPolicy("systemctl restart postgres", false, testPolicy())
	if err == nil {
		t.Fatal("must be blocked")
	}
	for _, want := range []string{"systemctl restart nginx", "journalctl -u nginx *", "operator patterns"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must list %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "# a comment") {
		t.Error("comments must not be listed")
	}
}

func TestParseCommandPatterns(t *testing.T) {
	got := ParseCommandPatterns("systemctl restart nginx\r\n\n  # comment\n journalctl   -u nginx * \n")
	if len(got) != 2 || got[0] != "systemctl restart nginx" || got[1] != "journalctl -u nginx *" {
		t.Errorf("string: got %q", got)
	}
	got = ParseCommandPatterns([]interface{}{"reboot", 42, " ", "shutdown"})
	if len(got) != 2 || got[0] != "reboot" || got[1] != "shutdown" {
		t.Errorf("array: got %q", got)
	}
	if got := ParseCommandPatterns(nil); len(got) != 0 {
		t.Errorf("nil: got %q", got)
	}
}

func TestCommandPattern_Matching(t *testing.T) {
	cases := []struct {
		pattern, cmd string
		prefix, want bool
	}{
		{"shutdown", "shutdown", false, true},
		{"shutdown", "shutdown -h now", false, false},
		{"shutdown", "shutdown -h now", true, true},
		{"shutdown *", "shutdown", false, true},
		{"shutdown *", "shutdown -h now", false, true},
		{"docker * nginx", "docker restart nginx", false, true},
		{"docker * nginx", "docker restart", false, false},
		{"nginx*", "nginx-reload", false, true},
		{"a.b", "axb", false, false}, // "." is literal
	}
	for _, c := range cases {
		cp, ok := compileCommandPattern(c.pattern)
		if !ok {
			t.Fatalf("compile %q", c.pattern)
		}
		if got := cp.matches(strings.Fields(c.cmd), c.prefix); got != c.want {
			t.Errorf("%q vs %q prefix=%v: got %v", c.pattern, c.cmd, c.prefix, got)
		}
	}
}

// TestExecuteOnServer_UsesPolicy checks the wiring: a blocked command never
// reaches the connection; an allowed one does (and fails to connect here).
func TestExecuteOnServer_UsesPolicy(t *testing.T) {
	tool := &SSHTool{}
	cfg := &SSHConfig{ConnectionTimeout: 1, CommandTimeout: 1, CommandPolicy: testPolicy()}
	host := &SSHHostConfig{Hostname: "web-1", Address: "127.0.0.1", Port: 1}

	res := tool.executeOnServer(context.Background(), "scope-1", host, "systemctl restart postgres", cfg)
	if !strings.Contains(res.Error, "command blocked") {
		t.Fatalf("want blocked, got %q", res.Error)
	}
	res = tool.executeOnServer(context.Background(), "scope-1", host, "systemctl restart nginx", cfg)
	if strings.Contains(res.Error, "command blocked") || res.Error == "" {
		t.Fatalf("allowed command must pass validation and fail only on connect, got %q", res.Error)
	}
}
