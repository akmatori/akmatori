// Package googledrive implements the google_drive.* gateway tools: search and
// read Google Drive files (Docs, Sheets and Slides as text), read Sheets
// ranges, and append or update Sheets rows.
//
// The tool signs in as a Google service account (JSON key in the instance
// settings). It sees only the files shared with the service account's email,
// or, with gdrive_impersonate_user and domain-wide delegation, the files of
// that Workspace user. Writes are refused unless gdrive_allow_writes is on.
package googledrive

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/validation"
)

const (
	toolType = "google_drive"

	ConfigCacheTTL   = 5 * time.Minute
	CacheCleanupTick = time.Minute

	defaultDriveURL  = "https://www.googleapis.com/drive/v3"
	defaultSheetsURL = "https://sheets.googleapis.com/v4"

	maxJSONResponseBytes = 10 * 1024 * 1024
	// maxExportBytes caps an export or download read from Google; the agent
	// still only gets max_bytes of it.
	maxExportBytes = 20 * 1024 * 1024

	DefaultMaxBytes = 50_000
	MaxMaxBytes     = 200_000

	defaultSearchLimit = 20
	maxSearchLimit     = 100

	defaultMaxRows = 200
	maxMaxRows     = 5_000
	csvChunkRows   = 1_000 // rows per values request in read_file
	csvMaxChunks   = 20    // read_file reads at most 20k rows of one tab

	// Write caps per call.
	maxWriteRows  = 1_000
	maxWriteCells = 50_000
)

// Config holds one Google Drive instance's settings.
type Config struct {
	ServiceAccountJSON string
	ImpersonateUser    string
	AllowWrites        bool
	Timeout            int
}

// Tool handles Google Drive and Sheets API calls.
type Tool struct {
	logger      *log.Logger
	configCache *cache.Cache
	rateLimiter *ratelimit.Limiter
	tokens      *tokenSource

	driveURL  string
	sheetsURL string
	// transport is nil in production (a new proxy-aware transport per call);
	// tests set it.
	transport http.RoundTripper
}

// NewTool creates the tool with an optional rate limiter.
func NewTool(logger *log.Logger, limiter *ratelimit.Limiter) *Tool {
	return &Tool{
		logger:      logger,
		configCache: cache.New(ConfigCacheTTL, CacheCleanupTick),
		rateLimiter: limiter,
		tokens:      newTokenSource(),
		driveURL:    defaultDriveURL,
		sheetsURL:   defaultSheetsURL,
	}
}

// Stop releases the caches.
func (t *Tool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
}

func configCacheKey(incidentID, logicalName string) string {
	if logicalName != "" {
		return "creds:logical:" + toolType + ":" + logicalName
	}
	return "creds:" + incidentID + ":" + toolType
}

func extractLogicalName(args map[string]interface{}) string {
	if v, ok := args["logical_name"].(string); ok {
		return v
	}
	return ""
}

func clampTimeout(timeout int) int {
	switch {
	case timeout <= 0:
		return 30
	case timeout < 5:
		return 5
	case timeout > 300:
		return 300
	}
	return timeout
}

func buildConfig(settings map[string]interface{}) *Config {
	c := &Config{Timeout: 30}
	if v, ok := settings["gdrive_service_account_json"].(string); ok {
		c.ServiceAccountJSON = v
	}
	if v, ok := settings["gdrive_impersonate_user"].(string); ok {
		c.ImpersonateUser = strings.TrimSpace(v)
	}
	if v, ok := settings["gdrive_allow_writes"].(bool); ok {
		c.AllowWrites = v
	}
	if v, ok := settings["gdrive_timeout"].(float64); ok {
		c.Timeout = int(v)
	}
	c.Timeout = clampTimeout(c.Timeout)
	return c
}

func (t *Tool) getConfig(ctx context.Context, incidentID, logicalName string) (*Config, error) {
	key := configCacheKey(incidentID, logicalName)
	if cached, ok := t.configCache.Get(key); ok {
		if c, ok := cached.(*Config); ok {
			return c, nil
		}
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, toolType, nil, logicalName)
	if err != nil {
		return nil, fmt.Errorf("failed to get Google Drive credentials: %w", err)
	}
	c := buildConfig(creds.Settings)
	t.configCache.Set(key, c)
	return c, nil
}

