package pagerduty

import (
	"log"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"time"
)

// Cache TTL constants
const (
	ConfigCacheTTL    = 5 * time.Minute  // Credentials cache TTL
	ResponseCacheTTL  = 30 * time.Second // Default API response cache TTL
	CacheCleanupTick  = time.Minute      // Background cleanup interval
	IncidentCacheTTL  = 15 * time.Second // Incidents and alerts cache TTL
	ServiceCacheTTL   = 60 * time.Second // Services and escalation policies cache TTL
)

// PagerDutyTool handles PagerDuty API operations
type PagerDutyTool struct {
	logger        *log.Logger
	configCache   *cache.Cache
	responseCache *cache.Cache
	rateLimiter   *ratelimit.Limiter
}

// NewPagerDutyTool creates a new PagerDuty tool with optional rate limiter
func NewPagerDutyTool(logger *log.Logger, limiter *ratelimit.Limiter) *PagerDutyTool {
	return &PagerDutyTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
}

// Stop cleans up cache resources
func (t *PagerDutyTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}
