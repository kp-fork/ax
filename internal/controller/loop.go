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

	"github.com/google/gar/agent"
	"github.com/google/gar/proto"
)

const plannerAgentID = "__planner"

// LoopExecutor orchestrates the agentic loop workflow.
// It implements the plan-execute-evaluate-checkpoint cycle.
type LoopExecutor struct {
	registry       *Registry
	sessionManager *SessionManager
	maxSteps       int
	planner        agent.Agent
}

// LoopConfig configures the loop executor.
type LoopConfig struct {
	Registry       *Registry
	SessionManager *SessionManager
	MaxSteps       int
	Planner        agent.Agent
}

// NewLoopExecutor creates a new loop executor.
func NewLoopExecutor(ctx context.Context, config LoopConfig) (*LoopExecutor, error) {
	if config.Registry == nil {
		return nil, fmt.Errorf("registry cannot be nil")
	}
	if config.SessionManager == nil {
		return nil, fmt.Errorf("session manager cannot be nil")
	}
	if config.MaxSteps == 0 {
		return nil, fmt.Errorf("max_steps cannot be zero")
	}
	// Plan function is required
	if config.Planner == nil {
		return nil, fmt.Errorf("planner is required")
	}

	return &LoopExecutor{
		registry:       config.Registry,
		sessionManager: config.SessionManager,
		maxSteps:       config.MaxSteps,
		planner:        config.Planner,
	}, nil
}

// Execute starts a new agentic loop execution for the given session.
func (e *LoopExecutor) Execute(ctx context.Context, session *Session, incoming *proto.ProcessRequest, handler agent.OutputHandler) error {
	return e.runLoop(ctx, session, incoming, handler)
}

// runLoop executes the main agentic loop.
// It runs up to maxSteps iterations per trigger/resume invocation.
func (e *LoopExecutor) runLoop(ctx context.Context, session *Session, incoming *proto.ProcessRequest, handler agent.OutputHandler) error {
	steps := 0

	for _, agentID := range session.WaitingAgents() {
		buffer := session.WaitingBuffer(agentID)
		_ = buffer
		// TODO(jbd): Run the agent with session history and buffer as input.
		return fmt.Errorf("resuming waiting agents is not yet supported")
	}

	// Write the new inputs to the event log.
	if err := session.WriteContent(ctx, "", incoming.CheckpointId, incoming.Contents); err != nil {
		return fmt.Errorf("failed to write input content: %w", err)
	}

	var nextAgentID = plannerAgentID // Start from planner.
	for steps < e.maxSteps {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		history := session.History()
		handoff, err := e.runAgent(ctx, session, nextAgentID, history, handler)
		if err != nil {
			return err
		}

		if handoff == "" {
			// No more agent handoffs to execute; loop is complete.
			return nil
		}

		nextAgentID = handoff
		steps++
	}

	// Can be resumed later with another trigger
	return fmt.Errorf("max steps per trigger (%d) reached", e.maxSteps)
}

func (e *LoopExecutor) runAgent(ctx context.Context, session *Session, agentID string, inputs []*proto.Content, handler agent.OutputHandler) (handoff string, err error) {
	var buffer []*proto.Content

	// Helper to flush buffer to session
	flushBuffer := func(checkpointID string) error {
		if len(buffer) == 0 {
			return nil
		}
		if err := session.WriteContent(ctx, agentID, checkpointID, buffer); err != nil {
			return fmt.Errorf("failed to write content: %w", err)
		}
		buffer = []*proto.Content{} // Clear buffer after successful write
		return nil
	}

	runHandler := func(outgoing *proto.ProcessResponse) error {
		buffer = append(buffer, outgoing.Contents...)

		if outgoing.CheckpointId != "" {
			if err := flushBuffer(outgoing.CheckpointId); err != nil {
				return err
			}
		}

		if outgoing.AgentHandoff != "" {
			// Write any pending content before handoff
			if err := flushBuffer(""); err != nil {
				return err
			}

			handoff = outgoing.AgentHandoff
			if err := session.WriteAgentHandoff(ctx, agentID, handoff); err != nil {
				return fmt.Errorf("failed to write handoff: %w", err)
			}
		}
		return handler(outgoing)
	}

	var a agent.Agent
	if agentID == plannerAgentID {
		a = e.planner
	} else {
		a, err = e.registry.Get(agentID)
		if err != nil {
			return "", fmt.Errorf("failed to get agent: %w", err)
		}
	}

	if err := a.Process(ctx, session.ID(), &proto.ProcessRequest{
		Contents: inputs,
	}, runHandler); err != nil {
		return "", fmt.Errorf("agent process failed: %w", err)
	}

	// Final flush of any remaining buffer content
	if err := flushBuffer(""); err != nil {
		return "", err
	}
	return handoff, nil
}