// verifyWriteGate re-reads the instance settings from the database (not the
// 5-minute cache), so turning gdrive_allow_writes off takes effect at once.
// Without a database (unit tests) the given config decides.
func (t *Tool) verifyWriteGate(ctx context.Context, incidentID, logicalName string, cached *Config) (*Config, error) {
	cfg := cached
	if database.DB != nil {
		creds, err := database.ResolveToolCredentials(ctx, incidentID, toolType, nil, logicalName)
		if err != nil {
			return nil, fmt.Errorf("failed to verify the Google Drive write setting: %w", err)
		}
		cfg = buildConfig(creds.Settings)
		t.configCache.Set(configCacheKey(incidentID, logicalName), cfg)
	}
	if cfg == nil || !cfg.AllowWrites {
		return cfg, fmt.Errorf("writes are disabled for this Google Drive connector; an operator must enable gdrive_allow_writes")
	}
	return cfg, nil
}

// --- file ids ---

var (
	fileIDRe     = regexp.MustCompile(`^[A-Za-z0-9_-]{10,200}$`)
	urlPathIDRe  = regexp.MustCompile(`/d/([A-Za-z0-9_-]{10,200})`)
	folderPathRe = regexp.MustCompile(`/folders/([A-Za-z0-9_-]{10,200})`)
	googleDocHst = map[string]bool{"docs.google.com": true, "drive.google.com": true, "sheets.google.com": true, "slides.google.com": true}
)

// ParseFileID accepts a bare file id or a docs.google.com / drive.google.com
// link and returns the file id.
func ParseFileID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("a file id or link is required")
	}
	if fileIDRe.MatchString(raw) {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a Google Drive file id or link", raw)
	}
	if !googleDocHst[strings.ToLower(u.Hostname())] {
		return "", fmt.Errorf("%q is not a docs.google.com or drive.google.com link", raw)
	}
	if m := urlPathIDRe.FindStringSubmatch(u.Path); m != nil {
		return m[1], nil
	}
	if m := folderPathRe.FindStringSubmatch(u.Path); m != nil {
		return m[1], nil
	}
	if id := u.Query().Get("id"); fileIDRe.MatchString(id) {
		return id, nil
	}
	return "", fmt.Errorf("no file id found in %q", raw)
}

// --- A1 ranges ---

var (
	a1CellsRe = regexp.MustCompile(`^\$?[A-Za-z]{0,3}\$?\d*(:\$?[A-Za-z]{0,3}\$?\d*)?$`)
	// colRangeRe is a whole-column range such as A:F (no row numbers).
	colRangeRe = regexp.MustCompile(`^\$?[A-Za-z]{1,3}:\$?[A-Za-z]{1,3}$`)
	// openRowsRe is a column range with an optional start row and no end
	// row (A:F, A2:F): it reaches to the last row of the tab.
	openRowsRe = regexp.MustCompile(`^(\$?[A-Za-z]{1,3})\$?(\d*):(\$?[A-Za-z]{1,3})$`)
)

// quoteSheet quotes a tab name for A1 notation.
func quoteSheet(name string) string {
	if strings.HasPrefix(name, "'") && strings.HasSuffix(name, "'") && len(name) >= 2 {
		return name
	}
	return "'" + strings.ReplaceAll(name, "'", "''") + "'"
}

// NormalizeRange turns a tab name ("Daily checks 2026") or a range
// ("Daily checks 2026!A1:D") into valid A1 notation: the tab part is quoted.
// A bare cell range ("A1:D10", "A:F") is passed through (it means the first
// tab); quote a tab that is named like a range ("'A:F'").
func NormalizeRange(r string) (string, error) {
	r = strings.TrimSpace(r)
	if r == "" {
		return "", fmt.Errorf("range is required (a tab name, or tab!A1:D10)")
	}
	if i := strings.LastIndex(r, "!"); i >= 0 {
		sheet, cells := strings.TrimSpace(r[:i]), strings.TrimSpace(r[i+1:])
		if sheet == "" {
			return "", fmt.Errorf("range %q has no tab name before !", r)
		}
		return quoteSheet(sheet) + "!" + cells, nil
	}
	if (strings.ContainsAny(r, "0123456789") && a1CellsRe.MatchString(r)) || colRangeRe.MatchString(r) {
		return r, nil
	}
	return quoteSheet(r), nil
}

