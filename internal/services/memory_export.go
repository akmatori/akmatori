package services

import (
	"archive/zip"
	"fmt"
	"io"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// ExportZip writes every memory to w as a zip of <scope>/<name>.md files in
// the same format SyncMemoryFiles writes to disk, so an export can be read
// by people and by the ingester alike. The body keeps its raw [[...]] links.
func (s *MemoryService) ExportZip(w io.Writer) error {
	var memories []database.Memory
	if err := s.db.Order("scope asc, name asc").Find(&memories).Error; err != nil {
		return fmt.Errorf("failed to query memories: %w", err)
	}

	zw := zip.NewWriter(w)
	for _, m := range memories {
		modified := m.UpdatedAt
		if modified.IsZero() {
			modified = time.Now()
		}
		f, err := zw.CreateHeader(&zip.FileHeader{
			Name:     m.Scope + "/" + m.Name + ".md",
			Method:   zip.Deflate,
			Modified: modified,
		})
		if err != nil {
			return fmt.Errorf("zip entry %s/%s: %w", m.Scope, m.Name, err)
		}
		if _, err := io.WriteString(f, renderMemoryFile(m)); err != nil {
			return fmt.Errorf("zip write %s/%s: %w", m.Scope, m.Name, err)
		}
	}
	return zw.Close()
}
