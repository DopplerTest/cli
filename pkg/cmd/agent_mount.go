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

var agentMountCmd = &cobra.Command{
	Use:   "mount",
	Short: "The host directories an agent can see, mounted under /workspace/<name>",
	Args:  cobra.NoArgs,
}

var agentMountAddCmd = &cobra.Command{
	Use:   "add <agent> <path>[:ro]",
	Short: "Give the agent a directory, read-only with :ro. Takes effect on its next run",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		spec, err := proxy.AddMount(p, args[1])
		if err != nil {
			utils.HandleError(err, "invalid directory")
		}
		if err := proxy.SaveAgent(configuration.UserConfigDir, p); err != nil {
			utils.HandleError(err, "unable to save the agent")
		}
		if utils.OutputJSON {
			printJSON(map[string]any{"agent": p.Name, "mounts": p.Mounts})
			return
		}
		utils.Log(fmt.Sprintf("Added %s to agent %s. It is mounted the next time the agent runs.", spec, p.Name))
	},
}

var agentMountRemoveCmd = &cobra.Command{
	Use:   "remove <agent> <path>",
	Short: "Take a directory away from the agent. Takes effect on its next run",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		if !proxy.RemoveMount(p, args[1]) {
			utils.HandleError(fmt.Errorf("agent %s does not have the directory %s", p.Name, args[1]))
		}
		if err := proxy.SaveAgent(configuration.UserConfigDir, p); err != nil {
			utils.HandleError(err, "unable to save the agent")
		}
		if utils.OutputJSON {
			printJSON(map[string]any{"agent": p.Name, "mounts": p.Mounts})
			return
		}
		utils.Log(fmt.Sprintf("Removed %s from agent %s. A running sandbox keeps it until it exits.", args[1], p.Name))
	},
}

func init() {
	agentMountCmd.AddCommand(agentMountAddCmd)
	agentMountCmd.AddCommand(agentMountRemoveCmd)
	agentCmd.AddCommand(agentMountCmd)
}