// boundRows limits an open-ended normalized range to n rows, so that Google
// does not send a whole tab when only the first rows are used: 'Tab' →
// 'Tab'!1:n, A:F → A1:Fn, 'Tab'!A2:F → 'Tab'!A2:F(n+1). Ranges that already
// have an end row are returned unchanged.
func boundRows(a1 string, n int) string {
	prefix, cells := "", a1
	if i := strings.LastIndex(a1, "!"); i >= 0 {
		prefix, cells = a1[:i+1], a1[i+1:]
	} else if strings.HasPrefix(a1, "'") {
		return fmt.Sprintf("%s!1:%d", a1, n)
	}
	if m := openRowsRe.FindStringSubmatch(cells); m != nil {
		start := 1
		if m[2] != "" {
			start, _ = strconv.Atoi(m[2])
		}
		return fmt.Sprintf("%s%s%d:%s%d", prefix, m[1], start, m[3], start+n-1)
	}
	return a1
}

// --- HTTP ---

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "Google returned 401: the service-account sign-in was not accepted: " + e.Message
	case http.StatusForbidden:
		return "Google returned 403: the service account has no access (share the file with its email, with Editor rights for writes; or the API is not enabled in its Google Cloud project): " + e.Message
	case http.StatusNotFound:
		return "Google returned 404: no such file, or it is not shared with the service account: " + e.Message
	case http.StatusTooManyRequests:
		return "Google returned 429: quota exceeded, try again later: " + e.Message
	}
	return fmt.Sprintf("Google returned HTTP %d: %s", e.Status, e.Message)
}

func (t *Tool) httpClient(cfg *Config) *http.Client {
	rt := t.transport
	if rt == nil {
		// Google is a fixed public endpoint, so the runtime HTTP(S)_PROXY is
		// used like for LLM calls: closed networks reach Google only through it.
		rt = &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true}
	}
	return &http.Client{
		Timeout:   time.Duration(cfg.Timeout) * time.Second,
		Transport: rt,
		// Go drops the Authorization header on a redirect to another host;
		// refuse non-https redirects as well.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" && t.transport == nil {
				return fmt.Errorf("refused a redirect to %s", req.URL.Scheme)
			}
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

// do performs one authorized request and returns at most limit bytes of the
// body; over reports a longer body.
func (t *Tool) do(ctx context.Context, cfg *Config, write bool, method, full string, body interface{}, limit int) (data []byte, over bool, err error) {
	sa, err := parseServiceAccount(cfg.ServiceAccountJSON)
	if err != nil {
		return nil, false, err
	}
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, false, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}
	client := t.httpClient(cfg)
	scope := scopeRead
	if write {
		scope = scopeWrite
	}
	tok, err := t.tokens.token(ctx, client, sa, cfg.ImpersonateUser, scope)
	if err != nil {
		return nil, false, err
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, false, fmt.Errorf("encode request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, reqBody)
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if u, perr := url.Parse(full); perr == nil {
		t.logger.Printf("Google API call: %s %s%s", method, u.Host, u.Path)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("request to Google failed: %w", err)
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, false, fmt.Errorf("read Google response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, &apiError{Status: resp.StatusCode, Message: googleErrorMessage(data)}
	}
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

func googleErrorMessage(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	msg := strings.TrimSpace(string(data))
	msg, _ = cut(msg, 300)
	return msg
}

func (t *Tool) getJSON(ctx context.Context, cfg *Config, full string, out interface{}) error {
	data, over, err := t.do(ctx, cfg, false, http.MethodGet, full, nil, maxJSONResponseBytes)
	if err != nil {
		return err
	}
	if over {
		return fmt.Errorf("Google response exceeds %d MB", maxJSONResponseBytes/(1024*1024))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode Google response: %w", err)
	}
	return nil
}

// --- small helpers ---

// cut keeps at most n bytes of s on a rune boundary.
func cut(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for i := 0; i < utf8.UTFMax-1 && n > 0 && !utf8.RuneStart(s[n]); i++ {
		n--
	}
	return s[:n], true
}

func marshal(v interface{}) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func stringArg(args map[string]interface{}, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func intArg(args map[string]interface{}, key string, def, min, max int) int {
	n := def
	switch v := args[key].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	case string:
		if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			n = p
		}
	}
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	return n
}

// fileArg reads the file id from file_id, file or url (any of them may be a
// link).
func fileArg(args map[string]interface{}, keys ...string) (string, error) {
	for _, k := range keys {
		if s := stringArg(args, k); s != "" {
			return ParseFileID(s)
		}
	}
	return "", fmt.Errorf("%s is required (a file id or a Google Docs/Drive link)%s", keys[0], validation.SuggestParam(keys[0], args))
}

// --- Drive ---

const (
	mimeFolder = "application/vnd.google-apps.folder"
	mimeDoc    = "application/vnd.google-apps.document"
	mimeSheet  = "application/vnd.google-apps.spreadsheet"
	mimeSlides = "application/vnd.google-apps.presentation"
)

var mimeAliases = map[string]string{
	"document":     mimeDoc,
	"spreadsheet":  mimeSheet,
	"presentation": mimeSlides,
	"folder":       mimeFolder,
	"pdf":          "application/pdf",
}

type driveFile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MimeType     string `json:"mimeType"`
	ModifiedTime string `json:"modifiedTime,omitempty"`
	WebViewLink  string `json:"webViewLink,omitempty"`
	Size         string `json:"size,omitempty"`
	Owners       []struct {
		EmailAddress string `json:"emailAddress"`
	} `json:"owners,omitempty"`
}

func shortFile(f driveFile) map[string]interface{} {
	m := map[string]interface{}{"id": f.ID, "name": f.Name, "type": typeName(f.MimeType), "modified": f.ModifiedTime, "link": f.WebViewLink}
	if len(f.Owners) > 0 {
		m["owner"] = f.Owners[0].EmailAddress
	}
	if f.Size != "" {
		m["size"] = f.Size
	}
	return m
}

func typeName(mime string) string {
	for k, v := range mimeAliases {
		if v == mime {
			return k
		}
	}
	return mime
}

// driveQueryString escapes a value for a Drive query string literal.
func driveQueryString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return "'" + strings.ReplaceAll(s, `'`, `\'`) + "'"
}

