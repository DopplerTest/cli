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
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/DopplerHQ/cli/pkg/configuration"
	"github.com/DopplerHQ/cli/pkg/http"
	"github.com/DopplerHQ/cli/pkg/models"
	"github.com/DopplerHQ/cli/pkg/proxy"
	"github.com/DopplerHQ/cli/pkg/utils"
	agentproxy "github.com/DopplerTest/agent-proxy"
	"github.com/DopplerTest/agent-proxy/sandbox"
	"github.com/spf13/cobra"
)

// agentView is what `agent list` and `agent show` print with --json: the profile
// plus the paths the desktop app and scripts need.
type agentView struct {
	proxy.AgentProfile
	Dir           string `json:"dir"`
	ConfigPath    string `json:"config_path"`
	DataDir       string `json:"data_dir"`
	AgentEnvPath  string `json:"agent_env_path"`
	CAPath        string `json:"ca_path"`
	HasToken      bool   `json:"has_token"`
	ListenAddress string `json:"listen_address"`
	// EnvKeys are the names in the agent's local environment (provider credentials). Values are never exposed.
	EnvKeys []string `json:"env_keys"`
	// LastRun is nil until the agent has been run at least once.
	LastRun *proxy.LastRun `json:"last_run"`
}

func viewOf(p proxy.AgentProfile) agentView {
	dir := proxy.AgentDir(configuration.UserConfigDir, p.Name)
	data := proxy.AgentDataDir(dir)
	envKeys := proxy.AgentEnvKeys(dir)
	if envKeys == nil {
		envKeys = []string{}
	}
	// A corrupt record reads as "never run" rather than breaking the whole listing.
	lastRun, _ := proxy.ReadLastRun(dir)
	return agentView{
		AgentProfile:  p,
		Dir:           dir,
		ConfigPath:    proxy.AgentConfigPath(dir),
		DataDir:       data,
		AgentEnvPath:  agentproxy.AgentEnvPath(data),
		CAPath:        agentproxy.CACertPath(data),
		HasToken:      proxy.ReadAgentToken(dir) != "",
		ListenAddress: proxy.ReadListenAddress(proxy.AgentConfigPath(dir)),
		EnvKeys:       envKeys,
		LastRun:       lastRun,
	}
}

func printJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		utils.HandleError(err, "unable to encode json")
	}
	fmt.Println(string(data))
}

// agentScopedConfig is the CLI scope an agent's proxy runs with: its own service
// token and its project and config, never the personal token.
func agentScopedConfig(cmd *cobra.Command, p *proxy.AgentProfile) models.ScopedOptions {
	lc := configuration.LocalConfig(cmd)
	dir := proxy.AgentDir(configuration.UserConfigDir, p.Name)
	token := proxy.ReadAgentToken(dir)
	if token == "" {
		utils.HandleError(fmt.Errorf("agent %s has no service token. Recreate it with `%s agent create`", p.Name, cmd.Root().Name()))
	}
	lc.Token = models.ScopedOption{Value: token, Scope: "/", Source: "agent " + p.Name}
	lc.EnclaveProject = models.ScopedOption{Value: p.Project, Scope: "/", Source: "agent " + p.Name}
	lc.EnclaveConfig = models.ScopedOption{Value: p.Config, Scope: "/", Source: "agent " + p.Name}
	return lc
}

var agentCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a named agent: a project and config, a service token minted for it, a command, and the directories it can see",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		if err := proxy.ValidateAgentName(name); err != nil {
			utils.HandleError(err)
		}
		if _, err := proxy.LoadAgent(configuration.UserConfigDir, name); err == nil {
			utils.HandleError(fmt.Errorf("agent %s already exists", name))
		}

		localConfig := configuration.LocalConfig(cmd)
		utils.RequireValue("token", localConfig.Token.Value)
		project := cmd.Flag("project").Value.String()
		config := cmd.Flag("config").Value.String()
		if project == "" {
			project = localConfig.EnclaveProject.Value
		}
		if config == "" {
			config = localConfig.EnclaveConfig.Value
		}
		if project == "" || config == "" {
			utils.HandleError(errors.New("pass --project and --config, or run this in a directory set up with `doppler-beta setup`"))
		}

		command, _ := cmd.Flags().GetString("command")
		mountFlags, _ := cmd.Flags().GetStringArray("mount")
		var mounts []string
		for _, m := range mountFlags {
			host, ro, err := proxy.ParseMount(m)
			if err != nil {
				utils.HandleError(err, "invalid --mount")
			}
			if ro {
				host += ":ro"
			}
			mounts = append(mounts, host)
		}

		hostname, _ := os.Hostname()
		tokenName := fmt.Sprintf("agent-proxy %s (%s)", name, hostname)
		verifyTLS := utils.GetBool(localConfig.VerifyTLS.Value, true)
		minted, herr := http.CreateConfigServiceToken(localConfig.APIHost.Value, verifyTLS, localConfig.Token.Value, project, config, tokenName, agentTokenExpiry(cmd), "read")
		if !herr.IsNil() {
			utils.HandleError(herr.Unwrap(), "unable to mint a service token for the agent")
		}
		if minted.Token == "" {
			utils.HandleError(errors.New("the API returned no token value"), "unable to mint a service token for the agent")
		}

		p := &proxy.AgentProfile{
			Name:           name,
			Project:        project,
			Config:         config,
			Command:        strings.Fields(command),
			Mounts:         mounts,
			TokenSlug:      minted.Slug,
			TokenName:      minted.Name,
			TokenExpiresAt: minted.ExpiresAt,
			CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		}
		if err := proxy.SaveAgent(configuration.UserConfigDir, p); err != nil {
			utils.HandleError(err, "unable to save the agent")
		}
		dir := proxy.AgentDir(configuration.UserConfigDir, name)
		if err := proxy.WriteAgentToken(dir, minted.Token); err != nil {
			utils.HandleError(err, "unable to store the agent's token")
		}
		port := proxy.NextAgentPort(configuration.UserConfigDir)
		if _, _, err := proxy.LoadOrScaffoldWith(proxy.AgentConfigPath(dir), proxy.ProviderHosts(p.Command)); err != nil {
			utils.HandleError(err, "unable to scaffold the agent's proxy config")
		}
		if err := proxy.SetListenAddress(proxy.AgentConfigPath(dir), fmt.Sprintf("0.0.0.0:%d", port)); err != nil {
			utils.HandleError(err, "unable to set the agent's listen address")
		}

		if utils.OutputJSON {
			printJSON(viewOf(*p))
			return
		}
		utils.Log(fmt.Sprintf("Created agent %s for %s/%s with service token %s", name, project, config, minted.Slug))
		utils.Log(fmt.Sprintf("Proxy config: %s", proxy.AgentConfigPath(dir)))
		utils.Log(fmt.Sprintf("Next: `%s proxy start --agent %s`, then `%s agent run %s`", cmd.Root().Name(), name, cmd.Root().Name(), name))
	},
}

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agents",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		agents, err := proxy.ListAgents(configuration.UserConfigDir)
		if err != nil {
			utils.HandleError(err, "unable to list agents")
		}
		if utils.OutputJSON {
			views := make([]agentView, 0, len(agents))
			for _, a := range agents {
				views = append(views, viewOf(a))
			}
			printJSON(views)
			return
		}
		if len(agents) == 0 {
			utils.Log("No agents yet. Create one with `" + cmd.Root().Name() + " agent create <name> -p <project> -c <config>`")
			return
		}
		for _, a := range agents {
			utils.Print(fmt.Sprintf("%s\t%s/%s\t%s", a.Name, a.Project, a.Config, strings.Join(a.Command, " ")))
		}
	},
}

var agentShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show one agent",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		printJSON(viewOf(*p))
	},
}

var agentDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an agent and revoke its service token",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		p, err := proxy.LoadAgent(configuration.UserConfigDir, name)
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		keepToken, _ := cmd.Flags().GetBool("keep-token")
		if !keepToken && p.TokenSlug != "" {
			localConfig := configuration.LocalConfig(cmd)
			utils.RequireValue("token", localConfig.Token.Value)
			verifyTLS := utils.GetBool(localConfig.VerifyTLS.Value, true)
			if herr := http.DeleteConfigServiceToken(localConfig.APIHost.Value, verifyTLS, localConfig.Token.Value, p.Project, p.Config, p.TokenSlug, ""); !herr.IsNil() {
				utils.HandleError(herr.Unwrap(), "unable to revoke the agent's service token (pass --keep-token to delete the agent anyway)")
			}
		}
		if err := os.RemoveAll(proxy.AgentDir(configuration.UserConfigDir, name)); err != nil {
			utils.HandleError(err, "unable to remove the agent directory")
		}
		// The home volume can hold a Claude Code login synced in by `agent login-sync`, and
		// its name is derived from the agent name, so a later agent of the same name would
		// silently reuse it. If removal fails (a sandbox is still running, or a different
		// container CLI), warn with the manual cleanup rather than leaving it a silent orphan.
		dockerBin, _ := cmd.Flags().GetString("docker")
		volume := proxy.AgentHomeVolume(name)
		homeVolumeRemoved := true
		if rmErr := sandbox.RemoveHomeVolume(context.Background(), dockerBin, volume); rmErr != nil {
			homeVolumeRemoved = false
			utils.PrintWarning(fmt.Sprintf("Removed the agent, but its home volume %q may remain (it can hold a synced login). Remove it with `%s volume rm %s`.", volume, dockerBin, volume))
		}
		if utils.OutputJSON {
			printJSON(map[string]any{"deleted": name, "token_revoked": !keepToken && p.TokenSlug != "", "home_volume_removed": homeVolumeRemoved})
			return
		}
		utils.Log("Deleted agent " + name)
	},
}

