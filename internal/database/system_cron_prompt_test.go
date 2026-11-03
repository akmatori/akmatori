package database

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func withSeedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&CronJob{}, &Integration{}, &Channel{}); err != nil {
		t.Fatal(err)
	}
	prev := DB
	DB = db
	t.Cleanup(func() { DB = prev })
	return db
}

func TestSeedSystemCrons_ReplaceUneditedLegacyPrompt(t *testing.T) {
	db := withSeedDB(t)
	legacyDreaming := legacySystemCronPrompts[dreamingCronName][0]
	legacyEval := legacySystemCronPrompts[improvementEvaluatorCronName][0]
	db.Create(&CronJob{UUID: "d", Name: dreamingCronName, Schedule: "0 2 * * *", Prompt: legacyDreaming, IsSystem: true})
	db.Create(&CronJob{UUID: "e", Name: improvementEvaluatorCronName, Schedule: "0 5 * * *", Prompt: legacyEval, IsSystem: true})

	if err := SeedSystemCronJobs(); err != nil {
		t.Fatal(err)
	}
	if err := SeedImprovementEvaluatorCron(); err != nil {
		t.Fatal(err)
	}
	var d, e CronJob
	db.Where("name = ?", dreamingCronName).First(&d)
	db.Where("name = ?", improvementEvaluatorCronName).First(&e)
	if d.Prompt != dreamingCronPrompt {
		t.Error("unedited legacy Dreaming prompt was not replaced")
	}
	if e.Prompt != improvementEvaluatorCronPrompt {
		t.Error("unedited legacy evaluator prompt was not replaced")
	}
}

func TestSeedSystemCrons_KeepEditedPrompt(t *testing.T) {
	db := withSeedDB(t)
	const custom = "my own consolidation steps, still with memory-searcher"
	db.Create(&CronJob{UUID: "d", Name: dreamingCronName, Schedule: "0 2 * * *", Prompt: custom, IsSystem: true})
	if err := SeedSystemCronJobs(); err != nil {
		t.Fatal(err)
	}
	var d CronJob
	db.Where("name = ?", dreamingCronName).First(&d)
	if d.Prompt != custom {
		t.Errorf("operator prompt replaced: %q", d.Prompt)
	}
}