// SearchFiles lists files the service account can see.
func (t *Tool) SearchFiles(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	clauses := []string{"trashed = false"}
	if q := stringArg(args, "query"); q != "" {
		lit := driveQueryString(q)
		clauses = append(clauses, "(name contains "+lit+" or fullText contains "+lit+")")
	}
	if mt := strings.ToLower(stringArg(args, "type")); mt != "" && mt != "any" {
		full, ok := mimeAliases[mt]
		if !ok {
			return "", fmt.Errorf("type must be one of any, document, spreadsheet, presentation, folder, pdf")
		}
		clauses = append(clauses, "mimeType = "+driveQueryString(full))
	}
	if folder := stringArg(args, "folder"); folder != "" {
		id, err := ParseFileID(folder)
		if err != nil {
			return "", err
		}
		clauses = append(clauses, driveQueryString(id)+" in parents")
	}
	q := url.Values{
		"q":                         {strings.Join(clauses, " and ")},
		"pageSize":                  {strconv.Itoa(intArg(args, "limit", defaultSearchLimit, 1, maxSearchLimit))},
		"fields":                    {"nextPageToken,files(id,name,mimeType,modifiedTime,webViewLink,size,owners(emailAddress))"},
		"orderBy":                   {"modifiedTime desc"},
		"supportsAllDrives":         {"true"},
		"includeItemsFromAllDrives": {"true"},
		"corpora":                   {"allDrives"},
	}
	if pt := stringArg(args, "page_token"); pt != "" {
		q.Set("pageToken", pt)
	}
	var out struct {
		NextPageToken string      `json:"nextPageToken"`
		Files         []driveFile `json:"files"`
	}
	if err := t.getJSON(ctx, cfg, t.driveURL+"/files?"+q.Encode(), &out); err != nil {
		return "", err
	}
	files := make([]map[string]interface{}, 0, len(out.Files))
	for _, f := range out.Files {
		files = append(files, shortFile(f))
	}
	res := map[string]interface{}{"files": files}
	if out.NextPageToken != "" {
		res["next_page_token"] = out.NextPageToken
	}
	if len(files) == 0 {
		res["hint"] = "Nothing found. The service account sees only files shared with its email (or with the impersonated user)."
	}
	return marshal(res)
}

func isTextMime(m string) bool {
	if strings.HasPrefix(m, "text/") {
		return true
	}
	switch m {
	case "application/json", "application/xml", "application/x-yaml", "application/yaml", "application/javascript", "application/x-sh":
		return true
	}
	return false
}

