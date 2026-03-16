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

// Package controller implements the single-writer orchestrator that coordinates
// agentic loops, manages sessions, and communicates with local and remote agents.
package controller

import (
	"context"
	"fmt"
	"os"
	"path"
	"sync"

	"github.com/google/gar/agent"
	"github.com/google/gar/internal/config"
	"github.com/google/gar/internal/controller/task"
	"github.com/google/gar/internal/testagent"
	"github.com/google/gar/proto"
)

const plannerAgentID = "__planner"

// Controller is the main controller that coordinates all components.
// It acts as a single-writer system for managing agentic loops.
type Controller struct {
	inFlightSessionsMu sync.Mutex
	inFlightSessions   map[string]struct{}
	registry           *Registry
	eventLogBuilder    task.EventLogBuilder
	plannerBuilder     PlannerBuilder
}

// PlannerBuilder is a function that creates a PlanFunc given a Registry.
type PlannerBuilder func(ctx context.Context, r *Registry) (agent.Agent, error)

// Config configures the controller.
type Config struct {
	EventLogBuilder task.EventLogBuilder
	PlannerBuilder  PlannerBuilder
	// TODO(jbd): Add CompacterBuilder.
	HealthCheck config.HealthCheckConfig
}

// New creates a new controller instance.
func New(ctx context.Context, config Config) (*Controller, error) {
	if config.EventLogBuilder == nil {
		config.EventLogBuilder = func(taskID string) (task.EventLog, error) {
			return task.OpenFileEventLog(path.Join(".", "tasklog", taskID+".jsonl"))
		}
	}

	// Initialize agent registry
	registry, err := NewRegistry(config.HealthCheck)
	if err != nil {
		return nil, fmt.Errorf("failed to create registry: %w", err)
	}

	// Determine plan function
	// If no planner builder is provided, use the default Gemini planner.
	if config.PlannerBuilder == nil {
		config.PlannerBuilder = func(ctx context.Context, r *Registry) (agent.Agent, error) {
			return NewGeminiPlannerAgent(ctx, r, GeminiPlannerConfig{})
		}
	}

	return &Controller{
		inFlightSessions: make(map[string]struct{}),
		registry:         registry,
		eventLogBuilder:  config.EventLogBuilder,
		plannerBuilder:   config.PlannerBuilder,
	}, nil
}

// TriggerSession triggers a new agentic loop session or resumes an existing one.
// If sessionID is empty, a UUID will be generated.
// If the session already exists, it will be resumed with optional new inputs.
func (d *Controller) TriggerSession(ctx context.Context, sessionID string, agentID string, incoming *proto.ProcessRequest, handler agent.OutputHandler) error {
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}

	inFlight, cleanup := d.markInFlight(sessionID)
	defer cleanup()

	if inFlight {
		return fmt.Errorf("task %q is already in flight", sessionID)
	}

	planner, err := d.plannerBuilder(ctx, d.registry)
	if err != nil {
		return fmt.Errorf("failed to create planner: %w", err)
	}
	registry := d.registry.Map()
	registry[plannerAgentID] = planner
	registry["gemini"], err = NewGeminiAgent(ctx, GeminiConfig{})
	if err != nil {
		return err
	}
	o := func(resp *proto.ProcessResponse) error {
		// Always filter out from_cache results.
		if !resp.FromCache {
			return handler(resp)
		}
		return nil
	}

	// For testing only! Remove this once the project is stable.
	// TODO(jbd): Remove this before the release.
	if os.Getenv("GAR_TEST_AGENTS") == "1" {
		for id, agent := range testagent.Agents() {
			registry[id] = agent
		}
		if agentID == "" {
			agentID = "coding"
		}
	}

	if agentID == "" {
		agentID = plannerAgentID
	}
	e := task.DefaultExecutor(d.eventLogBuilder, registry)
	return e.Exec(ctx, &agent.Task{
		ID:      sessionID,
		AgentID: agentID,
		Inputs:  incoming.Contents,
	}, o)
}

// ForkSession forks a session from a source session.
// If checkpointId is provided, fork til the checkpoint. Otherwise, fork the whole session.
func (d *Controller) ForkSession(ctx context.Context, sourceSessionID, sourceCheckpoint, destSessionID string) error {
	if sourceSessionID == "" {
		return fmt.Errorf("source session ID is required")
	}
	if destSessionID == "" {
		return fmt.Errorf("destination session ID is required")
	}
	panic("not yet implemented")

	return nil
}

// Registry returns the agent registry.
func (d *Controller) Registry() *Registry {
	return d.registry
}

// Close gracefully shuts down the controller.
func (d *Controller) Close() error {
	if err := d.registry.Close(); err != nil {
		return fmt.Errorf("failed to close registry: %w", err)
	}
	return nil
}

func (d *Controller) markInFlight(sessionID string) (exists bool, cleanup func()) {
	d.inFlightSessionsMu.Lock()
	defer d.inFlightSessionsMu.Unlock()

	_, ok := d.inFlightSessions[sessionID]
	if ok {
		return true, func() {}
	}
	d.inFlightSessions[sessionID] = struct{}{}

	return false, func() {
		d.inFlightSessionsMu.Lock()
		delete(d.inFlightSessions, sessionID)
		d.inFlightSessionsMu.Unlock()
	}
}
