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
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/DopplerHQ/cli/pkg/configuration"
	"github.com/DopplerHQ/cli/pkg/proxy"
	"github.com/DopplerHQ/cli/pkg/utils"
	agentproxy "github.com/DopplerTest/agent-proxy"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Run AI agents against the credential proxy (experimental)",
	Args:  cobra.NoArgs,
}

// hostRun is what `agent run` resolves before it starts the command.
type hostRun struct {
	command   []string           // the program and its arguments, before the notes are added
	notes     string             // the agent notes, empty without a named agent
	config    *proxy.ProxyConfig // the agent's proxy config, empty without a named agent
	dataDir   string             // the proxy data directory, which holds ca.crt and agent.env
	startHint string             // the command that starts this run's proxy
	agentDir  string             // where the run is recorded, empty without a named agent
}

// resolveHostRun reads the run from the arguments. A first argument naming an existing
// agent selects that agent, and the rest, if any, replaces what the agent would run.
// Otherwise every argument is the command, and the proxy is the plain `proxy start` one.
func resolveHostRun(cmd *cobra.Command, args []string) hostRun {
	var agent *proxy.AgentProfile
	if len(args) > 0 && cmd.ArgsLenAtDash() != 0 {
		agent, _ = proxy.LoadAgent(configuration.UserConfigDir, args[0])
	}
	if agent == nil {
		return hostRun{
			command:   firstCommand(args),
			config:    &proxy.ProxyConfig{},
			dataDir:   proxyDataDir(),
			startHint: fmt.Sprintf("%s proxy start", cmd.Root().Name()),
		}
	}
	agentDir := proxy.AgentDir(configuration.UserConfigDir, agent.Name)
	cfg, _, err := proxy.LoadOrScaffold(proxy.AgentConfigPath(agentDir))
	if err != nil {
		utils.HandleError(err, "unable to read the agent's proxy config")
	}
	scope, _ := cfg.ResolveForConfig(agent.Config)
	return hostRun{
		command:   firstCommand(args[1:], agent.RunCommand()),
		notes:     proxy.RenderAgentNotes(scope),
		config:    scope,
		dataDir:   proxy.AgentDataDir(agentDir),
		startHint: fmt.Sprintf("%s proxy start --agent %s", cmd.Root().Name(), agent.Name),
		agentDir:  agentDir,
	}
}

// firstCommand returns the first of the commands that is not empty, or claude.
func firstCommand(commands ...[]string) []string {
	for _, command := range commands {
		if len(command) > 0 {
			return command
		}
	}
	return []string{"claude"}
}

// proxyAddress returns the host and port of the proxy that agent.env names in
// HTTPS_PROXY, or nothing when it names none.
func proxyAddress(entries []string) string {
	value := ""
	for _, entry := range entries {
		if last, ok := strings.CutPrefix(entry, "HTTPS_PROXY="); ok {
			value = last
		}
	}
	proxyURL, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return proxyURL.Host
}

// securityTool runs the security tool of macOS. Adding a trusted certificate puts a
// prompt of macOS on screen, and with nobody to answer it the tool would wait for good,
// so it gets half a minute. It is a variable so that the tests of this package put a
// stand-in here and never reach a real keychain.
var securityTool = func(args ...string) ([]byte, error) {
	return utils.CommandOutput(append([]string{"/usr/bin/security"}, args...), 30*time.Second)
}

func loginKeychain() proxy.Keychain {
	return proxy.Keychain{GOOS: runtime.GOOS, Security: securityTool}
}

// trustProxyCA has macOS trust the proxy's CA, for the tools that read no certificate
// variable there. The command runs whether or not that worked.
func trustProxyCA(caPath string, disabled bool) {
	added, err := loginKeychain().TrustCA(caPath, proxy.CATrust{
		OnTerminal: isatty.IsTerminal(os.Stdin.Fd()),
		Disabled:   disabled,
		Announce: func() {
			utils.Log("Adding the proxy's CA to your login keychain, so that tools which read the system trust store (gh, go, terraform) accept the proxy's certificates. macOS will ask you to allow it. Use --no-ca-trust to skip this step.")
		},
	})
	if err != nil {
		utils.LogWarning(fmt.Sprintf("The proxy's CA was not added to your login keychain (%v). The command still runs, and tools that read the system trust store may report a certificate error. Use --no-ca-trust to skip this step.", err))
	}
	if added {
		utils.Log("Added the proxy's CA to your login keychain. It stays trusted after this run.")
	}
}

// forgetProxyCA takes an agent's CA out of the login keychain again. The agent is
// deleted whether or not that worked.
func forgetProxyCA(caPath string) {
	_, err := loginKeychain().ForgetCA(caPath, func() {
		utils.Log("Removing the agent's CA from your login keychain. macOS may ask you to allow it.")
	})
	if err != nil {
		utils.LogWarning(fmt.Sprintf("The agent's CA may still be a trusted root in your login keychain (%v). Every agent's CA has the same name, so find this one by its SHA-256 hash: %s. Delete it there in Keychain Access.", err, proxy.CAFingerprint(caPath)))
	}
}

