package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// ComputeAlertFingerprint returns a 32-char hex string that is stable across
// case variants of alertName and targetHost. It is derived from
// sha256(json([sourceUUID, lower(alertName), lower(targetHost)])) truncated to
// the first 32 hex characters (16 bytes of entropy).
//
// This fingerprint identifies the alert's logical identity (same rule on the
// same host) regardless of how the adapter capitalizes the rule name or host.
// It is intentionally distinct from:
//   - SourceFingerprint: the adapter-supplied external ID for exact dedup
//   - alertSpawnKey: includes SourceFingerprint for per-burst singleflight dedup
func ComputeAlertFingerprint(sourceUUID, alertName, targetHost string) string {
	tuple, _ := json.Marshal([]string{
		sourceUUID,
		strings.ToLower(alertName),
		strings.ToLower(targetHost),
	})
	h := sha256.Sum256(tuple)
	return hex.EncodeToString(h[:])[:32]
}
