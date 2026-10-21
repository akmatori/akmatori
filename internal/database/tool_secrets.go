package database

// ToolSecretMask is the placeholder the API returns in place of a stored tool
// secret. Sending it back on update (or omitting the key) keeps the stored
// value, so the UI can round-trip a settings object without ever seeing the
// plaintext secret.
const ToolSecretMask = "********"

// toolSecretSettingKeys names the top-level ToolInstance.Settings keys that
// hold credentials. It mirrors the properties marked `Secret: true` in
// mcp-gateway/internal/tools/schemas.go (a separate Go module, so it cannot be
// imported); TestToolSecretSettingKeys_MatchGatewaySchemas guards the drift.
// SSH private keys live nested under "ssh_keys" and are handled separately.
var toolSecretSettingKeys = map[string]struct{}{
	"zabbix_token":         {},
	"zabbix_password":      {},
	"vm_bearer_token":      {},
	"vm_password":          {},
	"grafana_api_token":    {},
	"catchpoint_api_token": {},
	"ch_password":          {},
	"pg_password":          {},
	"pagerduty_api_token":  {},
	"netbox_api_token":     {},
	"k8s_token":            {},
	"jira_api_token":       {},
}

// IsToolSecretSetting reports whether a top-level tool settings key holds a
// credential that must never be returned over the API in plaintext.
func IsToolSecretSetting(key string) bool {
	_, ok := toolSecretSettingKeys[key]
	return ok
}

// RedactToolSettings returns a copy of settings that is safe to return over
// the API: each ssh_keys entry loses its private_key, and every non-empty
// secret setting is replaced with ToolSecretMask. Empty secrets stay empty so
// the UI can still tell "not configured" apart from "configured". The input
// is never mutated (it may be shared with a cached or preloaded row).
func RedactToolSettings(settings JSONB) JSONB {
	if settings == nil {
		return nil
	}
	out := make(JSONB, len(settings))
	for k, v := range settings {
		switch {
		case k == "ssh_keys":
			out[k] = redactSSHKeys(v)
		case IsToolSecretSetting(k):
			if s, ok := v.(string); (ok && s == "") || v == nil {
				out[k] = v
			} else {
				out[k] = ToolSecretMask
			}
		default:
			out[k] = v
		}
	}
	return out
}

// redactSSHKeys copies an ssh_keys value with private_key removed from each
// entry. Unknown shapes are dropped rather than passed through.
func redactSSHKeys(v interface{}) interface{} {
	keys, ok := v.([]interface{})
	if !ok {
		return []interface{}{}
	}
	out := make([]interface{}, 0, len(keys))
	for _, keyData := range keys {
		keyMap, ok := keyData.(map[string]interface{})
		if !ok {
			continue
		}
		clean := make(map[string]interface{}, len(keyMap))
		for k, val := range keyMap {
			if k == "private_key" {
				continue
			}
			clean[k] = val
		}
		out = append(out, clean)
	}
	return out
}

// RestoreToolSecretSettings fills in stored secrets on an incoming settings
// object before it replaces the stored one: a secret key that is omitted, or
// whose value is ToolSecretMask, keeps its stored value. An explicit empty
// string clears the secret. incoming is modified in place.
func RestoreToolSecretSettings(incoming, stored JSONB) {
	if incoming == nil {
		return
	}
	for key := range toolSecretSettingKeys {
		newVal, present := incoming[key]
		storedVal, hasStored := stored[key]
		if present {
			if s, ok := newVal.(string); !ok || s != ToolSecretMask {
				continue
			}
			if !hasStored {
				// Placeholder sent with nothing stored: do not persist the mask.
				delete(incoming, key)
				continue
			}
		}
		if hasStored {
			incoming[key] = storedVal
		}
	}
}
