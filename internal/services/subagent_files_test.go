package services

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSubagentDefinitionFiles asserts that the three subagent .md files
// shipped under akmatori_data/agents/ parse cleanly and reference the right
// scoped mount paths. The agent-worker container mounts this directory at
// /home/agent/.pi/agent/agents/ so pi-subagents can register them as
// runbook-searcher / memory-searcher / memory-writer.
func TestSubagentDefinitionFiles(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine current test file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	agentsDir := filepath.Join(repoRoot, "akmatori_data", "agents")

	cases := []struct {
		filename     string
		wantName     string
		wantTools    []string
		bannedTools  []string
		wantScopeDir string
		wantPhrases  []string
	}{
		{
			filename:     "runbook-searcher.md",
			wantName:     "runbook-searcher",
			wantTools:    []string{"read", "grep", "find", "ls"},
			bannedTools:  []string{"bash"},
			wantScopeDir: "/akmatori/runbooks/",
			wantPhrases: []string{
				"out of scope",
				"read-only",
			},
		},
		{
			filename:     "memory-searcher.md",
			wantName:     "memory-searcher",
			wantTools:    []string{"read", "grep", "find", "ls"},
			bannedTools:  []string{"bash"},
			wantScopeDir: "/akmatori/memory/",
			wantPhrases: []string{
				"out of scope",
				"memory-writer",
			},
		},
		{
			filename:     "memory-writer.md",
			wantName:     "memory-writer",
			wantTools:    []string{"read", "edit", "write", "grep", "ls"},
			bannedTools:  []string{"bash"},
			wantScopeDir: "/akmatori/memory/",
			wantPhrases: []string{
				"out of scope",
				"incident_uuid",
				MemoryCreatedByAgent,
				// Deletion contract — the memory-curator system cron relies
				// on this being present so the cron-agent can ask for a
				// tombstone via "Action: delete <slug>".
				"Action: delete",
				"deleted: true",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			path := filepath.Join(agentsDir, tc.filename)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			fm, body, err := splitAgentFrontmatter(raw)
			if err != nil {
				t.Fatalf("split frontmatter: %v", err)
			}

			var meta struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
				Tools       string `yaml:"tools"`
				Model       string `yaml:"model"`
			}
			if err := yaml.Unmarshal(fm, &meta); err != nil {
				t.Fatalf("parse frontmatter yaml: %v\n--- raw ---\n%s", err, fm)
			}

			if meta.Name != tc.wantName {
				t.Errorf("name = %q, want %q", meta.Name, tc.wantName)
			}
			if strings.TrimSpace(meta.Description) == "" {
				t.Errorf("description must not be empty")
			}
			// `model:` is intentionally omitted so the subagent inherits the
			// parent session's provider+model. Hard-coding (e.g.
			// `claude-haiku-4-5`) would break deployments configured for
			// OpenAI/Google/OpenRouter/custom providers.
			if strings.TrimSpace(meta.Model) != "" {
				t.Errorf("model must be empty so the subagent inherits the parent provider/model (got %q)", meta.Model)
			}

			gotTools := splitCommaList(meta.Tools)
			for _, want := range tc.wantTools {
				if !containsString(gotTools, want) {
					t.Errorf("tools missing %q (got %v)", want, gotTools)
				}
			}
			// `bash` is deliberately omitted from system-supplied subagents
			// so a prompt injection cannot dump provider API key env vars
			// from the child `pi` process (the parent-bash spawnHook scrub
			// in agent-worker/src/agent-runner.ts does not extend to the
			// child's bash tool).
			for _, banned := range tc.bannedTools {
				if containsString(gotTools, banned) {
					t.Errorf("tools must not include %q (got %v)", banned, gotTools)
				}
			}

			bodyStr := string(body)
			if !strings.Contains(bodyStr, tc.wantScopeDir) {
				t.Errorf("body must reference scoped mount %q", tc.wantScopeDir)
			}
			for _, phrase := range tc.wantPhrases {
				if !strings.Contains(bodyStr, phrase) {
					t.Errorf("body must mention %q", phrase)
				}
			}
		})
	}
}

// splitAgentFrontmatter extracts the YAML frontmatter block (between the
// leading `---` fences) from a markdown agent definition and returns the
// frontmatter bytes plus the remaining body. Mirrors the trivially-simple
// parser used by pi-subagents itself; we deliberately re-implement here
// instead of importing pi-coding-agent (Node ESM) into Go test code.
func splitAgentFrontmatter(src []byte) ([]byte, []byte, error) {
	const fence = "---"
	if !bytes.HasPrefix(src, []byte(fence)) {
		return nil, nil, errNoFrontmatterFence("missing opening fence")
	}
	rest := src[len(fence):]
	rest = bytes.TrimLeft(rest, "\r\n")
	end := bytes.Index(rest, []byte("\n"+fence))
	if end < 0 {
		return nil, nil, errNoFrontmatterFence("missing closing fence")
	}
	fm := rest[:end]
	body := rest[end+len("\n"+fence):]
	body = bytes.TrimLeft(body, "\r\n")
	return fm, body, nil
}

type errNoFrontmatterFence string

func (e errNoFrontmatterFence) Error() string { return "frontmatter parse: " + string(e) }

func splitCommaList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
