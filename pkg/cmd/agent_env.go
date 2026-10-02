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
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DopplerHQ/cli/pkg/configuration"
	"github.com/DopplerHQ/cli/pkg/proxy"
	"github.com/DopplerHQ/cli/pkg/utils"
	"github.com/spf13/cobra"
)

var agentEnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Values injected into an agent's sandbox by value, such as its provider credential. Stored on this machine, never in Doppler",
	Args:  cobra.NoArgs,
}

var agentEnvSetCmd = &cobra.Command{
	Use:   "set <agent> KEY",
	Short: "Store a value for the agent, read from stdin so it never appears on the command line",
	Long: `Store a value that is injected into the agent's sandbox environment by value.
The value is read from stdin:

    echo "$TOKEN" | doppler-beta agent env set backend-claude CLAUDE_CODE_OAUTH_TOKEN

It is kept in a 0600 file beside the agent's profile. It is never written to Doppler
and the proxy never sees it, which is what a provider credential needs, since the
proxy tunnels provider hosts without inspection and could not swap a masked value in.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		key := args[1]
		if err := proxy.ValidateEnvKey(key); err != nil {
			utils.HandleError(err)
		}
		hasData, err := utils.HasDataOnStdIn()
		if err != nil {
			utils.HandleError(err)
		}
		if !hasData {
			utils.HandleError(fmt.Errorf("pipe the value on stdin, for example: echo \"$VALUE\" | %s agent env set %s %s", cmd.Root().Name(), p.Name, key))
		}
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			utils.HandleError(err, "unable to read the value from stdin")
		}
		value := strings.TrimRight(string(raw), "\r\n")
		if value == "" {
			utils.HandleError(errors.New("no value was provided on stdin"))
		}
		if err := proxy.SetAgentEnv(proxy.AgentDir(configuration.UserConfigDir, p.Name), key, value); err != nil {
			utils.HandleError(err, "unable to store the value")
		}
		if utils.OutputJSON {
			printJSON(map[string]any{"agent": p.Name, "key": key, "set": true})
			return
		}
		utils.Log(fmt.Sprintf("Stored %s for agent %s. It is handed to the sandbox when the agent runs and never leaves this machine.", key, p.Name))
	},
}

var agentEnvUnsetCmd = &cobra.Command{
	Use:   "unset <agent> KEY",
	Short: "Remove a stored value",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		if err := proxy.UnsetAgentEnv(proxy.AgentDir(configuration.UserConfigDir, p.Name), args[1]); err != nil {
			utils.HandleError(err, "unable to remove the value")
		}
		if utils.OutputJSON {
			printJSON(map[string]any{"agent": p.Name, "key": args[1], "set": false})
			return
		}
		utils.Log(fmt.Sprintf("Removed %s from agent %s", args[1], p.Name))
	},
}

var agentEnvListCmd = &cobra.Command{
	Use:   "list <agent>",
	Short: "List the stored names. Values are never shown",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		keys := proxy.AgentEnvKeys(proxy.AgentDir(configuration.UserConfigDir, p.Name))
		if keys == nil {
			keys = []string{}
		}
		if utils.OutputJSON {
			printJSON(map[string]any{"agent": p.Name, "keys": keys})
			return
		}
		if len(keys) == 0 {
			utils.Log("No values stored for agent " + p.Name)
			return
		}
		for _, k := range keys {
			utils.Print(k)
		}
	},
}

func init() {
	agentEnvCmd.AddCommand(agentEnvSetCmd)
	agentEnvCmd.AddCommand(agentEnvUnsetCmd)
	agentEnvCmd.AddCommand(agentEnvListCmd)
	agentCmd.AddCommand(agentEnvCmd)
}