// ReadFile returns a file as text: Docs and Slides as plain text, Sheets as
// CSV per tab, text files as they are.
func (t *Tool) ReadFile(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	id, err := fileArg(args, "file")
	if err != nil {
		return "", err
	}
	maxBytes := intArg(args, "max_bytes", DefaultMaxBytes, 1000, MaxMaxBytes)
	var f driveFile
	meta := url.Values{"fields": {"id,name,mimeType,modifiedTime,webViewLink,size,owners(emailAddress)"}, "supportsAllDrives": {"true"}}
	if err := t.getJSON(ctx, cfg, t.driveURL+"/files/"+url.PathEscape(id)+"?"+meta.Encode(), &f); err != nil {
		return "", err
	}
	res := shortFile(f)
	var text string
	var over bool
	switch {
	case f.MimeType == mimeSheet:
		text, over, err = t.sheetAsCSV(ctx, cfg, id, maxBytes)
	case f.MimeType == mimeDoc || f.MimeType == mimeSlides:
		var data []byte
		data, over, err = t.do(ctx, cfg, false, http.MethodGet, t.driveURL+"/files/"+url.PathEscape(id)+"/export?mimeType=text%2Fplain", nil, maxExportBytes)
		text = strings.TrimPrefix(string(data), "\ufeff")
	case isTextMime(f.MimeType):
		var data []byte
		data, over, err = t.do(ctx, cfg, false, http.MethodGet, t.driveURL+"/files/"+url.PathEscape(id)+"?alt=media&supportsAllDrives=true", nil, maxExportBytes)
		text = string(data)
	case f.MimeType == mimeFolder:
		return "", fmt.Errorf("%q is a folder; use google_drive.search_files with folder=%s", f.Name, f.ID)
	default:
		return "", fmt.Errorf("%q is a %s file; only Google Docs, Sheets, Slides and text files can be read as text (open %s)", f.Name, f.MimeType, f.WebViewLink)
	}
	if err != nil {
		return "", err
	}
	text, cutNow := cut(text, maxBytes)
	if over || cutNow {
		text += fmt.Sprintf("\n[TRUNCATED at %d bytes; raise max_bytes (max %d) or read a range with google_drive.sheets_get_values]", maxBytes, MaxMaxBytes)
		res["truncated"] = true
	}
	res["content"] = text
	return marshal(res)
}

// --- Sheets ---

type sheetProps struct {
	SheetID        int    `json:"sheetId"`
	Title          string `json:"title"`
	Index          int    `json:"index"`
	GridProperties struct {
		RowCount    int `json:"rowCount"`
		ColumnCount int `json:"columnCount"`
	} `json:"gridProperties"`
}

type spreadsheetMeta struct {
	Properties struct {
		Title string `json:"title"`
	} `json:"properties"`
	Sheets []struct {
		Properties sheetProps `json:"properties"`
	} `json:"sheets"`
}

