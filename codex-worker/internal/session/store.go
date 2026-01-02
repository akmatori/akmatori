package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Session represents a Codex session
type Session struct {
	IncidentID string    `json:"incident_id"`
	SessionID  string    `json:"session_id"`
	Status     string    `json:"status"` // pending, running, completed, failed
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Response   string    `json:"response,omitempty"`
	FullLog    string    `json:"full_log,omitempty"`
}

// Store manages Codex sessions
type Store struct {
	storePath string
	sessions  map[string]*Session
	mu        sync.RWMutex
}

// NewStore creates a new session store
func NewStore(storePath string) *Store {
	s := &Store{
		storePath: storePath,
		sessions:  make(map[string]*Session),
	}
	s.load()
	return s
}

// Get retrieves a session by incident ID
func (s *Store) Get(incidentID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[incidentID]
}

// Create creates a new session
func (s *Store) Create(incidentID string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	session := &Session{
		IncidentID: incidentID,
		Status:     "pending",
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	s.sessions[incidentID] = session
	s.persist()
	return session
}

// Update updates a session
func (s *Store) Update(incidentID string, fn func(*Session)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, exists := s.sessions[incidentID]
	if !exists {
		session = &Session{
			IncidentID: incidentID,
			StartedAt:  time.Now(),
		}
		s.sessions[incidentID] = session
	}

	fn(session)
	session.UpdatedAt = time.Now()
	s.persist()
}

// SetRunning marks a session as running with a session ID
func (s *Store) SetRunning(incidentID, sessionID string) {
	s.Update(incidentID, func(session *Session) {
		session.SessionID = sessionID
		session.Status = "running"
	})
}

// SetCompleted marks a session as completed
func (s *Store) SetCompleted(incidentID, response, fullLog string) {
	s.Update(incidentID, func(session *Session) {
		session.Status = "completed"
		session.Response = response
		session.FullLog = fullLog
	})
}

// SetFailed marks a session as failed
func (s *Store) SetFailed(incidentID, errorMsg string) {
	s.Update(incidentID, func(session *Session) {
		session.Status = "failed"
		session.Response = errorMsg
	})
}

// Delete removes a session
func (s *Store) Delete(incidentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, incidentID)
	s.persist()
}

// List returns all sessions
func (s *Store) List() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}

// load loads sessions from disk
func (s *Store) load() {
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		// File doesn't exist, start fresh
		return
	}

	var sessions map[string]*Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return
	}

	s.sessions = sessions
}

// persist saves sessions to disk
func (s *Store) persist() {
	// Ensure directory exists
	dir := filepath.Dir(s.storePath)
	os.MkdirAll(dir, 0755)

	data, err := json.MarshalIndent(s.sessions, "", "  ")
	if err != nil {
		return
	}

	os.WriteFile(s.storePath, data, 0644)
}
