package database

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

func TestRedactToolSettings(t *testing.T) {
	stored := JSONB{
		"zabbix_url":   "https://zbx.example.com",
		"zabbix_token": "real-token",
		"pg_password":  "",
		"k8s_token":    nil,
		"ssh_keys": []interface{}{
			map[string]interface{}{"id": "k1", "name": "default", "private_key": "PEM", "is_default": true},
		},
	}

	got := RedactToolSettings(stored)

	if got["zabbix_url"] != "https://zbx.example.com" {
		t.Errorf("non-secret changed: %v", got["zabbix_url"])
	}
	if got["zabbix_token"] != ToolSecretMask {
		t.Errorf("zabbix_token = %v, want mask", got["zabbix_token"])
	}
	if got["pg_password"] != "" {
		t.Errorf("empty secret must stay empty, got %v", got["pg_password"])
	}
	if got["k8s_token"] != nil {
		t.Errorf("nil secret must stay nil, got %v", got["k8s_token"])
	}
	keys := got["ssh_keys"].([]interface{})
	key := keys[0].(map[string]interface{})
	if _, ok := key["private_key"]; ok {
		t.Error("private_key must be stripped")
	}
	if key["name"] != "default" || key["id"] != "k1" {
		t.Errorf("ssh key metadata lost: %v", key)
	}

	// The input must not be mutated.
	if stored["zabbix_token"] != "real-token" {
		t.Error("input secret was mutated")
	}
	if stored["ssh_keys"].([]interface{})[0].(map[string]interface{})["private_key"] != "PEM" {
		t.Error("input private_key was mutated")
	}

	if RedactToolSettings(nil) != nil {
		t.Error("nil settings must stay nil")
	}
}

func TestRestoreToolSecretSettings(t *testing.T) {
	stored := JSONB{"zabbix_token": "real-token", "zabbix_password": "pw"}

	tests := []struct {
		name     string
		incoming JSONB
		want     JSONB
	}{
		{"mask keeps stored", JSONB{"zabbix_token": ToolSecretMask, "zabbix_password": ToolSecretMask},
			JSONB{"zabbix_token": "real-token", "zabbix_password": "pw"}},
		{"omitted keeps stored", JSONB{}, JSONB{"zabbix_token": "real-token", "zabbix_password": "pw"}},
		{"new value wins", JSONB{"zabbix_token": "new"}, JSONB{"zabbix_token": "new", "zabbix_password": "pw"}},
		{"empty clears", JSONB{"zabbix_token": ""}, JSONB{"zabbix_token": "", "zabbix_password": "pw"}},
		{"mask without stored is dropped", JSONB{"pg_password": ToolSecretMask},
			JSONB{"zabbix_token": "real-token", "zabbix_password": "pw"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RestoreToolSecretSettings(tt.incoming, stored)
			if len(tt.incoming) != len(tt.want) {
				t.Fatalf("got %v, want %v", tt.incoming, tt.want)
			}
			for k, v := range tt.want {
				if tt.incoming[k] != v {
					t.Errorf("%s = %v, want %v", k, tt.incoming[k], v)
				}
			}
		})
	}
}

// TestToolSecretSettingKeys_MatchGatewaySchemas keeps toolSecretSettingKeys
// in sync with the properties marked Secret in the MCP gateway schemas. The
// gateway is a separate Go module, so the schema file is scanned as text.
func TestToolSecretSettingKeys_MatchGatewaySchemas(t *testing.T) {
	path := filepath.Join("..", "..", "mcp-gateway", "internal", "tools", "schemas.go")
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("gateway schemas not available: %v", err)
	}
	defer f.Close()

	keyRe := regexp.MustCompile(`^\s*"([a-z0-9_]+)":\s*\{`)
	secretRe := regexp.MustCompile(`^\s*Secret:\s*true`)

	schemaSecrets := map[string]struct{}{}
	lastKey := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := keyRe.FindStringSubmatch(line); m != nil {
			lastKey = m[1]
			continue
		}
		if secretRe.MatchString(line) && lastKey != "" {
			schemaSecrets[lastKey] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan schemas.go: %v", err)
	}
	// private_key is nested under ssh_keys and stripped by redactSSHKeys.
	delete(schemaSecrets, "private_key")

	var missing, extra []string
	for k := range schemaSecrets {
		if !IsToolSecretSetting(k) {
			missing = append(missing, k)
		}
	}
	for k := range toolSecretSettingKeys {
		if _, ok := schemaSecrets[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("secret settings in gateway schemas but not in toolSecretSettingKeys: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("toolSecretSettingKeys entries not marked Secret in gateway schemas: %v", extra)
	}
}