func (t *Tool) spreadsheetMeta(ctx context.Context, cfg *Config, id string) (*spreadsheetMeta, error) {
	var m spreadsheetMeta
	q := url.Values{"fields": {"properties.title,sheets.properties(sheetId,title,index,gridProperties(rowCount,columnCount))"}}
	if err := t.getJSON(ctx, cfg, t.sheetsURL+"/spreadsheets/"+url.PathEscape(id)+"?"+q.Encode(), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

type valueRange struct {
	Range  string          `json:"range"`
	Values [][]interface{} `json:"values"`
}

func (t *Tool) getValues(ctx context.Context, cfg *Config, id, a1 string) (*valueRange, error) {
	var vr valueRange
	q := url.Values{"valueRenderOption": {"FORMATTED_VALUE"}, "majorDimension": {"ROWS"}}
	if err := t.getJSON(ctx, cfg, t.sheetsURL+"/spreadsheets/"+url.PathEscape(id)+"/values/"+url.PathEscape(a1)+"?"+q.Encode(), &vr); err != nil {
		return nil, err
	}
	return &vr, nil
}

func cellString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// sheetAsCSV renders every tab as CSV until the byte budget is used.
func (t *Tool) sheetAsCSV(ctx context.Context, cfg *Config, id string, budget int) (string, bool, error) {
	meta, err := t.spreadsheetMeta(ctx, cfg, id)
	if err != nil {
		return "", false, err
	}
	var b strings.Builder
	for _, s := range meta.Sheets {
		if b.Len() >= budget {
			return b.String(), true, nil
		}
		title := s.Properties.Title
		gridRows := s.Properties.GridProperties.RowCount
		fmt.Fprintf(&b, "## tab: %s (%d rows x %d columns)\n", title, gridRows, s.Properties.GridProperties.ColumnCount)
		w := csv.NewWriter(&b)
		// Read the tab in blocks of csvChunkRows, so a big tab never makes
		// one response larger than the 10 MB cap and reading stops at the
		// byte budget.
		for start, n := 1, 0; n < csvMaxChunks && (gridRows == 0 || start <= gridRows); start, n = start+csvChunkRows, n+1 {
			vr, err := t.getValues(ctx, cfg, id, fmt.Sprintf("%s!%d:%d", quoteSheet(title), start, start+csvChunkRows-1))
			if err != nil {
				return "", false, err
			}
			for _, row := range vr.Values {
				rec := make([]string, len(row))
				for i, c := range row {
					rec[i] = cellString(c)
				}
				_ = w.Write(rec)
				if b.Len() >= budget {
					w.Flush()
					return b.String(), true, nil
				}
			}
			if gridRows == 0 && len(vr.Values) < csvChunkRows {
				break // size unknown: a short block is the end of the data
			}
			if n == csvMaxChunks-1 && start+csvChunkRows <= gridRows {
				w.Flush()
				fmt.Fprintf(&b, "[TAB CUT after %d rows; use sheets_get_values for the rest]\n", start+csvChunkRows-1)
			}
		}
		w.Flush()
		b.WriteString("\n")
	}
	return b.String(), false, nil
}

// SheetsGetValues lists the tabs (no range) or returns the values of a range.
func (t *Tool) SheetsGetValues(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	id, err := fileArg(args, "spreadsheet")
	if err != nil {
		return "", err
	}
	if stringArg(args, "range") == "" {
		meta, err := t.spreadsheetMeta(ctx, cfg, id)
		if err != nil {
			return "", err
		}
		tabs := make([]map[string]interface{}, 0, len(meta.Sheets))
		for _, s := range meta.Sheets {
			p := s.Properties
			tabs = append(tabs, map[string]interface{}{"title": p.Title, "sheet_id": p.SheetID, "rows": p.GridProperties.RowCount, "columns": p.GridProperties.ColumnCount})
		}
		return marshal(map[string]interface{}{"spreadsheet_id": id, "title": meta.Properties.Title, "tabs": tabs,
			"hint": "Pass range (a tab name, or tab!A1:D50) to read values."})
	}
	a1, err := NormalizeRange(stringArg(args, "range"))
	if err != nil {
		return "", err
	}
	maxRows := intArg(args, "max_rows", defaultMaxRows, 1, maxMaxRows)
	// Ask for one row more than max_rows, to know if there are more rows.
	vr, err := t.getValues(ctx, cfg, id, boundRows(a1, maxRows+1))
	if err != nil {
		return "", err
	}
	res := map[string]interface{}{"spreadsheet_id": id, "range": vr.Range, "row_count": len(vr.Values)}
	values := vr.Values
	if len(values) > maxRows {
		values = values[:maxRows]
		res["truncated"] = true
		res["hint"] = fmt.Sprintf("Only the first %d rows are shown; read a narrower range or raise max_rows (max %d).", maxRows, maxMaxRows)
	}
	if values == nil {
		values = [][]interface{}{}
	}
	res["values"] = values
	return marshal(res)
}

// valuesArg reads a 2-D array of cells.
func valuesArg(args map[string]interface{}, key string) ([][]interface{}, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, fmt.Errorf("%s is required: an array of rows, each an array of cell values%s", key, validation.SuggestParam(key, args))
	}
	if s, ok := raw.(string); ok {
		// Some models send the array as a JSON string.
		var v interface{}
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return nil, fmt.Errorf("%s must be an array of rows, e.g. [[\"a\", 1], [\"b\", 2]]", key)
		}
		raw = v
	}
	rows, ok := raw.([]interface{})
	if !ok || len(rows) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty array of rows, e.g. [[\"a\", 1], [\"b\", 2]]", key)
	}
	if len(rows) > maxWriteRows {
		return nil, fmt.Errorf("%s has %d rows; the limit is %d per call", key, len(rows), maxWriteRows)
	}
	out := make([][]interface{}, 0, len(rows))
	cells := 0
	for i, r := range rows {
		row, ok := r.([]interface{})
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not an array; every row must be an array of cell values", key, i)
		}
		for j, c := range row {
			switch c.(type) {
			case nil, string, float64, bool:
			default:
				return nil, fmt.Errorf("%s[%d][%d] must be a string, number, boolean or null", key, i, j)
			}
		}
		cells += len(row)
		out = append(out, row)
	}
	if cells > maxWriteCells {
		return nil, fmt.Errorf("%s has %d cells; the limit is %d per call", key, cells, maxWriteCells)
	}
	return out, nil
}

