package knowledge

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type corpusFile struct {
	Runbooks []struct {
		ID      uint   `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"runbooks"`
	Memories []struct {
		ID          uint   `json:"id"`
		Scope       string `json:"scope"`
		Type        string `json:"type"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
	} `json:"memories"`
}

func loadCorpus(t *testing.T) corpusFile {
	t.Helper()
	b, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var c corpusFile
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func corpusDocs(t *testing.T) (runbooks, memories []document) {
	c := loadCorpus(t)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, r := range c.Runbooks {
		runbooks = append(runbooks, document{ID: r.ID, Title: r.Title, Body: r.Content, UpdatedAt: base})
	}
	for _, m := range c.Memories {
		memories = append(memories, document{ID: m.ID, Title: memoryTitle(m.Name, m.Description), Body: m.Body, UpdatedAt: base})
	}
	return
}

func ids(hits []scored) []uint {
	out := []uint{}
	for _, h := range hits {
		out = append(out, h.Doc.ID)
	}
	return out
}

func TestTokenize(t *testing.T) {
	got := tokenize("502 on nginx-edge-03 (10.0.0.1): see /var/lib for The logs, a b")
	has := map[string]bool{}
	for _, tok := range got {
		has[tok] = true
	}
	for _, w := range []string{"502", "nginx-edge-03", "nginx", "edge", "03", "10.0.0.1", "var/lib", "var", "lib", "logs", "see"} {
		if !has[w] {
			t.Errorf("missing token %q in %v", w, got)
		}
	}
	for _, w := range []string{"the", "a", "b", "on", "for", "(10.0.0.1):"} {
		if has[w] {
			t.Errorf("unexpected token %q in %v", w, got)
		}
	}
}

func TestSearchPinnedQueries(t *testing.T) {
	runbooks, memories := corpusDocs(t)
	cases := []struct {
		name   string
		docs   []document
		query  string
		top    []uint // expected leading ids, in order
		inTop3 []uint // expected somewhere in the top 3
	}{
		{name: "host alone", docs: memories, query: "nginx-edge-03", top: []uint{1}},
		{name: "error string alone (runbook)", docs: runbooks, query: "502 Bad Gateway", top: []uint{1}},
		{name: "error string alone (memory)", docs: memories, query: "pm.max_children", top: []uint{13}},
		{name: "replica lag runbook", docs: runbooks, query: "replication lag db-replica-02", top: []uint{2}},
		{name: "replica lag memory", docs: memories, query: "replication lag on db-replica-02", top: []uint{3}},
		{name: "full alert payload", docs: runbooks, query: `{"alertname":"NginxHigh5xx","host":"nginx-edge-03","summary":"502 Bad Gateway rate above 5% for 10m","labels":{"severity":"critical","team":"sre"}}`, top: []uint{1}},
		{name: "full alert payload memories", docs: memories, query: `Alert: HighCPU on app-04 at 10:02 UTC, cpu usage 100% for 5 minutes, team=web`, inTop3: []uint{10}},
		{name: "operator feedback topic", docs: memories, query: "restart database primary", top: []uint{2}},
		{name: "timestamps feedback", docs: memories, query: "which timezone for timestamps in reports", top: []uint{7}},
		{name: "no match", docs: runbooks, query: "zzqx unrelatedword", top: nil},
		{name: "body only match", docs: memories, query: "NETDEV WATCHDOG", top: []uint{1}},
		{name: "kubernetes oom", docs: runbooks, query: "pod OOMKilled CrashLoopBackOff", top: []uint{4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hits := search(c.docs, c.query, 3)
			got := ids(hits)
			if c.top == nil && c.inTop3 == nil {
				if len(got) != 0 {
					t.Fatalf("want no hits, got %v", got)
				}
				return
			}
			if len(c.top) > 0 && !reflect.DeepEqual(got[:min(len(got), len(c.top))], c.top) {
				t.Errorf("top = %v, want prefix %v", got, c.top)
			}
			for _, id := range c.inTop3 {
				found := false
				for _, g := range got {
					found = found || g == id
				}
				if !found {
					t.Errorf("id %d not in top 3 %v", id, got)
				}
			}
		})
	}
}

func TestSearchTiesNewestFirst(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	docs := []document{
		{ID: 1, Title: "disk full", Body: "x", UpdatedAt: old},
		{ID: 2, Title: "disk full", Body: "x", UpdatedAt: old.Add(time.Hour)},
		{ID: 3, Title: "disk full", Body: "x", UpdatedAt: old},
	}
	if got := ids(search(docs, "disk", 10)); !reflect.DeepEqual(got, []uint{2, 1, 3}) {
		t.Errorf("tie order = %v, want [2 1 3]", got)
	}
}

func TestQueryTermsCap(t *testing.T) {
	df := map[string]int{}
	var words []string
	for i := 0; i < 60; i++ {
		w := "w" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('a'+i/26))
		df[w] = i + 1
		words = append(words, w)
	}
	terms := queryTerms(strings.Join(words, " ")+" notincorpus", df)
	if len(terms) != maxQueryTerms {
		t.Fatalf("len = %d", len(terms))
	}
	for _, term := range terms {
		if df[term] > maxQueryTerms {
			t.Errorf("kept a frequent term %q (df %d)", term, df[term])
		}
	}
}

func TestExcerpt(t *testing.T) {
	short := "short body"
	if excerpt(short, []string{"body"}) != short {
		t.Error("short body must be returned whole")
	}
	long := strings.Repeat("filler line of text\n", 60) + "the needle is here\n" + strings.Repeat("more filler text\n", 60)
	ex := excerpt(long, []string{"needle"})
	if !strings.Contains(ex, "needle") || !strings.HasPrefix(ex, "…") || !strings.HasSuffix(ex, "…") {
		t.Errorf("excerpt = %q", ex)
	}
	if n := len([]rune(ex)); n > 2*excerptRadius+130 {
		t.Errorf("excerpt too long: %d runes", n)
	}
}
