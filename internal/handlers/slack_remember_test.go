package handlers

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRememberCommand(t *testing.T) {
	cases := map[string]struct {
		text string
		want string
		ok   bool
	}{
		"slash":          {"/remember redis prod listens on 16379", "redis prod listens on 16379", true},
		"bang":           {"!remember   the data dir is /mnt/data ", "the data dir is /mnt/data", true},
		"colon":          {"Remember: never restart db-01 during backups", "never restart db-01 during backups", true},
		"colon no space": {"remember:x", "x", true},
		"with mention":   {"<@BOT> /remember check the NIC first", "check the NIC first", true},
		"multi line":     {"/remember first line\nsecond line", "first line\nsecond line", true},
		"empty body":     {"/remember   ", "", false},
		"prose":          {"I remember this host was flaky", "", false},
		"similar word":   {"/remembering stuff", "", false},
		"plain reply":    {"looks resolved now", "", false},
		"mention no cmd": {"<@BOT> what happened?", "", false},
	}
	for name, c := range cases {
		got, ok := rememberCommand(c.text, "BOT")
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", name, got, ok, c.want, c.ok)
		}
	}
}

// TestMaybeCaptureSlackFeedback_RememberCommand: a remember command is
// stored without the classifier (no LLM needed) and acknowledged with the
// reaction only on the non-mention path.
func TestMaybeCaptureSlackFeedback_RememberCommand(t *testing.T) {
	mock := newMockMemoryService()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Incident{}, &database.AgentRun{}); err != nil {
		t.Fatal(err)
	}
	prev := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prev })
	seedMentionThread(t, db, "inc-7", "TX", "Postgres outage", "agent answer")
	acker := &fakeFeedbackAcker{}
	h := &SlackHandler{memoryManager: mock, botUserID: "BOT", feedbackAcker: acker} // classifier nil

	h.maybeCaptureSlackFeedback("C", "TX", "M-1", "remember: the postgres data dir is /mnt/data", "U")

	mem := mock.lastUpsertedSnap()
	if mem == nil {
		t.Fatal("remember command not stored")
	}
	if mem.IncidentUUID != "inc-7" || mem.Type != "feedback" || mem.CreatedBy != "operator" {
		t.Errorf("memory: %+v", mem)
	}
	if !strings.Contains(mem.Description, "/mnt/data") || mem.Body != "the postgres data dir is /mnt/data" {
		t.Errorf("text: %+v", mem)
	}
	if !strings.Contains(mem.EvidenceNote, "asked to remember") {
		t.Errorf("note: %q", mem.EvidenceNote)
	}
	if acker.reactionCount() != 1 || acker.postCount() != 0 {
		t.Errorf("acks: reactions=%d texts=%d, want 1/0 on the non-mention path", acker.reactionCount(), acker.postCount())
	}

	// The mention path also posts the short text ack.
	h.captureRemember("C", "TX", "M-2", "second fact", true)
	if acker.reactionCount() != 2 || acker.postCount() != 1 {
		t.Errorf("mention acks: reactions=%d texts=%d, want 2/1", acker.reactionCount(), acker.postCount())
	}
	// A plain reply without the classifier stores nothing.
	h.maybeCaptureSlackFeedback("C", "TX", "M-3", "looks resolved now", "U")
	if last := mock.lastUpsertedSnap(); last == nil || last.Body != "second fact" {
		t.Errorf("a plain reply was stored without the classifier: %+v", last)
	}
}
