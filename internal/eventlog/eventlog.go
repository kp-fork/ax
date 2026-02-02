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

// Package eventlog implements event logging for session durability.
// Event log entries use JSON Lines format with one log file per session.
package eventlog

import (
	"context"
	"time"

	"github.com/google/gar/proto"
)

// EventType represents the type of event stored in the event log.
type EventType string

const (
	EventTypeContent       EventType = "CONTENT"
	EventTypeSessionFailed EventType = "SESSION_FAILED" // cannot resume
	// TODO(jbd): Add EventTypeCompaction.
	// TODO(jbd): Events for agent call lifecycle.
)

// Entry represents a single entry in the event log.
type Entry struct {
	Type         EventType      `json:"type"`
	CheckpointID string         `json:"checkpoint_id,omitempty"` // UUID for checkpoint tracking
	AgentID      string         `json:"agent_id,omitempty"`      // Associated agent ID, could be empty
	Sequence     int64          `json:"seq"`                     // Monotonic sequence number
	Timestamp    time.Time      `json:"timestamp"`
	Data         map[string]any `json:"data"`
}

// EventLog is the interface that all event log implementations must satisfy.
// It provides methods for appending events, reading entries, and managing the log lifecycle.
type EventLog interface {
	// AppendContent appends a content message to the event log with a checkpoint UUID.
	AppendContent(ctx context.Context, checkpointID string, agentID string, content *proto.Content) error

	// AppendState appends a state to the event log.
	AppendState(ctx context.Context, s proto.State) error

	// Load returns all entries from the event log in order.
	// If checkpointID is provided, returns entries up to and including that checkpoint.
	Load(ctx context.Context, checkpointID string) ([]Entry, proto.State, error)

	// Close closes the event log and releases any resources.
	Close() error

	// SessionID returns the session ID for this event log.
	SessionID() string
}

// EventLogFactory is a function that creates EventLog instances for sessions.
type EventLogFactory func(sessionID string) (EventLog, error)
