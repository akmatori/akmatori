package zabbix

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// BatchItem represents a Zabbix item in batch results
type BatchItem struct {
	ItemID    string `json:"itemid"`
	HostID    string `json:"hostid"`
	Name      string `json:"name"`
	Key       string `json:"key_"`
	ValueType string `json:"value_type"`
	LastValue string `json:"lastvalue"`
	Units     string `json:"units"`
}

// BatchResult contains results grouped by search pattern
type BatchResult struct {
	Pattern string      `json:"pattern"`
	Items   []BatchItem `json:"items"`
	Count   int         `json:"count"`
}

// GetItemsBatch retrieves multiple items with deduplication
// This is more efficient than multiple GetItems calls for investigations
// that need items matching multiple patterns (e.g., cpu, memory, disk)
func (t *ZabbixTool) GetItemsBatch(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	// Extract search patterns
	var searches []string
	if searchesArg, ok := args["searches"].([]interface{}); ok {
		for _, s := range searchesArg {
			if str, ok := s.(string); ok {
				searches = append(searches, str)
			}
		}
	}

	if len(searches) == 0 {
		return "", fmt.Errorf("searches is required and must not be empty")
	}

	// Get optional parameters
	var hostids []interface{}
	if h, ok := args["hostids"]; ok {
		hostids, _ = h.([]interface{})
	}

	// Default to explicit field list to reduce Zabbix DB load
	var output interface{} = []string{"itemid", "hostid", "name", "key_", "value_type", "lastvalue", "units"}
	if o, ok := args["output"].(string); ok {
		output = o
	}

	limitPerSearch := 10
	if l, ok := args["limit_per_search"].(float64); ok {
		limitPerSearch = int(l)
	}

	// Track seen item IDs to deduplicate
	seenItems := make(map[string]bool)
	results := make([]BatchResult, 0, len(searches))

	// Determine startSearch setting (default true for prefix matching performance)
	startSearch := true
	if ss, ok := args["start_search"].(bool); ok {
		startSearch = ss
	}

	// Process each search pattern
	for _, pattern := range searches {
		params := map[string]interface{}{
			"output": output,
			"search": map[string]interface{}{
				"key_": pattern,
			},
			"searchWildcardsEnabled": true,
			"startSearch":            startSearch,
			"limit":                  limitPerSearch * 2, // Fetch extra to account for duplicates
		}

		if len(hostids) > 0 {
			params["hostids"] = hostids
		}

		// Use cached request for efficiency
		result, err := t.cachedRequest(ctx, incidentID, "item.get", params, ResponseCacheTTL)
		if err != nil {
			t.logger.Printf("Batch search failed for pattern '%s': %v", pattern, err)
			// Continue with other patterns
			results = append(results, BatchResult{
				Pattern: pattern,
				Items:   []BatchItem{},
				Count:   0,
			})
			continue
		}

		// Parse items
		var items []BatchItem
		if err := json.Unmarshal(result, &items); err != nil {
			t.logger.Printf("Failed to parse batch results for pattern '%s': %v", pattern, err)
			results = append(results, BatchResult{
				Pattern: pattern,
				Items:   []BatchItem{},
				Count:   0,
			})
			continue
		}

		// Deduplicate items
		uniqueItems := make([]BatchItem, 0)
		for _, item := range items {
			if !seenItems[item.ItemID] {
				seenItems[item.ItemID] = true
				uniqueItems = append(uniqueItems, item)
				if len(uniqueItems) >= limitPerSearch {
					break
				}
			}
		}

		results = append(results, BatchResult{
			Pattern: pattern,
			Items:   uniqueItems,
			Count:   len(uniqueItems),
		})
	}

	// Build response
	response := struct {
		Results      []BatchResult `json:"results"`
		TotalItems   int           `json:"total_items"`
		TotalUnique  int           `json:"total_unique"`
		PatternCount int           `json:"pattern_count"`
	}{
		Results:      results,
		TotalItems:   len(seenItems),
		TotalUnique:  len(seenItems),
		PatternCount: len(searches),
	}

	responseJSON, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("failed to marshal batch response: %w", err)
	}

	return string(responseJSON), nil
}

// GetItemsBatchWithHistory retrieves items and their recent history in one call
// This reduces the number of API calls needed for investigation
func (t *ZabbixTool) GetItemsBatchWithHistory(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	// First get the items
	itemsResult, err := t.GetItemsBatch(ctx, incidentID, args)
	if err != nil {
		return "", err
	}

	// Parse the batch result
	var batchResponse struct {
		Results []BatchResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(itemsResult), &batchResponse); err != nil {
		return "", fmt.Errorf("failed to parse batch results: %w", err)
	}

	// Collect all unique item IDs
	var itemIDs []string
	seenIDs := make(map[string]bool)
	for _, result := range batchResponse.Results {
		for _, item := range result.Items {
			if !seenIDs[item.ItemID] {
				seenIDs[item.ItemID] = true
				itemIDs = append(itemIDs, item.ItemID)
			}
		}
	}

	if len(itemIDs) == 0 {
		return itemsResult, nil // No items found, return as-is
	}

	// Get history limit from args or use default
	historyLimit := 5
	if l, ok := args["history_limit"].(float64); ok {
		historyLimit = int(l)
	}

	// Fetch history for all items in one call (if not too many)
	// Zabbix can handle up to ~100 items efficiently
	maxItemsForHistory := 50
	if len(itemIDs) > maxItemsForHistory {
		itemIDs = itemIDs[:maxItemsForHistory]
	}

	historyParams := map[string]interface{}{
		"output":    "extend",
		"itemids":   itemIDs,
		"limit":     historyLimit * len(itemIDs),
		"sortfield": "clock",
		"sortorder": "DESC",
	}

	historyResult, err := t.cachedRequest(ctx, incidentID, "history.get", historyParams, 15*time.Second)
	if err != nil {
		// Return items without history if history fetch fails
		t.logger.Printf("Failed to fetch history for batch items: %v", err)
		return itemsResult, nil
	}

	// Build combined response
	response := struct {
		Items   json.RawMessage `json:"items"`
		History json.RawMessage `json:"history"`
	}{
		Items:   json.RawMessage(itemsResult),
		History: historyResult,
	}

	responseJSON, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("failed to marshal batch with history response: %w", err)
	}

	return string(responseJSON), nil
}
