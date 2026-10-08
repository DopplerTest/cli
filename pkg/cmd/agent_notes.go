/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"fmt"

	"github.com/DopplerHQ/cli/pkg/configuration"
	"github.com/DopplerHQ/cli/pkg/proxy"
	"github.com/DopplerHQ/cli/pkg/utils"
	"github.com/spf13/cobra"
)

var agentNotesCmd = &cobra.Command{
	Use:   "notes <agent>",
	Short: "Print the sandbox notes the agent is handed at the start of each run",
	Long: `Print the notes the agent is handed at the start of each run.

They are rendered from the agent's current proxy config, so this shows exactly what the
agent will be told: what a placeholder is, where each secret may be used, what to do when
a request is refused, and where files persist. Nothing in them helps an agent misbehave.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		dir := proxy.AgentDir(configuration.UserConfigDir, p.Name)
		cfg, _, err := proxy.LoadOrScaffold(proxy.AgentConfigPath(dir))
		if err != nil {
			utils.HandleError(err, "unable to read the agent's proxy config")
		}
		scope, _ := cfg.ResolveForConfig(p.Config)
		fmt.Print(proxy.RenderSandboxNotes(scope))
	},
}

func init() {
	agentCmd.AddCommand(agentNotesCmd)
}
