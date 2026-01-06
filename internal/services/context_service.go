package services

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// Context file validation constants
const (
	MaxFileSize = 10 * 1024 * 1024 // 10 MB
)

// AllowedExtensions lists the allowed file extensions for context files
var AllowedExtensions = []string{
	".md", ".txt", ".json", ".yaml", ".yml",
	".xml", ".csv", ".log", ".conf", ".cfg", ".ini",
	".sh", ".py", ".pdf",
}

// FilenamePattern validates filename format: alphanumeric, dashes, underscores, and extension
var FilenamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*\.[a-zA-Z0-9]+$`)

// ReferencePattern matches [[filename]] patterns in text
var ReferencePattern = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// AssetLinkPattern matches [filename](assets/filename) patterns (already transformed references)
var AssetLinkPattern = regexp.MustCompile(`\[[^\]]+\]\(assets/([^)]+)\)`)

// ContextService manages context files
type ContextService struct {
	db         *gorm.DB
	contextDir string
}

// NewContextService creates a new context service
func NewContextService(dataDir string) (*ContextService, error) {
	contextDir := filepath.Join(dataDir, "context")

	// Ensure context directory exists
	if err := os.MkdirAll(contextDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create context directory: %w", err)
	}

	return &ContextService{
		db:         database.GetDB(),
		contextDir: contextDir,
	}, nil
}

// GetContextDir returns the context directory path
func (s *ContextService) GetContextDir() string {
	return s.contextDir
}

// ValidateFilename checks if filename format is valid
func (s *ContextService) ValidateFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("filename is required")
	}

	if len(filename) > 255 {
		return fmt.Errorf("filename too long (max 255 characters)")
	}

	if !FilenamePattern.MatchString(filename) {
		return fmt.Errorf("invalid filename format: use only letters, numbers, dashes, underscores, and a valid extension")
	}

	return nil
}

// ValidateFileType checks if file extension is allowed
func (s *ContextService) ValidateFileType(filename string) error {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return fmt.Errorf("filename must have an extension")
	}

	for _, allowed := range AllowedExtensions {
		if ext == allowed {
			return nil
		}
	}

	return fmt.Errorf("file type '%s' not allowed. Allowed: %s", ext, strings.Join(AllowedExtensions, ", "))
}

// FileExists checks if a file with the given filename already exists
func (s *ContextService) FileExists(filename string) bool {
	var count int64
	s.db.Model(&database.ContextFile{}).Where("filename = ?", filename).Count(&count)
	return count > 0
}

// SaveFile saves a file to storage and creates a database record
func (s *ContextService) SaveFile(filename, originalName, mimeType, description string, size int64, content io.Reader) (*database.ContextFile, error) {
	// Validate filename format
	if err := s.ValidateFilename(filename); err != nil {
		return nil, err
	}

	// Validate file type
	if err := s.ValidateFileType(filename); err != nil {
		return nil, err
	}

	// Check for duplicates
	if s.FileExists(filename) {
		return nil, fmt.Errorf("file '%s' already exists", filename)
	}

	// Validate file size
	if size > MaxFileSize {
		return nil, fmt.Errorf("file too large: %d bytes (max %d bytes)", size, MaxFileSize)
	}

	// Write file to disk
	filePath := filepath.Join(s.contextDir, filename)
	file, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	written, err := io.Copy(file, content)
	if err != nil {
		os.Remove(filePath) // Clean up on error
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	// Create database record
	contextFile := &database.ContextFile{
		Filename:     filename,
		OriginalName: originalName,
		MimeType:     mimeType,
		Size:         written,
		Description:  description,
	}

	if err := s.db.Create(contextFile).Error; err != nil {
		os.Remove(filePath) // Clean up on error
		return nil, fmt.Errorf("failed to create database record: %w", err)
	}

	return contextFile, nil
}

// ListFiles returns all context files
func (s *ContextService) ListFiles() ([]database.ContextFile, error) {
	var files []database.ContextFile
	if err := s.db.Order("filename ASC").Find(&files).Error; err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}
	return files, nil
}

// GetFile returns a file by ID
func (s *ContextService) GetFile(id uint) (*database.ContextFile, error) {
	var file database.ContextFile
	if err := s.db.First(&file, id).Error; err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}
	return &file, nil
}

// GetFileByName returns a file by filename
func (s *ContextService) GetFileByName(filename string) (*database.ContextFile, error) {
	var file database.ContextFile
	if err := s.db.Where("filename = ?", filename).First(&file).Error; err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}
	return &file, nil
}

// DeleteFile removes a file from storage and database
func (s *ContextService) DeleteFile(id uint) error {
	// Get file record
	file, err := s.GetFile(id)
	if err != nil {
		return err
	}

	// Delete from filesystem
	filePath := filepath.Join(s.contextDir, file.Filename)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file from disk: %w", err)
	}

	// Delete from database
	if err := s.db.Delete(&database.ContextFile{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete database record: %w", err)
	}

	return nil
}

// GetFilePath returns the full filesystem path for a file
func (s *ContextService) GetFilePath(filename string) string {
	return filepath.Join(s.contextDir, filename)
}

// ParseReferences extracts [[filename]] patterns and [filename](assets/filename) patterns from text
func (s *ContextService) ParseReferences(text string) []string {
	// Use map to deduplicate
	seen := make(map[string]bool)
	var references []string

	// Match [[filename]] patterns
	matches := ReferencePattern.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) > 1 {
			filename := strings.TrimSpace(match[1])
			if filename != "" && !seen[filename] {
				seen[filename] = true
				references = append(references, filename)
			}
		}
	}

	// Also match [filename](assets/filename) patterns (already transformed references)
	assetMatches := AssetLinkPattern.FindAllStringSubmatch(text, -1)
	for _, match := range assetMatches {
		if len(match) > 1 {
			filename := strings.TrimSpace(match[1])
			if filename != "" && !seen[filename] {
				seen[filename] = true
				references = append(references, filename)
			}
		}
	}

	return references
}

// ValidateReferences checks if all referenced files exist
func (s *ContextService) ValidateReferences(text string) (valid bool, missing []string, found []string) {
	references := s.ParseReferences(text)

	for _, ref := range references {
		if s.FileExists(ref) {
			found = append(found, ref)
		} else {
			missing = append(missing, ref)
		}
	}

	valid = len(missing) == 0
	return valid, missing, found
}

// ResolveReferences replaces [[filename]] with ./context/filename
func (s *ContextService) ResolveReferences(text string) string {
	return ReferencePattern.ReplaceAllString(text, "./context/$1")
}

// ResolveReferencesToMarkdownLinks replaces [[filename]] with [filename](assets/filename)
func (s *ContextService) ResolveReferencesToMarkdownLinks(text string) string {
	return ReferencePattern.ReplaceAllString(text, "[$1](assets/$1)")
}

// CopyReferencedFilesToDir creates symlinks for referenced files in the target directory
func (s *ContextService) CopyReferencedFilesToDir(text string, targetDir string) error {
	references := s.ParseReferences(text)

	if len(references) == 0 {
		return nil
	}

	// Create context directory in target
	contextDir := filepath.Join(targetDir, "context")
	if err := os.MkdirAll(contextDir, 0755); err != nil {
		return fmt.Errorf("failed to create context directory: %w", err)
	}

	// Create symlinks for each referenced file
	for _, filename := range references {
		srcPath := s.GetFilePath(filename)
		dstPath := filepath.Join(contextDir, filename)

		// Check if source file exists
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			// Skip missing files (they should have been validated before)
			continue
		}

		// Create symlink
		if err := os.Symlink(srcPath, dstPath); err != nil {
			// Ignore if symlink already exists
			if !os.IsExist(err) {
				return fmt.Errorf("failed to create symlink for %s: %w", filename, err)
			}
		}
	}

	return nil
}
