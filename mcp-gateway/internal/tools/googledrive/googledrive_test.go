package googledrive

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const testIncident = "test-incident"

var testKey *rsa.PrivateKey

func init() {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	testKey = k
}

func saJSON(t *testing.T) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(testKey)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{
		"type":           "service_account",
		"client_email":   "bot@proj.iam.gserviceaccount.com",
		"private_key_id": "kid1",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":      "https://evil.example.com/token",
	})
	return string(b)
}

type fakeGoogle struct {
	tokenCalls atomic.Int32
	scopes     []string
	subjects   []string
	seen       []string
	mux        *http.ServeMux
}

// verifyAssertion checks the RS256 signature and returns the claims.
func verifyAssertion(t *testing.T, a string) map[string]interface{} {
	t.Helper()
	parts := strings.Split(a, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&testKey.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("bad signature: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]interface{}
	_ = json.Unmarshal(raw, &c)
	return c
}

func newTestTool(t *testing.T, cfg Config) (*Tool, *fakeGoogle) {
	t.Helper()
	fg := &fakeGoogle{mux: http.NewServeMux()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			fg.tokenCalls.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				http.Error(w, `{"error":"unsupported_grant_type"}`, 400)
				return
			}
			c := verifyAssertion(t, r.Form.Get("assertion"))
			if c["iss"] != "bot@proj.iam.gserviceaccount.com" || c["aud"] != defaultTokenURL {
				t.Errorf("bad claims: %v", c)
			}
			sc, _ := c["scope"].(string)
			sub, _ := c["sub"].(string)
			fg.scopes = append(fg.scopes, sc)
			fg.subjects = append(fg.subjects, sub)
			tok := "read-token"
			if sc == scopeWrite {
				tok = "write-token"
			}
			writeJSON(w, map[string]interface{}{"access_token": tok, "expires_in": 3600})
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer read-token" && auth != "Bearer write-token" {
			http.Error(w, `{"error":{"message":"no auth"}}`, 401)
			return
		}
		if r.Method != http.MethodGet && auth != "Bearer write-token" {
			http.Error(w, `{"error":{"message":"read-only token used for a write"}}`, 403)
			return
		}
		fg.seen = append(fg.seen, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		fg.mux.ServeHTTP(w, r)
	}))
	tool := NewTool(log.New(io.Discard, "", 0), nil)
	tool.driveURL = srv.URL + "/drive/v3"
	tool.sheetsURL = srv.URL + "/v4"
	tool.tokens.url = srv.URL + "/token"
	tool.transport = srv.Client().Transport
	if cfg.ServiceAccountJSON == "" {
		cfg.ServiceAccountJSON = saJSON(t)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5
	}
	c := cfg
	tool.configCache.Set(configCacheKey(testIncident, ""), &c)
	t.Cleanup(func() {
		tool.Stop()
		srv.Close()
	})
	return tool, fg
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func decode(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, s)
	}
	return m
}

const sheetID = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd"

