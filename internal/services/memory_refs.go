package services

import (
	"regexp"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
)

// Memory bodies link to other memories and to uploaded context files with
// wiki-style references. The raw [[...]] text is what the DB and the on-disk
// files store; it is never rewritten into a path, so ingest round-trips it
// unchanged. ResolveRefs maps each reference to a target for views (API/UI),
// and the agent prompts tell the agent how to follow one on disk.
//
//	[[slug]]        memory in the same scope, else in "global"
//	[[scope/slug]]  memory in that scope
//	[[file.ext]]    context file at /akmatori/context/file.ext
//
// Memory slugs never contain a dot and context filenames always do, so the
// two kinds cannot be confused. Anything else is reported as invalid and
// never resolved to a path.
const (
	MemoryRefKindMemory  = "memory"
	MemoryRefKindFile    = "file"
	MemoryRefKindInvalid = "invalid"
)

// memoryRefsMax caps the references resolved per body; each costs a query.
const memoryRefsMax = 50

// agentMemoryDir is where the agent worker mounts the memory directory
// (docker-compose.yml); the API's memoryDir is its own path to the same tree.
const agentMemoryDir = "/akmatori/memory/"

var memoryRefPattern = regexp.MustCompile(`\[\[([^\[\]\n]{1,256})\]\]`)

// MemoryRef is one resolved [[...]] reference in a memory body.
type MemoryRef struct {
	Raw      string `json:"raw"`
	Kind     string `json:"kind"`
	Scope    string `json:"scope,omitempty"`
	Name     string `json:"name,omitempty"`
	MemoryID uint   `json:"memory_id,omitempty"`
	// Path is where the agent reads the target; empty for invalid refs.
	Path   string `json:"path,omitempty"`
	Exists bool   `json:"exists"`
}

// ParseMemoryRefs returns the distinct [[...]] references in body, in order
// of first appearance, classified but not checked against the database.
// scope is the referring memory's scope, used for bare [[slug]] references.
func ParseMemoryRefs(body, scope string) []MemoryRef {
	var refs []MemoryRef
	seen := map[string]bool{}
	for _, m := range memoryRefPattern.FindAllStringSubmatch(body, -1) {
		raw := strings.TrimSpace(m[1])
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		refs = append(refs, classifyMemoryRef(raw, scope))
		if len(refs) == memoryRefsMax {
			break
		}
	}
	return refs
}

func classifyMemoryRef(raw, scope string) MemoryRef {
	ref := MemoryRef{Raw: raw, Kind: MemoryRefKindInvalid}
	switch {
	case strings.Contains(raw, "."):
		if FilenamePattern.MatchString(raw) {
			ref.Kind = MemoryRefKindFile
			ref.Name = raw
			ref.Path = agentContextDir + raw
		}
	case strings.Contains(raw, "/"):
		refScope, name, _ := strings.Cut(raw, "/")
		if validMemoryName(refScope) && validMemoryName(name) {
			ref.Kind = MemoryRefKindMemory
			ref.Scope = refScope
			ref.Name = name
		}
	case validMemoryName(raw):
		ref.Kind = MemoryRefKindMemory
		ref.Scope = scope
		ref.Name = raw
	}
	return ref
}

// ResolveRefs parses m.Body and checks each reference against the memories
// and context_files tables. A bare [[slug]] that does not exist in m.Scope
// falls back to "global". Missing targets are returned with Exists=false so
// a view can show a dangling link.
func (s *MemoryService) ResolveRefs(m *database.Memory) []MemoryRef {
	refs := ParseMemoryRefs(m.Body, m.Scope)
	for i := range refs {
		ref := &refs[i]
		switch ref.Kind {
		case MemoryRefKindFile:
			var count int64
			if err := s.db.Model(&database.ContextFile{}).Where("filename = ?", ref.Name).Count(&count).Error; err == nil {
				ref.Exists = count > 0
			}
		case MemoryRefKindMemory:
			scopes := []string{ref.Scope}
			if !strings.Contains(ref.Raw, "/") && ref.Scope != MemoryScopeGlobal {
				scopes = append(scopes, MemoryScopeGlobal)
			}
			for _, scope := range scopes {
				var target database.Memory
				if err := s.db.Select("id").Where("scope = ? AND name = ?", scope, ref.Name).Take(&target).Error; err == nil {
					ref.Scope = scope
					ref.MemoryID = target.ID
					ref.Exists = true
					break
				}
			}
			ref.Path = agentMemoryDir + ref.Scope + "/" + ref.Name + ".md"
		}
	}
	return refs
}
