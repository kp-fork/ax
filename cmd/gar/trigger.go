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

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/gar/agent"
	"github.com/google/gar/cmd/gar/internal"
	"github.com/google/gar/internal/config"
	"github.com/google/gar/internal/controller"
	"github.com/google/gar/proto"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var (
	triggerSessionID  string
	triggerAgentID    string
	triggerInput      string
	triggerServerAddr string
	triggerConfigFile string
)

var triggerCmd = &cobra.Command{
	Use:   "trigger",
	Short: "Trigger a new session or resume an existing one",
	Long: `Trigger a new agentic session or resume an existing one.
If no session ID is provided, a new UUID will be generated.`,
	RunE: runTrigger,
}

func init() {
	triggerCmd.Flags().StringVar(&triggerSessionID, "session", "", "Session ID (optional, generates UUID if not provided)")
	triggerCmd.Flags().StringVar(&triggerAgentID, "agent", "", "Agent ID (optional, planner is used if not specified)")
	triggerCmd.Flags().StringVar(&triggerInput, "input", "", "Input message to send (optional)")
	triggerCmd.Flags().StringVar(&triggerServerAddr, "server", "", "gRPC controller server address (if specified, connects to remote server; otherwise runs with a local built-in GAR server)")
	triggerCmd.Flags().StringVar(&triggerConfigFile, "config", "gar.yaml", "Path to YAML configuration file (only used with a local built-in GAR server)")
}

// TODO(jbd): Add multimodal input flags, e.g. --input-image.

var (
	triggerController *controller.Controller
)

func runTrigger(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Generate UUID if no session ID provided
	if triggerSessionID == "" {
		triggerSessionID = uuid.New().String()
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nReceived interrupt, shutting down...")
		if triggerController != nil {
			triggerController.Close()
		}
		cancel()
	}()

	return triggerLoop(ctx, triggerSessionID, triggerAgentID, triggerInput)
}

func triggerLoop(ctx context.Context, sessionID string, agentID string, input string) error {
	d := internal.NewDisplay(sessionID)
	d.DisplayHeader()

	if input == "" {
		var err error
		input, err = d.PromptForInput()
		if err != nil {
			return err
		}
	}

	d.DisplayInput(input)
	history := []*proto.Content{
		{
			Role: "user",
			Content: &proto.Content_Text{
				Text: &proto.TextContent{
					Text: input,
				},
			},
		},
	}

	for {
		conf, outputs, err := runAutoTrigger(ctx, d, sessionID, agentID, history)
		if err != nil {
			return err
		}
		history = append(history, outputs...)

		if conf != nil {
			for {
				approved, err := d.PromptForApproval(conf.Question)
				if err != nil {
					return err
				}
				var decision []*proto.Content
				if approved {
					decision = []*proto.Content{{
						Role: "user",
						Content: &proto.Content_Confirmation{
							Confirmation: &proto.ConfirmationContent{
								Id: conf.Id,
								Decision: &proto.ConfirmationContent_Approval{
									Approval: &proto.ApprovalDecision{Approved: true},
								},
							},
						},
					}}
				} else {
					decision = []*proto.Content{{
						Role: "user",
						Content: &proto.Content_Confirmation{
							Confirmation: &proto.ConfirmationContent{
								Id: conf.Id,
								Decision: &proto.ConfirmationContent_Decline{
									Decline: &proto.DeclineDecision{Declined: true},
								},
							},
						},
					}}
				}
				// The task is still pending, we need to only send the answer.
				// not the full history. Because we executor will put the full
				// history together.
				conf, outputs, err = runAutoTrigger(ctx, d, sessionID, agentID, decision)
				if err != nil {
					return err
				}
				history = append(history, decision...)
				history = append(history, outputs...)
				if conf == nil {
					break
				}
			}
		}

		// Once we finished a task, we should start another one
		// to continue the conversation with history.
		sessionID = uuid.NewString()
		d := internal.NewDisplay(sessionID)
		d.DisplayHeader()

		// Remove all the function calls, confirmations,
		// and function responses. They are not relevant
		// for the upcoming executions.
		history = resetHistory(history)

		input, err = d.PromptForInput()
		if err != nil {
			return err
		}
		d.DisplayInput(input)
		history = append(history, &proto.Content{
			Role: "user",
			Content: &proto.Content_Text{
				Text: &proto.TextContent{
					Text: input,
				},
			},
		})
	}
}