func TestParseFileID(t *testing.T) {
	cases := map[string]string{
		sheetID: sheetID,
		"https://docs.google.com/spreadsheets/d/" + sheetID + "/edit?gid=0#gid=0": sheetID,
		"https://drive.google.com/file/d/abcdefghijKLMN/view":                     "abcdefghijKLMN",
		"https://drive.google.com/open?id=abcdefghijKLMN":                         "abcdefghijKLMN",
		"https://docs.google.com/document/u/1/d/abcdefghijKLMN/edit":              "abcdefghijKLMN",
		"https://drive.google.com/drive/folders/abcdefghijKLMN":                   "abcdefghijKLMN",
		"https://drive.google.com/drive/u/0/folders/abcdefghijKLMN?usp=sharing":   "abcdefghijKLMN",
	}
	for in, want := range cases {
		got, err := ParseFileID(in)
		if err != nil || got != want {
			t.Errorf("ParseFileID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "short", "https://evil.example.com/d/abcdefghijKLMN", "../../etc/passwd"} {
		if _, err := ParseFileID(bad); err == nil {
			t.Errorf("ParseFileID(%q) accepted", bad)
		}
	}
}

func TestNormalizeRange(t *testing.T) {
	cases := map[string]string{
		"Sheet1":                 "'Sheet1'",
		"Daily checks 2026":      "'Daily checks 2026'",
		"Daily checks 2026!A1:D": "'Daily checks 2026'!A1:D",
		"'Q1'!A:F":               "'Q1'!A:F",
		"A1:D10":                 "A1:D10",
		"Bob's tab":              "'Bob''s tab'",
		"Agencies":               "'Agencies'",
		"A:F":                    "A:F",
		"$A:$F":                  "$A:$F",
		"'A:F'":                  "'A:F'",
	}
	for in, want := range cases {
		got, err := NormalizeRange(in)
		if err != nil || got != want {
			t.Errorf("NormalizeRange(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeRange(""); err == nil {
		t.Error("empty range accepted")
	}
}

func TestBoundRows(t *testing.T) {
	cases := map[string]string{
		"'Results'":       "'Results'!1:201",
		"A:F":             "A1:F201",
		"'Results'!A:F":   "'Results'!A1:F201",
		"'Results'!A5:F":  "'Results'!A5:F205",
		"'Results'!A1:F9": "'Results'!A1:F9",
		"A1:D10":          "A1:D10",
		"'Results'!1:50":  "'Results'!1:50",
	}
	for in, want := range cases {
		if got := boundRows(in, 201); got != want {
			t.Errorf("boundRows(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestParseServiceAccountErrors(t *testing.T) {
	for _, raw := range []string{"", "not json", `{"type":"authorized_user"}`, `{"type":"service_account","client_email":"a@b"}`,
		`{"type":"service_account","client_email":"a@b","private_key":"garbage"}`} {
		_, err := parseServiceAccount(raw)
		if err == nil {
			t.Errorf("accepted %q", raw)
		} else if strings.Contains(err.Error(), "garbage") {
			t.Errorf("error leaks key material: %v", err)
		}
	}
	sa, err := parseServiceAccount(saJSON(t))
	if err != nil || sa.key == nil || sa.PrivateKey != "" {
		t.Fatalf("valid key: %v", err)
	}
}

func TestSearchFiles(t *testing.T) {
	tool, fg := newTestTool(t, Config{})
	fg.mux.HandleFunc("/drive/v3/files", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		want := `trashed = false and (name contains 'Bob\'s' or fullText contains 'Bob\'s') and mimeType = 'application/vnd.google-apps.spreadsheet'`
		if q != want {
			t.Errorf("q = %s\nwant %s", q, want)
		}
		if r.URL.Query().Get("supportsAllDrives") != "true" {
			t.Error("supportsAllDrives missing")
		}
		writeJSON(w, map[string]interface{}{"nextPageToken": "p2", "files": []map[string]interface{}{
			{"id": sheetID, "name": "Checks", "mimeType": mimeSheet, "webViewLink": "https://docs.google.com/x", "owners": []map[string]string{{"emailAddress": "op@example.com"}}},
		}})
	})
	out, err := tool.SearchFiles(context.Background(), testIncident, map[string]interface{}{"query": "Bob's", "type": "spreadsheet"})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	files := m["files"].([]interface{})
	f := files[0].(map[string]interface{})
	if f["type"] != "spreadsheet" || f["owner"] != "op@example.com" || m["next_page_token"] != "p2" {
		t.Errorf("unexpected output: %s", out)
	}
	if _, err := tool.SearchFiles(context.Background(), testIncident, map[string]interface{}{"type": "video"}); err == nil {
		t.Error("bad type accepted")
	}
}

func TestTokenCachedAndScoped(t *testing.T) {
	tool, fg := newTestTool(t, Config{ImpersonateUser: "ops@example.com", AllowWrites: true})
	fg.mux.HandleFunc("/drive/v3/files", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"files": []interface{}{}})
	})
	fg.mux.HandleFunc("/v4/spreadsheets/"+sheetID+"/values/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"updates": map[string]interface{}{"updatedRange": "'T'!A5:B5", "updatedRows": 1, "updatedCells": 2}})
	})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := tool.SearchFiles(ctx, testIncident, map[string]interface{}{}); err != nil {
			t.Fatal(err)
		}
	}
	if n := fg.tokenCalls.Load(); n != 1 {
		t.Fatalf("token calls = %d, want 1 (cached)", n)
	}
	if _, err := tool.SheetsAppendRows(ctx, testIncident, map[string]interface{}{"spreadsheet": sheetID, "range": "T", "rows": []interface{}{[]interface{}{"a", 1.0}}}); err != nil {
		t.Fatal(err)
	}
	if n := fg.tokenCalls.Load(); n != 2 {
		t.Fatalf("token calls = %d, want 2 (separate write scope)", n)
	}
	if fg.scopes[0] != scopeRead || fg.scopes[1] != scopeWrite {
		t.Errorf("scopes = %v", fg.scopes)
	}
	if fg.subjects[0] != "ops@example.com" {
		t.Errorf("sub = %v", fg.subjects)
	}
}

func TestTokenErrorIsClear(t *testing.T) {
	tool, _ := newTestTool(t, Config{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		writeJSON(w, map[string]string{"error": "invalid_grant", "error_description": "Invalid JWT Signature."})
	}))
	defer srv.Close()
	tool.tokens.url = srv.URL
	_, err := tool.SearchFiles(context.Background(), testIncident, map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") || !strings.Contains(err.Error(), "deleted or disabled") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadFileDoc(t *testing.T) {
	tool, fg := newTestTool(t, Config{})
	fg.mux.HandleFunc("/drive/v3/files/docid12345", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"id": "docid12345", "name": "Runbook", "mimeType": mimeDoc})
	})
	fg.mux.HandleFunc("/drive/v3/files/docid12345/export", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mimeType") != "text/plain" {
			t.Errorf("export mime = %s", r.URL.Query().Get("mimeType"))
		}
		_, _ = io.WriteString(w, "\ufeffStep 1. "+strings.Repeat("x", 3000))
	})
	out, err := tool.ReadFile(context.Background(), testIncident, map[string]interface{}{"file": "https://docs.google.com/document/d/docid12345/edit", "max_bytes": 1000.0})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	c := m["content"].(string)
	if !strings.HasPrefix(c, "Step 1.") || !strings.Contains(c, "[TRUNCATED at 1000 bytes") || m["truncated"] != true {
		t.Errorf("content = %.80q…, truncated=%v", c, m["truncated"])
	}
}

