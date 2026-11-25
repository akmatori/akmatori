package tools

import (
	"context"

	"github.com/akmatori/mcp-gateway/internal/mcp"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/tools/googledrive"
)

const (
	GoogleDriveRatePerSecond = 5  // requests per second (Sheets allows 60 reads/min per user)
	GoogleDriveBurstCapacity = 10 // burst capacity
)

// registerGoogleDriveTools registers google_drive.* (3 read tools + 2 Sheets
// write tools that are refused unless the instance has gdrive_allow_writes on).
func (r *Registry) registerGoogleDriveTools() {
	r.googleDriveLimit = ratelimit.New(GoogleDriveRatePerSecond, GoogleDriveBurstCapacity)
	r.googleDriveTool = googledrive.NewTool(r.logger, r.googleDriveLimit)
	gd := r.googleDriveTool

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "google_drive.search_files",
			Description: "Search Google Drive files the connector's service account can see (shared with its email), newest first. Returns id, name, type, owner and link.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"query":      {Type: "string", Description: "Words to find in the file name or content"},
					"type":       {Type: "string", Description: "File type filter", Enum: []string{"any", "document", "spreadsheet", "presentation", "folder", "pdf"}},
					"folder":     {Type: "string", Description: "Only files directly in this folder (id or link)"},
					"limit":      {Type: "number", Description: "Results per page (default 20, max 100)"},
					"page_token": {Type: "string", Description: "next_page_token from the previous call"},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gd.SearchFiles(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "google_drive.read_file",
			Description: "Read a Google Drive file as text: Google Docs and Slides as plain text, Google Sheets as CSV per tab, and text files (txt, csv, json, yaml, ...). Cut at max_bytes with a [TRUNCATED ...] marker.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"file":      {Type: "string", Description: "File id or a docs.google.com / drive.google.com link (required)"},
					"max_bytes": {Type: "number", Description: "Byte budget for the text (default 50000, max 200000)"},
				},
				Required: []string{"file"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gd.ReadFile(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "google_drive.sheets_get_values",
			Description: "Google Sheets: without range, list the tabs (title, size); with range (a tab name, or tab!A1:D50), return the cell values as rows. Read the header row before you append.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"spreadsheet": {Type: "string", Description: "Spreadsheet id or docs.google.com/spreadsheets link (required)"},
					"range":       {Type: "string", Description: "Tab name, or A1 range such as Results!A1:F20 (omit to list the tabs)"},
					"max_rows":    {Type: "number", Description: "Rows to return (default 200, max 5000)"},
				},
				Required: []string{"spreadsheet"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gd.SheetsGetValues(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "google_drive.sheets_append_rows",
			Description: "Google Sheets (write): append rows after the last row with data of the table in range. Refused unless the connector has gdrive_allow_writes enabled.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"spreadsheet": {Type: "string", Description: "Spreadsheet id or link (required)"},
					"range":       {Type: "string", Description: "Tab name, or the table's A1 range such as Results!A:F (required)"},
					"rows":        {Type: "array", Description: "Rows to add, each an array of cell values in column order, e.g. [[\"2026-11-25 10:00\", \"api\", 1234, \"Acme\", 12, 40, -28]] (required, max 1000 rows)", Items: &mcp.Items{Type: "array"}},
					"value_input": {Type: "string", Description: "USER_ENTERED (default: parsed like typed in the UI, e.g. dates, numbers, formulas) or RAW (stored as given)", Enum: []string{"USER_ENTERED", "RAW"}},
				},
				Required: []string{"spreadsheet", "range", "rows"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gd.SheetsAppendRows(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "google_drive.sheets_update_values",
			Description: "Google Sheets (write): overwrite the cells of a range with values. Refused unless the connector has gdrive_allow_writes enabled.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"spreadsheet": {Type: "string", Description: "Spreadsheet id or link (required)"},
					"range":       {Type: "string", Description: "A1 range to write, such as Results!A2:C3 (required)"},
					"values":      {Type: "array", Description: "Rows of cell values, e.g. [[\"a\", 1], [\"b\", 2]] (required)", Items: &mcp.Items{Type: "array"}},
					"value_input": {Type: "string", Description: "USER_ENTERED (default) or RAW", Enum: []string{"USER_ENTERED", "RAW"}},
				},
				Required: []string{"spreadsheet", "range", "values"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gd.SheetsUpdateValues(ctx, incidentID, args)
		},
	)

	r.logger.Println("Google Drive tools registered (5 methods)")
}
