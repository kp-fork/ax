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
	"net"
	"sort"
	"time"

	"github.com/google/ax/internal/config"
	"github.com/google/ax/internal/controller/task"
	"github.com/google/ax/proto"
	"github.com/spf13/cobra"
)

// Data structures
type TextPart struct {
	Text string `json:"text"`
}

type Approval struct {
	Approved bool `json:"approved"`
}

type Confirmation struct {
	ID       string    `json:"id"`
	Question string    `json:"question,omitempty"`
	Approval *Approval `json:"approval,omitempty"`
}

type Content struct {
	Role         string        `json:"role"`
	Text         *TextPart     `json:"text,omitempty"`
	Confirmation *Confirmation `json:"confirmation,omitempty"`
}

type ExecutionEvent struct {
	TaskID    string    `json:"task_id"`
	AgentID   string    `json:"agent_id"`
	Inputs    []Content `json:"inputs"`
	Outputs   []Content `json:"outputs"`
	State     string    `json:"state"`
	Timestamp time.Time `json:"timestamp"`
}

type TaskTrace struct {
	TaskID  string           `json:"task_id"`
	AgentID string           `json:"agent_id"`
	Events  []ExecutionEvent `json:"events"`
}

type TraceData struct {
	RootTaskID string      `json:"root_task_id"`
	Tasks      []TaskTrace `json:"tasks"`
}

var (
	traceServerAddr string
	traceConfigFile string
)

var traceCmd = &cobra.Command{
	Use:   "trace <execution-id>",
	Short: "View the execution trace for a given execution ID (uses SQLite eventlog)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTrace,
}

func init() {
	traceCmd.Flags().StringVar(&traceServerAddr, "server", "localhost:8080", "Server address to listen on")
	traceCmd.Flags().StringVar(&traceConfigFile, "config", "ax.yaml", "Path to YAML configuration file")
}

func runTrace(cmd *cobra.Command, args []string) error {
	taskID := args[0]

	// Load trace data
	data, err := loadTraceData(taskID)
	if err != nil {
		return fmt.Errorf("error loading trace data: %w", err)
	}

	if len(data.Tasks) == 0 {
		return fmt.Errorf("no trace data found for execution-id: %s", taskID)
	}

	// Start HTTP server on specified address
	listener, err := net.Listen("tcp", traceServerAddr)
	if err != nil {
		return fmt.Errorf("failed to bind server (try another using --server): %w", err)
	}

	return serveTraceUI(listener, data, indexHTML)
}

func loadTraceData(rootTaskID string) (*TraceData, error) {
	// The trace command uses the config provided by --config flag
	configPath := traceConfigFile

	events, err := fetchEventsFromDB(configPath, rootTaskID)
	if err != nil {
		return nil, err
	}

	data := &TraceData{
		RootTaskID: rootTaskID,
		Tasks:      buildTaskTraces(rootTaskID, events),
	}

	return data, nil
}

func fetchEventsFromDB(configPath string, rootTaskID string) ([]*proto.ExecutionEvent, error) {
	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("error loading config: %w", err)
	}

	evLog, err := task.OpenSQLiteEventLog(cfg.EventLog.SQLiteFilename)
	if err != nil {
		return nil, fmt.Errorf("could not open sqlite eventlog: %w", err)
	}
	defer evLog.Close()

	ctx := context.Background()
	events, err := evLog.EventsByPrefix(ctx, rootTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to query trace events: %w", err)
	}

	return events, nil
}

func buildTaskTraces(rootTaskID string, events []*proto.ExecutionEvent) []TaskTrace {
	tasksMap := make(map[string][]ExecutionEvent)

	for _, protoEv := range events {
		taskID := protoEv.TaskId
		ev := extractExecutionEvent(taskID, protoEv)
		tasksMap[taskID] = append(tasksMap[taskID], ev)
	}

	var tasks []TaskTrace
	for taskID, evs := range tasksMap {
		agentID := ""
		for _, ev := range evs {
			if ev.AgentID != "" {
				agentID = ev.AgentID
				break
			}
		}
		tasks = append(tasks, TaskTrace{
			TaskID:  taskID,
			AgentID: agentID,
			Events:  evs,
		})
	}

	// Root task first, then sub-tasks sorted by name.
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].TaskID == rootTaskID {
			return true
		}
		if tasks[j].TaskID == rootTaskID {
			return false
		}
		return tasks[i].TaskID < tasks[j].TaskID
	})

	return tasks
}

func extractContents(protoContents []*proto.Content) []Content {
	var results []Content
	for _, c := range protoContents {
		content := Content{Role: c.Role}
		if textC := c.GetText(); textC != nil {
			content.Text = &TextPart{Text: textC.Text}
		} else if conf := c.GetConfirmation(); conf != nil {
			content.Confirmation = &Confirmation{
				ID:       conf.Id,
				Question: conf.Question,
			}
			if app := conf.GetApproval(); app != nil {
				content.Confirmation.Approval = &Approval{Approved: app.Approved}
			} else if dec := conf.GetDecline(); dec != nil {
				content.Confirmation.Approval = &Approval{Approved: !dec.Declined}
			}
		}
		results = append(results, content)
	}
	return results
}

func extractExecutionEvent(taskID string, protoEv *proto.ExecutionEvent) ExecutionEvent {
	ev := ExecutionEvent{
		TaskID:  taskID,
		AgentID: protoEv.AgentId,
	}
	if protoEv.Timestamp != nil {
		ev.Timestamp = protoEv.Timestamp.AsTime()
	}

	ev.State = fmt.Sprint(protoEv.State)
	ev.Outputs = extractContents(protoEv.Outputs)
	ev.Inputs = extractContents(protoEv.Inputs)

	return ev
}
