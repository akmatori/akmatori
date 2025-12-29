package slack

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/slack-go/slack"
)

// ChannelResolver resolves channel names to IDs
type ChannelResolver struct {
	client *slack.Client
	cache  map[string]string // name -> id
	mu     sync.RWMutex
}

// NewChannelResolver creates a new channel resolver
func NewChannelResolver(client *slack.Client) *ChannelResolver {
	return &ChannelResolver{
		client: client,
		cache:  make(map[string]string),
	}
}

// ResolveChannel resolves a channel name or ID to a channel ID
// Accepts:
// - Channel ID (C01234567890)
// - Channel name (#alerts or alerts)
// Returns the channel ID and an error if not found
func (r *ChannelResolver) ResolveChannel(nameOrID string) (string, error) {
	if nameOrID == "" {
		return "", fmt.Errorf("channel name/ID is empty")
	}

	// If it looks like a channel ID (starts with C and is alphanumeric), return as-is
	if isChannelID(nameOrID) {
		return nameOrID, nil
	}

	// Remove # prefix if present
	channelName := strings.TrimPrefix(nameOrID, "#")

	// Check cache first
	r.mu.RLock()
	if id, ok := r.cache[channelName]; ok {
		r.mu.RUnlock()
		log.Printf("Resolved channel '%s' to '%s' (cached)", channelName, id)
		return id, nil
	}
	r.mu.RUnlock()

	// Not in cache, look it up via Slack API
	id, err := r.lookupChannel(channelName)
	if err != nil {
		return "", err
	}

	// Cache the result
	r.mu.Lock()
	r.cache[channelName] = id
	r.mu.Unlock()

	log.Printf("Resolved channel '%s' to '%s'", channelName, id)
	return id, nil
}

// lookupChannel looks up a channel by name using the Slack API
func (r *ChannelResolver) lookupChannel(name string) (string, error) {
	// Try public channels first
	channels, _, err := r.client.GetConversations(&slack.GetConversationsParameters{
		ExcludeArchived: true,
		Limit:           1000,
		Types:           []string{"public_channel"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to list public channels: %w", err)
	}

	// Search for matching name in public channels
	for _, channel := range channels {
		if channel.Name == name {
			return channel.ID, nil
		}
	}

	// Try private channels if not found in public
	privateChannels, _, err := r.client.GetConversations(&slack.GetConversationsParameters{
		ExcludeArchived: true,
		Limit:           1000,
		Types:           []string{"private_channel"},
	})
	if err != nil {
		log.Printf("Warning: Failed to list private channels: %v", err)
		// Don't fail, just return not found for public channels
		return "", fmt.Errorf("channel '%s' not found", name)
	}

	// Search for matching name in private channels
	for _, channel := range privateChannels {
		if channel.Name == name {
			return channel.ID, nil
		}
	}

	return "", fmt.Errorf("channel '%s' not found", name)
}

// ClearCache clears the channel name resolution cache
func (r *ChannelResolver) ClearCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = make(map[string]string)
	log.Printf("Cleared channel resolution cache")
}

// isChannelID checks if a string looks like a Slack channel ID
// Channel IDs start with C and are followed by alphanumeric characters
func isChannelID(s string) bool {
	if len(s) < 9 || len(s) > 15 {
		return false
	}
	if !strings.HasPrefix(s, "C") {
		return false
	}
	// Check if rest is alphanumeric
	for _, c := range s[1:] {
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