// agentTokenExpiry turns the --max-age flag into the absolute expiry the API wants:
// now + the duration, or the zero time (no expiry) when the flag is unset or 0. This
// mirrors `doppler configs tokens create`.
func agentTokenExpiry(cmd *cobra.Command) time.Time {
	maxAge := utils.GetDurationFlagIfChanged(cmd, "max-age", 0)
	if maxAge == 0 {
		return time.Time{}
	}
	return time.Now().Add(maxAge)
}

// rotateAgentToken mints a fresh token, writes it and updates the profile, then
// revokes the old one. The new token is written and saved before the old one is
// revoked, so a failure along the way never leaves the agent with no usable token.
func rotateAgentToken(configDir string, p *proxy.AgentProfile, mint func() (models.ConfigServiceToken, error), revoke func(slug string) error) error {
	minted, err := mint()
	if err != nil {
		return err
	}
	if minted.Token == "" {
		return errors.New("the API returned no token value")
	}
	oldSlug := p.TokenSlug
	dir := proxy.AgentDir(configDir, p.Name)
	if err := proxy.WriteAgentToken(dir, minted.Token); err != nil {
		return err
	}
	p.TokenSlug = minted.Slug
	p.TokenName = minted.Name
	p.TokenExpiresAt = minted.ExpiresAt
	if err := proxy.SaveAgent(configDir, p); err != nil {
		return err
	}
	if oldSlug != "" && oldSlug != minted.Slug {
		if err := revoke(oldSlug); err != nil {
			return fmt.Errorf("minted a new token but could not revoke the old one (%s), revoke it by hand: %w", oldSlug, err)
		}
	}
	return nil
}

var agentRotateCmd = &cobra.Command{
	Use:   "rotate <name>",
	Short: "Mint a fresh service token for an agent and revoke the old one",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		localConfig := configuration.LocalConfig(cmd)
		utils.RequireValue("token", localConfig.Token.Value)
		verifyTLS := utils.GetBool(localConfig.VerifyTLS.Value, true)
		hostname, _ := os.Hostname()
		tokenName := fmt.Sprintf("agent-proxy %s (%s)", p.Name, hostname)
		expireAt := agentTokenExpiry(cmd)

		mint := func() (models.ConfigServiceToken, error) {
			m, herr := http.CreateConfigServiceToken(localConfig.APIHost.Value, verifyTLS, localConfig.Token.Value, p.Project, p.Config, tokenName, expireAt, "read")
			return m, httpErr(herr)
		}
		revoke := func(slug string) error {
			return httpErr(http.DeleteConfigServiceToken(localConfig.APIHost.Value, verifyTLS, localConfig.Token.Value, p.Project, p.Config, slug, ""))
		}
		if err := rotateAgentToken(configuration.UserConfigDir, p, mint, revoke); err != nil {
			utils.HandleError(err, "unable to rotate the agent's token")
		}

		if utils.OutputJSON {
			printJSON(viewOf(*p))
			return
		}
		utils.Log(fmt.Sprintf("Rotated the service token for agent %s (new token %s)", p.Name, p.TokenSlug))
		utils.Log(fmt.Sprintf("Restart its proxy to pick up the new token: `%s proxy start --agent %s`", cmd.Root().Name(), p.Name))
	},
}

// httpErr adapts an http.Error to a plain error, preserving the message even when
// the wrapped error is nil.
func httpErr(herr http.Error) error {
	if herr.IsNil() {
		return nil
	}
	if e := herr.Unwrap(); e != nil {
		return e
	}
	return errors.New(herr.Message)
}

func init() {
	agentCreateCmd.Flags().StringP("project", "p", "", "Doppler project the agent reads")
	agentCreateCmd.Flags().StringP("config", "c", "", "Doppler config the agent reads")
	agentCreateCmd.Flags().String("command", "claude", "command the sandbox runs")
	agentCreateCmd.Flags().StringArray("mount", nil, "host directory mounted at /workspace/<basename>; repeat for more, append :ro for read-only")
	agentCreateCmd.Flags().Duration("max-age", 0, "service token expires after this duration (e.g. '720h'); 0 means no expiry")
	agentRotateCmd.Flags().Duration("max-age", 0, "service token expires after this duration (e.g. '720h'); 0 means no expiry")
	agentDeleteCmd.Flags().Bool("keep-token", false, "leave the service token in place")
	agentDeleteCmd.Flags().String("docker", "docker", "container CLI used to remove the agent's home volume (docker, podman, ...)")
	agentCmd.AddCommand(agentCreateCmd)
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentShowCmd)
	agentCmd.AddCommand(agentRotateCmd)
	agentCmd.AddCommand(agentDeleteCmd)
}

