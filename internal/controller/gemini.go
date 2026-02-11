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
	"os"
	"time"

	"github.com/google/gar/agent"
	"github.com/google/gar/proto"
	"google.golang.org/genai"
)

// GeminiPlannerConfig configures the Gemini-based planner.
type GeminiPlannerConfig struct {
	APIKey       string        // Google AI API key (for programmatic use only; if empty, uses GEMINI_API_KEY env var - recommended)
	Model        string        // Model name (default: gemini-3-flash-preview)
	MaxTokens    int32         // Max output tokens (default: 8192)
	Timeout      time.Duration // Request timeout (default: 60s)
	SystemPrompt string        // Custom system prompt (optional)
}

func NewGeminiPlanner(ctx context.Context, registry *Registry, config GeminiPlannerConfig) (agent.Agent, error) {
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}
	if config.Model == "" {
		config.Model = os.Getenv("GAR_GEMINI_MODEL")
		if config.Model == "" {
			config.Model = "gemini-3-flash-preview"
		}
	}
	if config.APIKey == "" {
		config.APIKey = os.Getenv("GEMINI_API_KEY")
		if config.APIKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY not set and no API key provided in config")
		}
	}

	// Default system prompt
	if config.SystemPrompt == "" {
		config.SystemPrompt = `You are an intelligent agent orchestrator. Your role is to analyze the conversation history and user requests, then select the most appropriate agent to handle the task.

Available agents have been provided to you as function tools. Each agent has:
- A unique ID
- A description of its capabilities

Your job is to:
1. Analyze the current conversation context and understand what needs to be done
2. Select the best agent for the task by calling the appropriate function
3. If enough work is done, stop to indicate completion

Guidelines:
- Choose agents based on their capabilities and the user's needs
- If no suitable agent exists, stop.
- Keep the conversation context in mind when selecting agents`
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: config.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &geminiPlannerAgent{
		client:   client,
		registry: registry,
		config:   config,
	}, nil
}

type geminiPlannerAgent struct {
	client   *genai.Client
	registry *Registry
	config   GeminiPlannerConfig
}

// agentsToTools converts registry agents to Gemini function declarations.
func agentsToTools(registry *Registry) ([]*genai.Tool, error) {
	healthyAgents := registry.ListHealthy()
	if len(healthyAgents) == 0 {
		return nil, fmt.Errorf("no healthy agents available")
	}

	var tools []*genai.Tool
	// TODO(lhuan): Check if agentsToTools returns an error or empty list and return a friendly "no agent available, try later" error.
	for _, id := range healthyAgents {
		info, err := registry.GetInfo(id)
		if err != nil {
			continue // Skip agents we can't get info for
		}

		// Create a function declaration for this agent
		funcDecl := &genai.FunctionDeclaration{
			Name:        id, // Use agent ID as function name
			Description: fmt.Sprintf("%s, %s", info.Name, info.Description),
		}

		tools = append(tools, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{funcDecl},
		})
	}
	return tools, nil
}

func (p *geminiPlannerAgent) Process(ctx context.Context, sessionID string, incoming *proto.ProcessRequest, handler agent.OutputHandler) error {
	// Convert agents to Gemini function declarations
	tools, err := agentsToTools(p.registry)
	if err != nil {
		return fmt.Errorf("failed to convert agents to tools: %w", err)
	}

	// Convert session to conversation history
	contents := protoToContents(incoming.Contents)
	resp, err := p.client.Models.GenerateContent(ctx, p.config.Model, contents, &genai.GenerateContentConfig{
		Tools:             tools,
		SystemInstruction: genai.Text(p.config.SystemPrompt)[0],
		MaxOutputTokens:   p.config.MaxTokens,
		CandidateCount:    1,
	})

	if err != nil {
		return fmt.Errorf("failed to generate in planner: %w", err)
	}
	if len(resp.Candidates) == 0 {
		return fmt.Errorf("no candidates from Gemini in planner")
	}
	candidate := resp.Candidates[0]
	if candidate.Content == nil || candidate.Content.Parts == nil {
		if candidate.FinishReason == genai.FinishReasonStop {
			return nil // No more tasks
		}
		return fmt.Errorf("no content in candidates from Gemini")
	}

	// Look for function calls in the response
	for _, part := range candidate.Content.Parts {
		if part == nil {
			continue
		}

		if fc := part.FunctionCall; fc != nil {
			if err := handler(&proto.ProcessResponse{
				AgentHandoff: fc.Name,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// HealthCheck checks if the agent is healthy and responsive.
// Returns an error if the agent is unhealthy or unreachable.
func (p *geminiPlannerAgent) HealthCheck(ctx context.Context) error {
	return nil // always healthy
}

// Close gracefully shuts down the agent and releases resources.
func (p *geminiPlannerAgent) Close() error {
	return nil
}

// protoToContents converts session message history to Gemini conversation format.
func protoToContents(inputs []*proto.Content) []*genai.Content {
	var contents []*genai.Content

	// Convert each message to Gemini format
	for _, msg := range inputs {
		role := msg.Role
		if role != "user" {
			role = "model"
		}

		switch m := msg.Content.(type) {
		case *proto.Content_Text:
			contents = append(contents, &genai.Content{
				Role: role,
				Parts: []*genai.Part{
					{
						Text: m.Text.Text,
					},
				},
			})
			// TODO(jbd): Handle other content types (e.g., images, files)

		}
	}
	return contents
}
