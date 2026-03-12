package services

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// RunbookService manages runbook CRUD and file synchronization
type RunbookService struct {
	db          *gorm.DB
	runbooksDir string
	syncMu      sync.Mutex // serializes SyncRunbookFiles calls
}

// NewRunbookService creates a new runbook service
func NewRunbookService(dataDir string) *RunbookService {
	dir := filepath.Join(dataDir, "runbooks")
	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("Warning: failed to create runbooks directory %s: %v", dir, err)
	}
	return &RunbookService{
		db:          database.GetDB(),
		runbooksDir: dir,
	}
}

// CreateRunbook creates a new runbook and syncs files
func (s *RunbookService) CreateRunbook(title, content string) (*database.Runbook, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("content cannot be empty")
	}

	runbook := &database.Runbook{
		Title:   strings.TrimSpace(title),
		Content: content,
	}
	if err := s.db.Create(runbook).Error; err != nil {
		return nil, fmt.Errorf("failed to create runbook: %w", err)
	}

	if err := s.SyncRunbookFiles(); err != nil {
		return nil, fmt.Errorf("runbook created but file sync failed: %w", err)
	}

	return runbook, nil
}

// UpdateRunbook updates an existing runbook and syncs files
func (s *RunbookService) UpdateRunbook(id uint, title, content string) (*database.Runbook, error) {
	var runbook database.Runbook
	if err := s.db.First(&runbook, id).Error; err != nil {
		return nil, fmt.Errorf("runbook not found")
	}

	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("content cannot be empty")
	}

	runbook.Title = strings.TrimSpace(title)
	runbook.Content = content
	if err := s.db.Save(&runbook).Error; err != nil {
		return nil, fmt.Errorf("failed to update runbook: %w", err)
	}

	if err := s.SyncRunbookFiles(); err != nil {
		return nil, fmt.Errorf("runbook updated but file sync failed: %w", err)
	}

	return &runbook, nil
}

// DeleteRunbook deletes a runbook and syncs files
func (s *RunbookService) DeleteRunbook(id uint) error {
	result := s.db.Delete(&database.Runbook{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete runbook: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("runbook not found")
	}

	if err := s.SyncRunbookFiles(); err != nil {
		return fmt.Errorf("runbook deleted but file sync failed: %w", err)
	}

	return nil
}

// GetRunbook retrieves a single runbook by ID
func (s *RunbookService) GetRunbook(id uint) (*database.Runbook, error) {
	var runbook database.Runbook
	if err := s.db.First(&runbook, id).Error; err != nil {
		return nil, fmt.Errorf("runbook not found")
	}
	return &runbook, nil
}

// ListRunbooks retrieves all runbooks ordered by title
func (s *RunbookService) ListRunbooks() ([]database.Runbook, error) {
	var runbooks []database.Runbook
	if err := s.db.Order("title asc").Find(&runbooks).Error; err != nil {
		return nil, fmt.Errorf("failed to list runbooks: %w", err)
	}
	return runbooks, nil
}

// slugify converts a title to a filename-safe slug
func slugify(title string) string {
	// Convert to lowercase
	s := strings.ToLower(title)
	// Replace non-alphanumeric characters with hyphens
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	// Trim leading/trailing hyphens
	s = strings.Trim(s, "-")
	// Limit length
	if len(s) > 100 {
		s = s[:100]
		// Don't end on a hyphen
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		s = "runbook"
	}
	return s
}

// SyncRunbookFiles writes all runbooks as markdown files and removes stale ones.
// It is serialized by a mutex to prevent concurrent syncs from racing.
func (s *RunbookService) SyncRunbookFiles() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	// Ensure directory exists
	if err := os.MkdirAll(s.runbooksDir, 0755); err != nil {
		return fmt.Errorf("failed to create runbooks directory: %w", err)
	}

	// Get all runbooks from DB
	var runbooks []database.Runbook
	if err := s.db.Find(&runbooks).Error; err != nil {
		return fmt.Errorf("failed to query runbooks: %w", err)
	}

	// Build set of expected filenames
	expectedFiles := make(map[string]bool)
	for _, rb := range runbooks {
		filename := fmt.Sprintf("%d-%s.md", rb.ID, slugify(rb.Title))
		expectedFiles[filename] = true

		// Write the file
		content := fmt.Sprintf("# %s\n\n%s\n", rb.Title, rb.Content)
		filePath := filepath.Join(s.runbooksDir, filename)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write runbook file %s: %w", filename, err)
		}
	}

	// Remove files that no longer correspond to any runbook
	entries, err := os.ReadDir(s.runbooksDir)
	if err != nil {
		return fmt.Errorf("failed to read runbooks directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !expectedFiles[entry.Name()] {
			filePath := filepath.Join(s.runbooksDir, entry.Name())
			if err := os.Remove(filePath); err != nil {
				log.Printf("Warning: failed to remove stale runbook file %s: %v", entry.Name(), err)
			}
		}
	}

	return nil
}