func TestReadFileSheetAsCSV(t *testing.T) {
	tool, fg := newTestTool(t, Config{})
	fg.mux.HandleFunc("/drive/v3/files/"+sheetID, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"id": sheetID, "name": "Checks", "mimeType": mimeSheet})
	})
	fg.mux.HandleFunc("/v4/spreadsheets/"+sheetID, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"properties": map[string]string{"title": "Checks"}, "sheets": []interface{}{
			map[string]interface{}{"properties": map[string]interface{}{"sheetId": 0, "title": "Agencies", "gridProperties": map[string]int{"rowCount": 1500, "columnCount": 3}}},
			map[string]interface{}{"properties": map[string]interface{}{"sheetId": 1, "title": "Q 2"}},
		}})
	})
	fg.mux.HandleFunc("/v4/spreadsheets/"+sheetID+"/values/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/spreadsheets/" + sheetID + "/values/'Agencies'!1:1000":
			writeJSON(w, map[string]interface{}{"range": "Agencies!A1:C2", "values": [][]interface{}{{"id", "name", "deficit"}, {"1234", "Acme, Inc", "-28"}}})
		case "/v4/spreadsheets/" + sheetID + "/values/'Agencies'!1001:2000":
			writeJSON(w, map[string]interface{}{"range": "Agencies!A1001:C1001", "values": [][]interface{}{{"5678", "Beta", "3"}}})
		case "/v4/spreadsheets/" + sheetID + "/values/'Q 2'!1:1000":
			writeJSON(w, map[string]interface{}{"range": "'Q 2'!A1", "values": [][]interface{}{{"x"}}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	out, err := tool.ReadFile(context.Background(), testIncident, map[string]interface{}{"file": sheetID})
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, out)["content"].(string)
	for _, want := range []string{"## tab: Agencies (1500 rows x 3 columns)", `1234,"Acme, Inc",-28`, "5678,Beta,3", "## tab: Q 2"} {
		if !strings.Contains(c, want) {
			t.Errorf("missing %q in:\n%s", want, c)
		}
	}
}

func TestReadFileBinaryRefused(t *testing.T) {
	tool, fg := newTestTool(t, Config{})
	fg.mux.HandleFunc("/drive/v3/files/imgid12345", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"id": "imgid12345", "name": "a.png", "mimeType": "image/png"})
	})
	if _, err := tool.ReadFile(context.Background(), testIncident, map[string]interface{}{"file": "imgid12345"}); err == nil || !strings.Contains(err.Error(), "image/png") {
		t.Fatalf("err = %v", err)
	}
}

