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
	"context"
	"fmt"

	"github.com/DopplerHQ/cli/pkg/configuration"
	"github.com/DopplerHQ/cli/pkg/proxy"
	"github.com/DopplerHQ/cli/pkg/utils"
	"github.com/spf13/cobra"
)

var agentStopCmd = &cobra.Command{
	Use:   "stop <agent>",
	Short: "Stop the agent's running sandbox",
	Long: `Stop the agent's running sandbox container(s).

The sandbox can only reach the network through the agent's proxy, so once the proxy
is gone a running agent is stranded. The desktop app calls this before stopping the
proxy; it is also useful on its own to end a run from another terminal. The agent
inside is asked to exit (SIGTERM) and ` + "`agent run`" + ` returns normally, so the run
record closes with a real exit code. Nothing running is not an error.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		dockerBin, _ := cmd.Flags().GetString("docker")
		dir := proxy.AgentDir(configuration.UserConfigDir, p.Name)
		port := proxy.PortOfListenAddress(proxy.ReadListenAddress(proxy.AgentConfigPath(dir)))

		ctx := context.Background()
		all, err := proxy.ListSandboxContainers(ctx, dockerBin)
		if err != nil {
			utils.HandleError(err, "unable to list containers; is Docker running?")
		}
		ids := proxy.MatchSandboxContainers(all, port, proxy.AgentHomeVolume(p.Name))
		if err := proxy.StopContainers(ctx, dockerBin, ids, 5); err != nil {
			utils.HandleError(err, "unable to stop the sandbox")
		}
		if ids == nil {
			ids = []string{}
		}
		if utils.OutputJSON {
			printJSON(map[string]any{"agent": p.Name, "stopped": ids})
			return
		}
		if len(ids) == 0 {
			utils.Log(fmt.Sprintf("No running sandbox for agent %s.", p.Name))
			return
		}
		utils.Log(fmt.Sprintf("Stopped %d sandbox container(s) for agent %s.", len(ids), p.Name))
	},
}

func init() {
	agentStopCmd.Flags().String("docker", "docker", "container CLI used to find and stop the sandbox (docker, podman, ...)")
	agentCmd.AddCommand(agentStopCmd)
}
