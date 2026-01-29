// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/gar/internal/eventlog"
	"github.com/google/gar/proto"
	"github.com/google/uuid"
)

// Session represents an agentic loop execution session.
// It maintains in-memory state and uses event log for durability.
type Session struct {
	ID string

	mu             sync.RWMutex
	eventLog       eventlog.EventLog
	currentStep    int
	state          proto.State
	activeAgents   []string
	messageHistory []*proto.Content
	checkpointIDs  []string // Ordered list of checkpoint UUIDs

	createdAt time.Time
	updatedAt time.Time
}

// EventLogFactory is a function that creates EventLog instances for sessions.
type EventLogFactory func(sessionID string) (eventlog.EventLog, error)

// SessionManager manages multiple sessions.
type SessionManager struct {
	mu              sync.RWMutex
	sessions        map[string]*Session
	eventLogFactory EventLogFactory
}

// NewSessionManager creates a new session manager with a custom EventLog factory.
func NewSessionManager(factory EventLogFactory) *SessionManager {
	return &SessionManager{
		sessions:        make(map[string]*Session),
		eventLogFactory: factory,
	}
}

// NewSession creates a new session with the given ID.
func (sm *SessionManager) NewSession(sessionID string) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if session already ok
	if _, ok := sm.sessions[sessionID]; ok {
		return nil, fmt.Errorf("session %s already exists", sessionID)
	}

	// Create event log for this session using the factory
	el, err := sm.eventLogFactory(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to create event log: %w", err)
	}

	now := time.Now()
	session := &Session{
		ID:             sessionID,
		state:          proto.State_STATE_UNSPECIFIED,
		currentStep:    0,
		activeAgents:   []string{},
		messageHistory: []*proto.Content{},
		checkpointIDs:  []string{},
		createdAt:      now,
		updatedAt:      now,
		eventLog:       el,
	}

	sm.sessions[sessionID] = session
	return session, nil
}

// LoadSession loads an existing session from event log.
func (sm *SessionManager) LoadSession(ctx context.Context, sessionID string) (*Session, error) {
	return sm.LoadSessionFromCheckpoint(ctx, sessionID, "")
}

// LoadSessionFromCheckpoint loads an existing session from event log up to a specific checkpoint.
// If checkpointID is empty, loads to the latest state.
// If checkpointID is provided, loads up to and including that checkpoint UUID.
func (sm *SessionManager) LoadSessionFromCheckpoint(ctx context.Context, sessionID string, checkpointID string) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if already loaded - remove it to reload fresh from checkpoint
	delete(sm.sessions, sessionID)

	// Open event log for replay using the factory
	replayLog, err := sm.eventLogFactory(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to open event log for replay: %w", err)
	}
	defer replayLog.Close()

	// TODO(jbd): Propagate the context properly.
	entries, err := replayLog.RetrieveEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get event log entries: %w", err)
	}

	// Reconstruct session state from event log
	session := &Session{
		ID:             sessionID,
		state:          proto.State_STATE_UNSPECIFIED,
		currentStep:    0,
		activeAgents:   []string{},
		messageHistory: []*proto.Content{},
		checkpointIDs:  []string{},
		createdAt:      time.Now(),
		updatedAt:      time.Now(),
	}

	targetReached := false
	checkpointFound := false

	// Replay entries to rebuild state
	for _, entry := range entries {
		// If we've reached the target checkpoint, stop processing
		if targetReached {
			break
		}

		switch entry.Type {
		case eventlog.EventTypeContentIn, eventlog.EventTypeContentOut:
			content := &proto.Content{
				Role:     getStringFromData(entry.Data, "role"),
				Type:     getStringFromData(entry.Data, "type"),
				Mimetype: getStringFromData(entry.Data, "mimetype"),
				Data:     getStringFromData(entry.Data, "data"),
			}
			session.messageHistory = append(session.messageHistory, content)

			// Track checkpoint ID if present
			if entry.CheckpointID != "" {
				session.checkpointIDs = append(session.checkpointIDs, entry.CheckpointID)

				// Check if this is the target checkpoint
				if checkpointID != "" && entry.CheckpointID == checkpointID {
					targetReached = true
					checkpointFound = true
				}
			}
		}

		session.updatedAt = entry.Timestamp
	}

	// Validate checkpoint ID if provided
	if checkpointID != "" && !checkpointFound {
		return nil, fmt.Errorf("checkpoint ID %s not found in session", checkpointID)
	}

	// Reopen event log for appending using the factory
	el, err := sm.eventLogFactory(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to reopen event log: %w", err)
	}
	session.eventLog = el

	sm.sessions[sessionID] = session
	return session, nil
}

// GetSession retrieves a session by ID.
func (sm *SessionManager) GetSession(sessionID string) (*Session, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	return session, nil
}

// CloseSession closes a session and its event log.
func (sm *SessionManager) CloseSession(sessionID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}

	if err := session.eventLog.Close(); err != nil {
		return fmt.Errorf("failed to close event log: %w", err)
	}

	delete(sm.sessions, sessionID)
	return nil
}

// CloseAll closes all active sessions.
func (sm *SessionManager) CloseAll() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for sessionID, session := range sm.sessions {
		if err := session.eventLog.Close(); err != nil {
			// Log error but continue closing other sessions
			_ = err
		}
		delete(sm.sessions, sessionID)
	}
}

// WriteContentIn appends an incoming content message to the session.
// Creates a checkpoint only if checkpoint_id is provided in the content.
func (s *Session) WriteContentIn(ctx context.Context, content *proto.Content) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Use checkpoint_id from content if provided
	checkpointID := content.CheckpointId

	if checkpointID != "" {
		// TODO(jbd): Optimize the lookup.
		for _, existingID := range s.checkpointIDs {
			if existingID == checkpointID {
				return "", fmt.Errorf("checkpoint %s already exists", checkpointID)
			}
		}
	}

	if err := s.eventLog.AppendContent(ctx, eventlog.EventTypeContentIn, checkpointID, content); err != nil {
		return "", err
	}

	s.messageHistory = append(s.messageHistory, content)
	if checkpointID != "" {
		s.checkpointIDs = append(s.checkpointIDs, checkpointID)
	}
	s.updatedAt = time.Now()
	return checkpointID, nil
}

// WriteContentOut appends an outgoing content message to the session with a new checkpoint.
func (s *Session) WriteContentOut(ctx context.Context, content *proto.Content) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate a new checkpoint UUID
	checkpointID := uuid.New().String()

	if err := s.eventLog.AppendContent(ctx, eventlog.EventTypeContentOut, checkpointID, content); err != nil {
		return "", err
	}

	s.messageHistory = append(s.messageHistory, content)
	s.checkpointIDs = append(s.checkpointIDs, checkpointID)
	s.updatedAt = time.Now()
	return checkpointID, nil
}

// SetState updates the session state.
func (s *Session) SetState(state proto.State) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = state
	s.updatedAt = time.Now()
}

func (s *Session) State() proto.State {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.state
}

// AdvanceStep increments the current step.
func (s *Session) AdvanceStep() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentStep++
	s.updatedAt = time.Now()
}

func (s *Session) CurrentStep() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.currentStep
}

func (s *Session) ActiveAgents() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.activeAgents
}

func (s *Session) MessageHistory() []*proto.Content {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.messageHistory
}

func (s *Session) CheckpointIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.checkpointIDs
}

func (s *Session) CreatedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.createdAt
}

func (s *Session) UpdatedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.updatedAt
}

// Helper function to extract string from map[string]any
func getStringFromData(data map[string]any, key string) string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}