func TestSheetsGetValues(t *testing.T) {
	tool, fg := newTestTool(t, Config{})
	fg.mux.HandleFunc("/v4/spreadsheets/"+sheetID, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"properties": map[string]string{"title": "F"}, "sheets": []interface{}{
			map[string]interface{}{"properties": map[string]interface{}{"sheetId": 7, "title": "Agencies", "gridProperties": map[string]int{"rowCount": 1000, "columnCount": 26}}},
		}})
	})
	var gotPath string
	fg.mux.HandleFunc("/v4/spreadsheets/"+sheetID+"/values/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeJSON(w, map[string]interface{}{"range": "Agencies!A1:B3", "values": [][]interface{}{{"a"}, {"b"}, {"c"}}})
	})
	ctx := context.Background()
	out, err := tool.SheetsGetValues(ctx, testIncident, map[string]interface{}{"spreadsheet": "https://docs.google.com/spreadsheets/d/" + sheetID + "/edit"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"title": "Agencies"`) || !strings.Contains(out, `"rows": 1000`) {
		t.Errorf("tabs: %s", out)
	}
	out, err = tool.SheetsGetValues(ctx, testIncident, map[string]interface{}{"spreadsheet": sheetID, "range": "Agencies!A1:B3", "max_rows": 2.0})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if len(m["values"].([]interface{})) != 2 || m["truncated"] != true || m["row_count"] != 3.0 {
		t.Errorf("values: %s", out)
	}
	// A whole tab is asked for in bounded form: max_rows + 1 rows.
	if _, err := tool.SheetsGetValues(ctx, testIncident, map[string]interface{}{"spreadsheet": sheetID, "range": "Agencies", "max_rows": 2.0}); err != nil {
		t.Fatal(err)
	}
	if want := "/v4/spreadsheets/" + sheetID + "/values/'Agencies'!1:3"; gotPath != want {
		t.Errorf("path = %s; want %s", gotPath, want)
	}
}

func TestWritesRefusedWhenDisabled(t *testing.T) {
	tool, fg := newTestTool(t, Config{})
	args := map[string]interface{}{"spreadsheet": sheetID, "range": "T", "rows": []interface{}{[]interface{}{"a"}}}
	if _, err := tool.SheetsAppendRows(context.Background(), testIncident, args); err == nil || !strings.Contains(err.Error(), "gdrive_allow_writes") {
		t.Fatalf("append err = %v", err)
	}
	args["values"] = args["rows"]
	if _, err := tool.SheetsUpdateValues(context.Background(), testIncident, args); err == nil || !strings.Contains(err.Error(), "gdrive_allow_writes") {
		t.Fatalf("update err = %v", err)
	}
	if len(fg.seen) != 0 || fg.tokenCalls.Load() != 0 {
		t.Errorf("a refused write reached Google: %v", fg.seen)
	}
}

func TestSheetsAppendRows(t *testing.T) {
	tool, fg := newTestTool(t, Config{AllowWrites: true})
	var body map[string]interface{}
	fg.mux.HandleFunc("/v4/spreadsheets/"+sheetID+"/values/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.EscapedPath(), ":append") {
			t.Errorf("got %s %s", r.Method, r.URL.EscapedPath())
		}
		if r.URL.Query().Get("valueInputOption") != "USER_ENTERED" || r.URL.Query().Get("insertDataOption") != "INSERT_ROWS" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, map[string]interface{}{"updates": map[string]interface{}{"updatedRange": "'Agencies 2026'!A5:C6", "updatedRows": 2, "updatedCells": 6}})
	})
	// rows as a JSON string, like some models send it
	out, err := tool.SheetsAppendRows(context.Background(), testIncident, map[string]interface{}{
		"spreadsheet": sheetID, "range": "Agencies 2026", "rows": `[["2026-11-25", 1234, "Acme"], ["2026-11-25", 99, null]]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decode(t, out)["updated_rows"] != 2.0 {
		t.Errorf("out = %s", out)
	}
	if !strings.Contains(fg.seen[0], "/values/%27Agencies%202026%27:append") {
		t.Errorf("path = %s", fg.seen[0])
	}
	if rows := body["values"].([]interface{}); len(rows) != 2 {
		t.Errorf("body = %v", body)
	}
}

func TestValuesArgValidation(t *testing.T) {
	bad := []interface{}{nil, "not json", []interface{}{}, []interface{}{"flat"}, []interface{}{[]interface{}{map[string]interface{}{"x": 1}}}}
	for _, v := range bad {
		if _, err := valuesArg(map[string]interface{}{"rows": v}, "rows"); err == nil {
			t.Errorf("accepted %#v", v)
		}
	}
	big := make([]interface{}, maxWriteRows+1)
	for i := range big {
		big[i] = []interface{}{"a"}
	}
	if _, err := valuesArg(map[string]interface{}{"rows": big}, "rows"); err == nil {
		t.Error("too many rows accepted")
	}
}

func TestAPIErrorMessages(t *testing.T) {
	tool, fg := newTestTool(t, Config{})
	fg.mux.HandleFunc("/drive/v3/files/missing12345", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		writeJSON(w, map[string]interface{}{"error": map[string]interface{}{"message": "File not found: missing12345."}})
	})
	_, err := tool.ReadFile(context.Background(), testIncident, map[string]interface{}{"file": "missing12345"})
	if err == nil || !strings.Contains(err.Error(), "not shared with the service account") || !strings.Contains(err.Error(), "File not found") {
		t.Fatalf("err = %v", err)
	}
}
