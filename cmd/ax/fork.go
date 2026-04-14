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
	"github.com/spf13/cobra"
)

var (
	forkSourceConversation string
	forkSourceSeq         int32
	forkDestConversation   string
	forkServerAddr         string
)

var forkCmd = &cobra.Command{
	Use:   "fork",
	Short: "Fork an event log from a specific checkpoint",
	Long: `Fork an existing agentic event log from a specific checkpoint.
If --dest-conversation is not provided, a new UUID will be generated.`,
	RunE: runFork,
}

func init() {
	forkCmd.Flags().StringVar(&forkSourceConversation, "src-conversation", "", "Source conversation ID to fork from (required)")
	forkCmd.Flags().Int32Var(&forkSourceSeq, "src-seq", 0, "Sequence number to fork from (optional, defaults to latest)")
	forkCmd.Flags().StringVar(&forkDestConversation, "dest-conversation", "", "Destination conversation ID (optional, generates UUID if not provided)")
	forkCmd.Flags().StringVar(&forkServerAddr, "server", "localhost:8494", "gRPC controller server address (default: localhost:8494)")

	forkCmd.MarkFlagRequired("src-conversation")
}

func runFork(cmd *cobra.Command, args []string) error {
	panic("forking not implemented")
}