func valueInputOption(args map[string]interface{}) (string, error) {
	switch v := strings.ToUpper(stringArg(args, "value_input")); v {
	case "", "USER_ENTERED":
		return "USER_ENTERED", nil
	case "RAW":
		return "RAW", nil
	}
	return "", fmt.Errorf("value_input must be USER_ENTERED (default: parsed like typed in the UI) or RAW")
}

// prepareWrite resolves the config with the write gate, the spreadsheet id,
// the range and the values.
func (t *Tool) prepareWrite(ctx context.Context, incidentID string, args map[string]interface{}, valuesKey string) (*Config, string, string, string, [][]interface{}, error) {
	logicalName := extractLogicalName(args)
	cfg, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return nil, "", "", "", nil, err
	}
	if cfg, err = t.verifyWriteGate(ctx, incidentID, logicalName, cfg); err != nil {
		return nil, "", "", "", nil, err
	}
	id, err := fileArg(args, "spreadsheet")
	if err != nil {
		return nil, "", "", "", nil, err
	}
	a1, err := NormalizeRange(stringArg(args, "range"))
	if err != nil {
		return nil, "", "", "", nil, err
	}
	opt, err := valueInputOption(args)
	if err != nil {
		return nil, "", "", "", nil, err
	}
	values, err := valuesArg(args, valuesKey)
	if err != nil {
		return nil, "", "", "", nil, err
	}
	return cfg, id, a1, opt, values, nil
}

// SheetsAppendRows appends rows after the last row with data of a table.
func (t *Tool) SheetsAppendRows(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, id, a1, opt, rows, err := t.prepareWrite(ctx, incidentID, args, "rows")
	if err != nil {
		return "", err
	}
	q := url.Values{"valueInputOption": {opt}, "insertDataOption": {"INSERT_ROWS"}}
	full := t.sheetsURL + "/spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(a1) + ":append?" + q.Encode()
	data, _, err := t.do(ctx, cfg, true, http.MethodPost, full, map[string]interface{}{"majorDimension": "ROWS", "values": rows}, maxJSONResponseBytes)
	if err != nil {
		return "", err
	}
	var out struct {
		Updates struct {
			UpdatedRange string `json:"updatedRange"`
			UpdatedRows  int    `json:"updatedRows"`
			UpdatedCells int    `json:"updatedCells"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode Google response: %w", err)
	}
	return marshal(map[string]interface{}{"spreadsheet_id": id, "updated_range": out.Updates.UpdatedRange,
		"updated_rows": out.Updates.UpdatedRows, "updated_cells": out.Updates.UpdatedCells})
}

// SheetsUpdateValues overwrites the cells of a range.
func (t *Tool) SheetsUpdateValues(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, id, a1, opt, values, err := t.prepareWrite(ctx, incidentID, args, "values")
	if err != nil {
		return "", err
	}
	q := url.Values{"valueInputOption": {opt}}
	full := t.sheetsURL + "/spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(a1) + "?" + q.Encode()
	data, _, err := t.do(ctx, cfg, true, http.MethodPut, full, map[string]interface{}{"majorDimension": "ROWS", "values": values}, maxJSONResponseBytes)
	if err != nil {
		return "", err
	}
	var out struct {
		UpdatedRange string `json:"updatedRange"`
		UpdatedRows  int    `json:"updatedRows"`
		UpdatedCells int    `json:"updatedCells"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode Google response: %w", err)
	}
	return marshal(map[string]interface{}{"spreadsheet_id": id, "updated_range": out.UpdatedRange,
		"updated_rows": out.UpdatedRows, "updated_cells": out.UpdatedCells})
}