// hostClaudeCredentials returns the Claude Code OAuth credential stored on this
// machine: the macOS keychain item on darwin, ~/.claude/.credentials.json elsewhere.
func hostClaudeCredentials() ([]byte, string, error) {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
		if err != nil {
			return nil, "", errors.New("no Claude Code login in the macOS keychain. Run `claude` on this machine and log in first")
		}
		return bytes.TrimSpace(out), "macOS keychain", nil
	}
	path := filepath.Join(utils.HomeDir(), ".claude", ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("no Claude Code login at %s. Run `claude` on this machine and log in first", path)
	}
	return bytes.TrimSpace(data), path, nil
}

// claudeHomeTar packs the credential (and the host's settings.json when present)
// as a tar stream rooted at the sandbox user's home.
func claudeHomeTar(creds []byte) ([]byte, []string, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	var copied []string
	add := func(name string, data []byte, mode int64) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Now()}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := add(".claude/.credentials.json", creds, 0o600); err != nil {
		return nil, nil, err
	}
	copied = append(copied, ".claude/.credentials.json")
	if settings, err := os.ReadFile(filepath.Join(utils.HomeDir(), ".claude", "settings.json")); err == nil && json.Valid(settings) {
		if err := add(".claude/settings.json", settings, 0o600); err != nil {
			return nil, nil, err
		}
		copied = append(copied, ".claude/settings.json")
	}
	// ~/.claude.json carries the onboarding flag and the account the login belongs
	// to. Without it Claude Code treats the sandbox as a first run. Host-specific
	// entries (project history, MCP servers with host paths) stay behind.
	if state, err := os.ReadFile(filepath.Join(utils.HomeDir(), ".claude.json")); err == nil {
		var m map[string]any
		if json.Unmarshal(state, &m) == nil {
			for _, k := range []string{"projects", "mcpServers", "claudeCodeFirstTokenDate", "cachedChangelog"} {
				delete(m, k)
			}
			m["hasCompletedOnboarding"] = true
			if trimmed, err := json.Marshal(m); err == nil {
				if err := add(".claude.json", trimmed, 0o600); err != nil {
					return nil, nil, err
				}
				copied = append(copied, ".claude.json")
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), copied, nil
}

var agentLoginSyncCmd = &cobra.Command{
	Use:   "login-sync <name>",
	Short: "Copy this machine's Claude Code login into the agent's sandbox home, so the agent starts logged in",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		p, err := proxy.LoadAgent(configuration.UserConfigDir, args[0])
		if err != nil {
			utils.HandleError(err, "unable to load the agent")
		}
		dockerBin, _ := cmd.Flags().GetString("docker")
		creds, source, err := hostClaudeCredentials()
		if err != nil {
			utils.HandleError(err)
		}
		if !json.Valid(creds) {
			utils.HandleError(errors.New("the stored Claude Code credential is not valid JSON"))
		}
		archive, copied, err := claudeHomeTar(creds)
		if err != nil {
			utils.HandleError(err, "unable to pack the credential")
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		cfg := sandbox.Config{DockerBin: dockerBin}
		if err := sandbox.EnsureImage(ctx, cfg); err != nil {
			utils.HandleError(err, "failed to prepare the sandbox image")
		}
		volume := proxy.AgentHomeVolume(p.Name)
		script := "umask 077 && mkdir -p /home/agent/.claude && tar -C /home/agent -xf - && chown -R agent:agent /home/agent/.claude /home/agent/.claude.json 2>/dev/null; chown agent:agent /home/agent/.claude && chmod 700 /home/agent/.claude"
		run := exec.CommandContext(ctx, dockerBin, "run", "--rm", "-i", "--entrypoint", "/bin/sh", "-v", volume+":/home/agent", sandbox.DefaultImage, "-c", script)
		run.Stdin = bytes.NewReader(archive)
		var stderr bytes.Buffer
		run.Stderr = &stderr
		if err := run.Run(); err != nil {
			utils.HandleError(fmt.Errorf("%s", strings.TrimSpace(stderr.String())), "unable to write into the agent's home volume")
		}
		if utils.OutputJSON {
			printJSON(map[string]any{"agent": p.Name, "source": source, "volume": volume, "copied": copied})
			return
		}
		utils.Log(fmt.Sprintf("Copied your Claude Code login (%s) into %s: %s", source, volume, strings.Join(copied, ", ")))
	},
}

func init() {
	agentLoginSyncCmd.Flags().String("docker", "docker", "container CLI to use (docker, podman, ...)")
	agentCmd.AddCommand(agentLoginSyncCmd)
}
