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

// envTarget is the store an env command works on: the agent named by the first
// argument, or the shared store every agent inherits when --shared is set.
type envTarget struct {
	agent string // empty for the shared store
	dir   string // the agent's directory, or the config directory for the shared store
}

func (t envTarget) shared() bool { return t.agent == "" }

func (t envTarget) set(key, value string) error {
	if t.shared() {
		return proxy.SetSharedEnv(t.dir, key, value)
	}
	return proxy.SetAgentEnv(t.dir, key, value)
}

func (t envTarget) unset(key string) error {
	if t.shared() {
		return proxy.UnsetSharedEnv(t.dir, key)
	}
	return proxy.UnsetAgentEnv(t.dir, key)
}

func (t envTarget) keys() []string {
	if t.shared() {
		return proxy.SharedEnvKeys(t.dir)
	}
	return proxy.AgentEnvKeys(t.dir)
}

// envArgs validates an env command's positional arguments: the agent name followed
// by trailing arguments, or only the trailing arguments when --shared is set.
func envArgs(trailing int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if shared, _ := cmd.Flags().GetBool("shared"); shared {
			if len(args) != trailing {
				return errors.New("--shared targets the environment every agent inherits, so leave the agent name out")
			}
			return nil
		}
		return cobra.ExactArgs(trailing+1)(cmd, args)
	}
}

// envTargetFor resolves the store from --shared or the first argument and returns
// the arguments that follow it.
func envTargetFor(cmd *cobra.Command, args []string) (envTarget, []string) {
	if shared, _ := cmd.Flags().GetBool("shared"); shared {
		return envTarget{dir: configuration.UserConfigDir}, args
	}
	p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
	if err != nil {
		utils.HandleError(err, "unable to load the agent")
	}
	return envTarget{agent: p.Name, dir: proxy.AgentDir(configuration.UserConfigDir, p.Name)}, args[1:]
}

var agentEnvSetCmd = &cobra.Command{
	Use:   "set [<agent>] KEY",
	Short: "Store a value for the agent, read from stdin so it never appears on the command line",
	Long: `Store a value that is injected into the agent's sandbox environment by value.
The value is read from stdin:

    echo "$TOKEN" | doppler-beta agent env set backend-claude CLAUDE_CODE_OAUTH_TOKEN

It is kept in a 0600 file beside the agent's profile. It is never written to Doppler
and the proxy never sees it, which is what a provider credential needs, since the
proxy tunnels provider hosts without inspection and could not swap a masked value in.

With --shared the value is stored once for every agent instead of for one:

    echo "$TOKEN" | doppler-beta agent env set --shared CLAUDE_CODE_OAUTH_TOKEN

An agent's own value for the same name wins over the shared one.`,
	Args: envArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target, rest := envTargetFor(cmd, args)
		key := rest[0]
		if err := proxy.ValidateEnvKey(key); err != nil {
			utils.HandleError(err)
		}
		hasData, err := utils.HasDataOnStdIn()
		if err != nil {
			utils.HandleError(err)
		}
		if !hasData {
			example := fmt.Sprintf("%s agent env set %s %s", cmd.Root().Name(), target.agent, key)
			if target.shared() {
				example = fmt.Sprintf("%s agent env set --shared %s", cmd.Root().Name(), key)
			}
			utils.HandleError(fmt.Errorf("pipe the value on stdin, for example: echo \"$VALUE\" | %s", example))
		}
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			utils.HandleError(err, "unable to read the value from stdin")
		}
		value := strings.TrimRight(string(raw), "\r\n")
		if value == "" {
			utils.HandleError(errors.New("no value was provided on stdin"))
		}
		if err := target.set(key, value); err != nil {
			utils.HandleError(err, "unable to store the value")
		}
		if utils.OutputJSON {
			if target.shared() {
				printJSON(map[string]any{"shared": true, "key": key, "set": true})
				return
			}
			printJSON(map[string]any{"agent": target.agent, "key": key, "set": true})
			return
		}
		if target.shared() {
			utils.Log(fmt.Sprintf("Stored %s for every agent. It is handed to the sandbox when an agent runs and never leaves this machine.", key))
			return
		}
		utils.Log(fmt.Sprintf("Stored %s for agent %s. It is handed to the sandbox when the agent runs and never leaves this machine.", key, target.agent))
	},
}

var agentEnvUnsetCmd = &cobra.Command{
	Use:   "unset [<agent>] KEY",
	Short: "Remove a stored value",
	Args:  envArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target, rest := envTargetFor(cmd, args)
		key := rest[0]
		if err := target.unset(key); err != nil {
			utils.HandleError(err, "unable to remove the value")
		}
		if utils.OutputJSON {
			if target.shared() {
				printJSON(map[string]any{"shared": true, "key": key, "set": false})
				return
			}
			printJSON(map[string]any{"agent": target.agent, "key": key, "set": false})
			return
		}
		if target.shared() {
			utils.Log(fmt.Sprintf("Removed %s from the shared environment", key))
			return
		}
		utils.Log(fmt.Sprintf("Removed %s from agent %s", key, target.agent))
	},
}

var agentEnvListCmd = &cobra.Command{
	Use:   "list [<agent>]",
	Short: "List the stored names, and for an agent the shared names it inherits. Values are never shown",
	Args:  envArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		target, _ := envTargetFor(cmd, args)
		keys := target.keys()
		if keys == nil {
			keys = []string{}
		}
		if target.shared() {
			if utils.OutputJSON {
				printJSON(map[string]any{"shared": true, "keys": keys})
				return
			}
			if len(keys) == 0 {
				utils.Log("No shared values stored")
				return
			}
			for _, k := range keys {
				utils.Print(k)
			}
			return
		}
		sharedKeys := proxy.SharedEnvKeys(configuration.UserConfigDir)
		if sharedKeys == nil {
			sharedKeys = []string{}
		}
		if utils.OutputJSON {
			printJSON(map[string]any{"agent": target.agent, "keys": keys, "shared_keys": sharedKeys})
			return
		}
		if len(keys) == 0 {
			utils.Log("No values stored for agent " + target.agent)
		}
		for _, k := range keys {
			utils.Print(k)
		}
		if len(sharedKeys) == 0 {
			return
		}
		utils.Print("Shared with every agent:")
		for _, k := range sharedKeys {
			utils.Print(k)
		}
	},
}

func init() {
	for _, c := range []*cobra.Command{agentEnvSetCmd, agentEnvUnsetCmd, agentEnvListCmd} {
		c.Flags().Bool("shared", false, "target the environment every agent inherits instead of one agent's")
	}
	agentEnvCmd.AddCommand(agentEnvSetCmd)
	agentEnvCmd.AddCommand(agentEnvUnsetCmd)
	agentEnvCmd.AddCommand(agentEnvListCmd)
	agentCmd.AddCommand(agentEnvCmd)
}
