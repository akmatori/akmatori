package slack

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// channelLabelTTL bounds how long a resolved label is reused. Channels get
// renamed and a bot's membership changes, so labels are refreshed rather than
// cached for the process lifetime. Negative results are cached under the same
// TTL so a channel the bot cannot see does not trigger a conversations.info
// call on every message in it.
const channelLabelTTL = time.Hour

type channelLabel struct {
	label     string
	expiresAt time.Time
}

// ResolveLabel maps a Slack channel ID to a human-readable label for use in
// agent prompts: "#csqo_downtime" for a channel, "direct message" for a DM.
//
// This is the reverse of ResolveChannel, and it exists because the channel a
// message arrived in is a routing signal the agent needs. Skills select their
// workflow by channel ("alerts in #csqo_downtime fire per product, so skip the
// project-level sweep"), and without a label in the prompt the agent cannot
// apply those rules — it silently falls back to the generic workflow.
//
// Returns "" when the ID is empty or the label cannot be determined, so the
// caller can omit the line entirely rather than print a useless raw ID.
func (r *ChannelResolver) ResolveLabel(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || r == nil || r.client == nil {
		return ""
	}

	r.labelMu.RLock()
	entry, ok := r.labelCache[channelID]
	r.labelMu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.label
	}

	label, err := r.lookupLabel(channelID)
	if err != nil {
		// Cache the miss: a bot that is not in the channel stays out of it,
		// and conversations.info is rate-limited (Tier 3).
		slog.Warn("failed to resolve Slack channel label", "channel_id", channelID, "err", err)
		label = ""
	}

	r.labelMu.Lock()
	if r.labelCache == nil {
		r.labelCache = make(map[string]channelLabel)
	}
	r.labelCache[channelID] = channelLabel{label: label, expiresAt: time.Now().Add(channelLabelTTL)}
	r.labelMu.Unlock()

	return label
}

// lookupLabel fetches the channel name via conversations.info.
func (r *ChannelResolver) lookupLabel(channelID string) (string, error) {
	info, err := r.client.GetConversationInfo(&slack.GetConversationInfoInput{
		ChannelID: channelID,
	})
	if err != nil {
		return "", fmt.Errorf("conversations.info for %s: %w", channelID, err)
	}
	if info == nil {
		return "", fmt.Errorf("conversations.info for %s returned no channel", channelID)
	}

	// IMs and multi-person IMs have no name; naming the peer would leak a
	// user ID into the prompt without adding routing value.
	if info.IsIM {
		return "direct message", nil
	}
	if info.IsMpIM {
		return "group direct message", nil
	}
	if info.Name == "" {
		return "", fmt.Errorf("conversations.info for %s returned an empty name", channelID)
	}
	return "#" + info.Name, nil
}

// ClearLabelCache drops every cached channel label.
func (r *ChannelResolver) ClearLabelCache() {
	r.labelMu.Lock()
	defer r.labelMu.Unlock()
	r.labelCache = make(map[string]channelLabel)
	slog.Info("Cleared channel label cache")
}