func runAutoTrigger(ctx context.Context, d *internal.Display, sessionID string, agentID string, inputs []*proto.Content) (*proto.ConfirmationContent, []*proto.Content, error) {
	fn := runTriggerHeadless
	if triggerServerAddr != "" {
		fn = runTriggerServer
	}
	return fn(ctx, d, sessionID, agentID, inputs)
}

func runTriggerHeadless(ctx context.Context, d *internal.Display, sessionID string, agentID string, inputs []*proto.Content) (*proto.ConfirmationContent, []*proto.Content, error) {
	if triggerController == nil {
		cfg, err := config.LoadFromFile(triggerConfigFile)
		if err != nil {
			return nil, nil, fmt.Errorf("error loading config file '%s': %w", triggerConfigFile, err)
		}

		c, err := newControllerFromConfig(ctx, cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating controller: %w", err)
		}
		triggerController = c
	}

	var checkpoint string
	var outputs []*proto.Content
	var confirmation *proto.ConfirmationContent
	outputHandler := agent.OutputHandler(func(resp *proto.ProcessResponse) error {
		if resp.CheckpointId != "" {
			checkpoint = resp.CheckpointId
		}

		for _, c := range resp.Contents {
			if conf := c.GetConfirmation(); conf != nil {
				confirmation = conf
			}
		}
		outputs = append(outputs, resp.Contents...)
		displayContents(d, resp.Contents)
		return nil
	})
	if err := triggerController.TriggerSession(ctx, sessionID, agentID, &proto.ProcessRequest{
		Contents: inputs,
	}, outputHandler); err != nil {
		return nil, nil, fmt.Errorf("error triggering session with local server: %w", err)
	}

	if confirmation == nil {
		d.FinishOutput(checkpoint)
	}
	return confirmation, outputs, nil
}

func runTriggerServer(ctx context.Context, d *internal.Display, sessionID string, agentID string, inputs []*proto.Content) (*proto.ConfirmationContent, []*proto.Content, error) {
	conn, err := connect(triggerServerAddr)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	client := proto.NewGARServiceClient(conn)
	stream, err := client.TriggerSession(ctx, &proto.TriggerSessionRequest{
		SessionId: sessionID,
		AgentId:   agentID,
		Inputs:    inputs,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error triggering session: %w", err)
	}

	var checkpoint string
	var outputs []*proto.Content
	var confirmation *proto.ConfirmationContent
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("error receiving response: %w", err)
		}
		if resp.Outputs != nil {
			for _, c := range resp.Outputs {
				if conf := c.GetConfirmation(); conf != nil {
					confirmation = conf
				}
			}
			outputs = append(outputs, resp.Outputs...)
			displayContents(d, resp.Outputs)
		}
	}
	if confirmation == nil {
		d.FinishOutput(checkpoint)
	}
	return confirmation, outputs, nil
}

func displayContents(d *internal.Display, contents []*proto.Content) {
	for _, output := range contents {
		switch o := output.Content.(type) {
		case *proto.Content_Text:
			d.DisplayOutput(o.Text.Text)
		case *proto.Content_Confirmation:
			// Let the confirmation prompt handle displaying the question.
		case *proto.Content_FunctionCall:
			// No-op for cleaner CLI logs
		case *proto.Content_FunctionResponse:
			// Only print if the tool returned an error, otherwise skip
			respMap := o.FunctionResponse.Response.AsMap()
			if errStr, ok := respMap["error"]; ok {
				d.DisplayOutput(fmt.Sprintf("\n[TOOL ERROR for %s]\n%v\n", o.FunctionResponse.Name, errStr))
			}
		default:
			d.DisplayOutput(fmt.Sprintf("unknown output type: %v", o))
		}
	}
}

func resetHistory(history []*proto.Content) []*proto.Content {
	var out []*proto.Content
	for _, c := range history {
		if c.GetFunctionCall() != nil {
			continue
		}
		if c.GetFunctionResponse() != nil {
			continue
		}
		if c.GetConfirmation() != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}
