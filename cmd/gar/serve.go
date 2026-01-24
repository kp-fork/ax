package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/gar/internal/config"
	"github.com/google/gar/internal/controller"
	"github.com/google/gar/internal/eventlog"
	"github.com/google/gar/internal/server"
	"github.com/spf13/cobra"
)

var (
	serveConfigFile string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run controller as a gRPC server",
	Long: `Run the GAR controller as a gRPC server.
Loads configuration from a YAML file (default: gar.yaml).`,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().StringVar(&serveConfigFile, "config", "gar.yaml", "Path to YAML configuration file")
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load configuration from YAML file
	cfg, err := config.LoadFromFile(serveConfigFile)
	if err != nil {
		return fmt.Errorf("error loading config file '%s': %w\nTip: Create a config file with 'gar serve --help' to see an example", serveConfigFile, err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	fmt.Printf("Starting GAR server at %s...\n", cfg.Server.Address)
	c, err := newControllerFromConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("error creating controller: %w", err)
	}
	defer c.Close()

	// Create server
	srv := server.New(c)

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nReceived interrupt, shutting down...")
		os.Exit(0)
	}()

	// Start serving
	if err := srv.Serve(cfg.Server.Address); err != nil {
		return fmt.Errorf("error serving: %w", err)
	}

	return nil
}

func newControllerFromConfig(ctx context.Context, cfg *config.Config) (*controller.Controller, error) {
	// Create event log factory
	eventLogFactory := func(sessionID string) (eventlog.EventLog, error) {
		return eventlog.NewFileEventLog(eventlog.FileConfig{
			SessionID: sessionID,
			Dir:       cfg.EventLog.Dir,
		})
	}

	// Build controller config
	// The controller will create a default Gemini planner if PlanFunc is nil
	// Gemini config can be customized via environment variables (GEMINI_API_KEY, GAR_GEMINI_MODEL)
	controllerConfig := controller.Config{
		EventLogFactory:     eventLogFactory,
		MaxSteps:            cfg.MaxSteps,
		HealthCheckInterval: cfg.HealthCheckInterval,
	}

	// Create controller
	c, err := controller.New(ctx, controllerConfig)
	if err != nil {
		return nil, err
	}

	// Register remote agents from config
	for _, agentCfg := range cfg.RemoteAgents {
		if err := c.Registry().RegisterRemote(
			agentCfg.ID,
			agentCfg.Name,
			agentCfg.Description,
			agentCfg.Address,
			agentCfg.Metadata,
		); err != nil {
			return nil, fmt.Errorf("failed to register remote agent %s: %w", agentCfg.ID, err)
		}
	}

	return c, nil
}
