package main

import (
	"context"
	"fmt"

	"github.com/google/gar/proto"
	"github.com/spf13/cobra"
)

var (
	registerAgentID    string
	registerAgentName  string
	registerAgentDesc  string
	registerAgentAddr  string
	registerServerAddr string
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a remote agent",
	Long:  `Register a remote agent with the controller so it can be used in sessions.`,
	RunE:  runRegister,
}

func init() {
	registerCmd.Flags().StringVar(&registerAgentID, "agent-id", "", "Agent ID (required)")
	registerCmd.Flags().StringVar(&registerAgentName, "name", "", "Agent name")
	registerCmd.Flags().StringVar(&registerAgentDesc, "description", "", "Agent description")
	registerCmd.Flags().StringVar(&registerAgentAddr, "agent-addr", "", "Agent address (e.g., localhost:50051)")
	registerCmd.Flags().StringVar(&registerServerAddr, "server", "localhost:8494", "gRPC controller server address (default: localhost:8494)")
	registerCmd.MarkFlagRequired("agent-id")
	registerCmd.MarkFlagRequired("agent-addr")
}

func runRegister(cmd *cobra.Command, args []string) error {
	fmt.Printf("Registering agent: %s at %s\n", registerAgentID, registerAgentAddr)
	if registerAgentName != "" {
		fmt.Printf("  Name: %s\n", registerAgentName)
	}
	if registerAgentDesc != "" {
		fmt.Printf("  Description: %s\n", registerAgentDesc)
	}

	conn, err := openConn(inspectServerAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := proto.NewGARServiceClient(conn)

	// Register remote agent
	_, err = client.RegisterAgent(context.Background(), &proto.RegisterAgentRequest{
		AgentId:     registerAgentID,
		Name:        registerAgentName,
		Description: registerAgentDesc,
		Address:     registerAgentAddr,
	})
	if err != nil {
		return fmt.Errorf("error registering agent: %w", err)
	}

	fmt.Println("Agent registered successfully")
	return nil
}
