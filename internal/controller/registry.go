package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/gar/agent"
)

// AgentType represents the type of agent (local or remote).
type AgentType string

const (
	AgentTypeLocal  AgentType = "local"
	AgentTypeRemote AgentType = "remote"
)

// AgentInfo contains metadata about a registered agent.
type AgentInfo struct {
	ID              string
	Name            string
	Description     string
	Type            AgentType
	Healthy         bool
	LastHealthCheck time.Time
	Metadata        map[string]string
}

// Registry manages a collection of local and remote agents.
// It provides agent discovery, health monitoring, and load balancing.
type Registry struct {
	mu                sync.RWMutex
	agents            map[string]agent.Agent
	agentInfo         map[string]*AgentInfo
	healthCheckTicker *time.Ticker
	stopHealthCheck   chan struct{}
}

// NewRegistry creates a new agent registry.
func NewRegistry(healthCheckInterval time.Duration) *Registry {
	if healthCheckInterval == 0 {
		healthCheckInterval = 30 * time.Second
	}

	r := &Registry{
		agents:          make(map[string]agent.Agent),
		agentInfo:       make(map[string]*AgentInfo),
		stopHealthCheck: make(chan struct{}),
	}

	// Start background health check if interval is positive
	if healthCheckInterval > 0 {
		r.healthCheckTicker = time.NewTicker(healthCheckInterval)
		go r.runHealthChecks()
	}

	return r
}

// RegisterLocal registers a local (in-process) agent.
func (r *Registry) RegisterLocal(a agent.Agent, name, description string, metadata map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := a.ID()
	if _, ok := r.agents[id]; ok {
		return fmt.Errorf("agent %s already registered", id)
	}

	r.agents[id] = a
	r.agentInfo[id] = &AgentInfo{
		ID:              id,
		Name:            name,
		Description:     description,
		Type:            AgentTypeLocal,
		Healthy:         true,
		LastHealthCheck: time.Now(),
		Metadata:        metadata,
	}

	return nil
}

// RegisterRemote registers a remote agent by creating a remote agent client.
func (r *Registry) RegisterRemote(id, name, description, address string, metadata map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[id]; exists {
		return fmt.Errorf("agent %s already registered", id)
	}

	// Create remote agent client
	remoteAgent, err := agent.NewRemoteAgent(agent.RemoteAgentConfig{
		ID:         id,
		Address:    address,
		Reconnect:  true,
		MaxRetries: 3,
	})
	if err != nil {
		return fmt.Errorf("failed to create remote agent: %w", err)
	}

	r.agents[id] = remoteAgent
	r.agentInfo[id] = &AgentInfo{
		ID:              id,
		Name:            name,
		Description:     description,
		Type:            AgentTypeRemote,
		Healthy:         false, // Will be checked by health monitor
		LastHealthCheck: time.Time{},
		Metadata:        metadata,
	}

	return nil
}

// Unregister removes an agent from the registry.
func (r *Registry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, exists := r.agents[id]
	if !exists {
		return fmt.Errorf("agent %s not found", id)
	}

	// Close the agent
	if err := agent.Close(); err != nil {
		return fmt.Errorf("failed to close agent: %w", err)
	}

	delete(r.agents, id)
	delete(r.agentInfo, id)

	return nil
}

// Get retrieves an agent by ID.
func (r *Registry) Get(id string) (agent.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	a, exists := r.agents[id]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", id)
	}

	return a, nil
}

// GetInfo retrieves agent metadata by ID.
func (r *Registry) GetInfo(id string) (*AgentInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.agentInfo[id]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", id)
	}

	return info, nil
}

// List returns all registered agent IDs.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}

	return ids
}

// ListByType returns all agent IDs of a specific type.
func (r *Registry) ListByType(agentType AgentType) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0)
	for id, info := range r.agentInfo {
		if info.Type == agentType {
			ids = append(ids, id)
		}
	}

	return ids
}

// ListHealthy returns all healthy agent IDs.
func (r *Registry) ListHealthy() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0)
	for id, info := range r.agentInfo {
		if info.Healthy {
			ids = append(ids, id)
		}
	}

	return ids
}

// SelectAgent selects an agent based on a simple load balancing strategy.
// Currently uses round-robin among healthy agents of the specified type.
func (r *Registry) SelectAgent(agentType AgentType) (agent.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Find healthy agents of the specified type
	var candidates []string
	for id, info := range r.agentInfo {
		if info.Type == agentType && info.Healthy {
			candidates = append(candidates, id)
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no healthy agents of type %s available", agentType)
	}

	// Simple round-robin: return first healthy agent
	// TODO: Implement more sophisticated load balancing
	selectedID := candidates[0]
	return r.agents[selectedID], nil
}

// HealthCheck performs a health check on a specific agent.
func (r *Registry) HealthCheck(id string) error {
	a, err := r.Get(id)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = a.HealthCheck(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()

	info, exists := r.agentInfo[id]
	if exists {
		info.Healthy = (err == nil)
		info.LastHealthCheck = time.Now()
	}

	return err
}

// runHealthChecks runs periodic health checks on all agents.
func (r *Registry) runHealthChecks() {
	for {
		select {
		case <-r.healthCheckTicker.C:
			r.performHealthChecks()
		case <-r.stopHealthCheck:
			return
		}
	}
}

// performHealthChecks checks the health of all registered agents.
func (r *Registry) performHealthChecks() {
	ids := r.List()
	for _, id := range ids {
		// Run health check (ignore errors, status is updated in HealthCheck method)
		_ = r.HealthCheck(id)
	}
}

// Close stops the registry and closes all agents.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Stop health checks
	if r.healthCheckTicker != nil {
		r.healthCheckTicker.Stop()
		close(r.stopHealthCheck)
	}

	// Close all agents
	var firstErr error
	for id, a := range r.agents {
		if err := a.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("failed to close agent %s: %w", id, err)
		}
	}

	return firstErr
}