// record keeps the run beside the profile so the desktop app can tell runs apart, no
// matter which terminal launched this one. A failed write must not stop the run.
func (r hostRun) record(run proxy.LastRun, moment string) {
	if r.agentDir == "" {
		return
	}
	if err := proxy.WriteLastRun(r.agentDir, run); err != nil {
		utils.LogWarning(fmt.Sprintf("Unable to record the %s of this run: %v", moment, err))
	}
}

var agentRunCmd = &cobra.Command{
	Use:   "run [<agent>] [-- <command> [args...]]",
	Short: "Run an agent, or any command, on this machine through the proxy",
	Long: `Run a named agent on this machine, with its requests sent through the proxy started
for it with ` + "`proxy start --agent <name>`" + `. The agent's environment gets the proxy
address, the proxy's CA and a placeholder for each secret. Without a command after --
the agent's own program is started: codex for a codex agent, gemini for a gemini
agent, aider for an aider agent, and claude for any other. A command that an older
version stored with the agent was written for a container and is not run. Claude
Code is also given the agent notes when it starts.

On macOS, tools built with Go (gh, go, terraform) read no certificate variable and
trust only the keychain. So on a terminal the CA of the agent's proxy is added to
your login keychain as a trusted root for TLS the first time, after a prompt from
macOS. It stays trusted after the run, until the agent is deleted. --no-ca-trust
leaves the keychain alone.

Without an agent name, run the command against the proxy started with a plain
` + "`proxy start`" + `. The CA of that proxy is never added to the keychain.`,
	Example: `doppler-beta agent run backend-claude -- claude`,
	Args:    cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		run := resolveHostRun(cmd, args)
		caPath, envPath := agentproxy.CACertPath(run.dataDir), agentproxy.AgentEnvPath(run.dataDir)
		for _, p := range []string{caPath, envPath} {
			if _, err := os.Stat(p); err != nil {
				utils.HandleError(fmt.Errorf("proxy artifacts not found (%s). Start the proxy first:\n  %s", p, run.startHint))
			}
		}
		agentEnv, err := os.ReadFile(envPath)
		if err != nil {
			utils.HandleError(err, "unable to read the proxy's agent.env")
		}
		entries := proxy.ParseAgentEnv(string(agentEnv))
		// Without the proxy's address the command would send its requests straight to
		// their hosts, and report nothing wrong.
		address := proxyAddress(entries)
		if address == "" {
			utils.HandleError(fmt.Errorf("proxy address not found (%s). Restart the proxy:\n  %s", envPath, run.startHint))
		}
		// The proxy leaves its files behind when it exits, so they do not show that it
		// is running. Without it every request of the command would fail.
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			utils.HandleError(fmt.Errorf("proxy not running (%s). Start the proxy first:\n  %s", address, run.startHint))
		}
		conn.Close() // #nosec G104
		proxyCA, err := os.ReadFile(caPath)
		if err != nil {
			utils.HandleError(err, "unable to read the proxy's CA")
		}
		// The CA of the plain proxy belongs to no agent, so no agent delete would take
		// it out of the keychain again. It is never added.
		if run.agentDir != "" {
			noCATrust, _ := cmd.Flags().GetBool("no-ca-trust")
			trustProxyCA(caPath, noCATrust)
		}
		caFiles := proxy.CAFiles(os.Environ(), proxyCA, run.dataDir, os.ReadFile)
		for _, file := range caFiles {
			if err := utils.WriteFile(file.Path, file.Content, 0o600); err != nil {
				utils.HandleError(err, "unable to write the CA bundle")
			}
		}
		env := proxy.RunEnv{Base: os.Environ(), AgentEnv: entries, CAFiles: caFiles, Config: run.config}.Environ()
		command, promptFiles, err := proxy.CommandWithNotes(run.command, run.notes, run.dataDir, os.ReadFile)
		if err != nil {
			utils.LogWarning(fmt.Sprintf("Unable to read the file of --append-system-prompt-file (%v). The command runs as you wrote it, without the agent notes.", err))
		}
		for _, file := range promptFiles {
			if err := utils.WriteFile(file.Path, file.Content, 0o600); err != nil {
				utils.HandleError(err, "unable to write the agent notes")
			}
		}

		record := proxy.LastRun{StartedAt: time.Now().UTC(), Command: run.command}
		run.record(record, "start")
		// No signal ends this process, so the record is still closed when the run is
		// stopped or its terminal window closes.
		child, runErr := utils.RunCommandAsJob(command, env, os.Stdin, os.Stdout, os.Stderr)
		if runErr == nil {
			runErr = child.Wait()
		}
		ended := time.Now().UTC()
		code := proxy.ExitCodeOf(runErr)
		record.EndedAt, record.ExitCode = &ended, &code
		run.record(record, "end")

		var exit *exec.ExitError
		if runErr != nil && !errors.As(runErr, &exit) {
			utils.HandleError(runErr, "unable to run "+command[0])
		}
		// SIGTERM (143) and Ctrl-C (130) end a run on purpose, so say so.
		if proxy.IsStopExitCode(code) {
			utils.Log("Run stopped.")
		}
		os.Exit(code)
	},
}

func init() {
	agentRunCmd.Flags().Bool("no-ca-trust", false, "do not add the proxy's CA to the macOS login keychain")
	agentCmd.AddCommand(agentRunCmd)
	rootCmd.AddCommand(agentCmd)
}
