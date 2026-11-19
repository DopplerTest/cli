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
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DopplerHQ/cli/pkg/configuration"
	"github.com/DopplerHQ/cli/pkg/proxy"
	"github.com/DopplerHQ/cli/pkg/utils"
	"github.com/DopplerHQ/cli/pkg/version"
	agentproxy "github.com/DopplerTest/agent-proxy"
	"github.com/DopplerTest/agent-proxy/enforce"
	"github.com/DopplerTest/agent-proxy/verify"
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
// agent selects that agent, and the rest, if any, replaces the agent's own command.
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
		command:   firstCommand(args[1:], agent.Command),
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
address, the proxy's CA and a placeholder for each secret. A command after -- replaces
the agent's own. With neither, the command is claude. Claude Code is also given the
agent notes when it starts.

Without an agent name, run the command against the proxy started with a plain
` + "`proxy start`" + `.`,
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

// agentDoctorCmd verifies the sandbox contract for the environment it's run in.
// It is the same verifier the enforced paths invoke internally as a preflight;
// as a standalone command it doubles as a diagnostic ("why can't the agent reach
// GitHub?"). Run it AS the agent — same user, network, and env the agent gets.
var agentDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Verify the sandbox contract (egress containment, CA trust, privilege, hygiene)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		enforced, _ := cmd.Flags().GetBool("enforced")
		strictDNS, _ := cmd.Flags().GetBool("strict-dns")
		testURL, _ := cmd.Flags().GetString("test-url")

		// The proxy the agent is meant to use: its HTTPS_PROXY, falling back to
		// the default listen address.
		proxyURL, _ := cmd.Flags().GetString("proxy")
		if proxyURL == "" {
			if v := firstEnv("HTTPS_PROXY", "https_proxy"); v != "" {
				proxyURL = v
			} else {
				proxyURL = "http://127.0.0.1:14322"
			}
		}

		// The proxy CA: prefer an explicit flag, then the vars the agent trusts,
		// then the default on-disk location.
		caPath, _ := cmd.Flags().GetString("ca")
		if caPath == "" {
			if v := firstEnv("NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "SSL_CERT_FILE"); v != "" {
				caPath = v
			} else {
				caPath = agentproxy.CACertPath(proxyDataDir())
			}
		}

		report := verify.Doctor{Enforced: enforced, Checks: agentChecks(proxyURL, caPath, strictDNS, testURL, resolveCredentialSources(cmd))}.Run()
		report.Render(os.Stdout)
		os.Exit(report.ExitCode())
	},
}

// agentChecks is the standard contract check-list, shared by `agent doctor` and
// the preflight `agent enforce` runs before launching the agent — so both assert
// exactly the same contract.
// egressProbeTargets are the IP:port literals clause 1 proves are directly
// unreachable from the agent. They span both IP families on purpose: a
// shared-box lock that only writes iptables rules leaves the agent's IPv6
// egress wide open wherever the container has an IPv6 route, so an IPv4-only
// probe list reports "contained" on a box that isn't. The list mixes external
// routes (the agent must not reach the internet directly) with an IPv6 loopback
// service port (a `::1` Postgres or the like is egress the lock must also cut,
// and netfilter's IPv4 chain never sees it). Every entry is an IP literal, never
// a hostname — a blocked resolver would make a hostname dial fail at resolution
// and falsely look contained.
var egressProbeTargets = []string{
	"1.1.1.1:443",                // IPv4 external
	"8.8.8.8:443",                // IPv4 external
	"1.1.1.1:80",                 // IPv4 external (plaintext)
	"[2606:4700:4700::1111]:443", // IPv6 external — an IPv4-only iptables lock never covers this
	"[::1]:5432",                 // IPv6 loopback — a local service (e.g. Postgres) the agent must not reach
}

func agentChecks(proxyURL, caPath string, strictDNS bool, testURL string, credentialSources []string) []verify.Check {
	// clause 1 — egress containment (adversarial: dial by IP literal, both families)
	var checks []verify.Check
	for _, addr := range egressProbeTargets {
		checks = append(checks, verify.EgressBlockedTCP(addr))
	}
	checks = append(checks,
		verify.EgressDNS("8.8.8.8:53", strictDNS),
		// proxy reachability
		verify.ProxyReachable(proxyURL),
		// clause 3 — CA trust
		verify.CACertValid(caPath),
		verify.CATrustEnv(),
		verify.CAEndToEnd(proxyURL, testURL),
	)
	// clause 2 — privilege
	checks = append(checks, privilegeChecks()...)
	// credential hygiene (Doppler-specific)
	checks = append(checks,
		verify.EnvAbsent("DOPPLER_TOKEN"),
		verify.EnvNoTokenShapes("real token shapes", "dp.st.", "dp.pt."),
	)
	// masking only holds while the agent cannot read the brokered secrets off disk
	for _, p := range credentialSources {
		checks = append(checks, verify.FileUnreadable("agent cannot read "+p, p))
	}
	return checks
}

// privilegeChecks proves clause 2: the agent runs unprivileged AND cannot regain
// the capability it would need to unlock its own egress. A clean effective set
// (NetAdminAbsent) is not enough on its own — while CAP_NET_ADMIN remains in the
// bounding set, a file-capability or setuid binary can hand it back — so the
// bounding set must be clean too (NetAdminNotAcquirable, ENG-9749).
func privilegeChecks() []verify.Check {
	return []verify.Check{
		verify.UIDNotRoot(),
		verify.NetAdminAbsent(),
		verify.NetAdminNotAcquirable(),
	}
}

// developerHome is the home of the person whose secrets the proxy brokers: the
// user behind sudo under `agent enforce`, otherwise the current user.
func developerHome() string {
	if dev := os.Getenv("SUDO_USER"); dev != "" {
		if u, err := user.Lookup(dev); err == nil {
			return u.HomeDir
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// credentialSources are the files holding what the proxy brokers on the agent's
// behalf: the developer's Doppler config and the proxy CA key. Files rather
// than their directories, since a directory the agent cannot list still lets it
// open a file inside by name.
func credentialSources(devHome, dataDir string) []string {
	var out []string
	if devHome != "" {
		out = append(out, filepath.Join(devHome, version.ConfigDirName, version.ConfigFileName))
	}
	if dataDir != "" {
		out = append(out, filepath.Join(dataDir, "ca.key"))
	}
	return out
}

// dataDirUnder is proxyDataDir for another user's home. --proxy-data-dir covers a
// --config-dir override.
func dataDirUnder(home string) string {
	return filepath.Join(home, version.ConfigDirName, "proxy")
}

// agentEnforceCmd installs the sandbox contract IN PLACE — inside a box the user
// already has (a devcontainer, a VM) — then runs the agent. It locks the agent's
// egress to only the proxy, drops to an unprivileged user, runs the doctor
// preflight, and execs the command. Must be run as root (e.g. via sudo, or from
// a devcontainer feature's init). Linux only.
var agentEnforceCmd = &cobra.Command{
	Use:   "enforce -- <command>",
	Short: "Lock egress to the proxy in place, drop privileges, and run the agent (Linux, root)",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		strategyName, _ := cmd.Flags().GetString("strategy")
		agentUser, _ := cmd.Flags().GetString("agent-user")
		proxyHost, _ := cmd.Flags().GetString("proxy-host")
		proxyPort, _ := cmd.Flags().GetInt("proxy-port")
		strictDNS, _ := cmd.Flags().GetBool("strict-dns")
		testURL, _ := cmd.Flags().GetString("test-url")

		var strat enforce.Strategy
		switch strategyName {
		case "owned-container":
			strat = enforce.OwnedContainer{}
		case "shared-box", "":
			strat = enforce.SharedBox{}
		default:
			utils.HandleError(fmt.Errorf("unknown strategy %q (want owned-container or shared-box)", strategyName))
		}

		// Resolve the unprivileged agent user we'll drop to.
		u, err := user.Lookup(agentUser)
		if err != nil {
			utils.HandleError(fmt.Errorf("agent user %q not found: %w. Create it (the devcontainer feature does this) or pass --agent-user", agentUser, err))
		}
		uid, gid, groups := resolveUser(u)

		// The firewall rule needs an IP; the proxy env keeps the host name.
		proxyIP := proxyHost
		if net.ParseIP(proxyHost) == nil {
			ips, err := net.LookupHost(proxyHost)
			if err != nil || len(ips) == 0 {
				utils.HandleError(fmt.Errorf("could not resolve proxy host %q: %w", proxyHost, err))
			}
			proxyIP = ips[0]
		}

		// CA path: flag, else default on-disk location.
		caPath, _ := cmd.Flags().GetString("ca")
		if caPath == "" {
			caPath = agentproxy.CACertPath(proxyDataDir())
		}

		// Build the agent env from the proxy's agent.env, repointing the proxy and
		// CA vars at this boundary and stripping anything the agent must not hold.
		envPath, _ := cmd.Flags().GetString("agent-env")
		if envPath == "" {
			envPath = agentproxy.AgentEnvPath(proxyDataDir())
		}
		rawEnv, err := os.ReadFile(envPath)
		if err != nil {
			utils.HandleError(fmt.Errorf("reading agent env %s: %w. Start the proxy first", envPath, err))
		}
		// Keep the per-run proxy token (userinfo) from agent.env's HTTPS_PROXY and
		// repoint only the host at this boundary. Dropping it would hand the agent a
		// credential-less proxy URL and every request would get a 407.
		proxyURL := fmt.Sprintf("http://%s%s:%d", proxyUserinfo(rawEnv), proxyHost, proxyPort)
		overrides := map[string]string{
			"HTTPS_PROXY":         proxyURL,
			"HTTP_PROXY":          proxyURL,
			"NODE_EXTRA_CA_CERTS": caPath,
			"CURL_CA_BUNDLE":      caPath,
			"SSL_CERT_FILE":       caPath,
			// git and Python's requests honor their own CA vars, not the three above;
			// without these, git over HTTPS to an intercepted host fails in the enforced
			// box now that enforce no longer installs the CA into the system trust store.
			"GIT_SSL_CAINFO":     caPath,
			"REQUESTS_CA_BUNDLE": caPath,
			// Enforce clears the environment before exec, so the essential process
			// vars for the dropped-privilege agent must be set explicitly.
			"HOME":    u.HomeDir,
			"USER":    agentUser,
			"LOGNAME": agentUser,
			"PATH":    envOr("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"),
			"TERM":    envOr("TERM", "xterm"),
		}
		env := enforce.ParseAgentEnv(string(rawEnv))
		env = enforce.OverrideEnv(env, overrides)
		env = enforce.RemoveEnv(env, "DOPPLER_TOKEN", "NO_PROXY", "no_proxy")

		// Resolved here, while SUDO_USER is still in the environment; Enforce clears
		// the environment before the preflight runs as the agent.
		sources := resolveCredentialSources(cmd)

		// The preflight is the same contract doctor asserts, run as the agent user
		// after the lock. It fails the launch if the sandbox isn't sound.
		preflight := func() error {
			rep := verify.Doctor{Enforced: true, Checks: agentChecks(proxyURL, caPath, strictDNS, testURL, sources)}.Run()
			rep.Render(os.Stderr)
			if rep.Failed() {
				return errors.New("sandbox contract check failed; refusing to launch the agent")
			}
			return nil
		}

		// No CA path is passed: Enforce no longer installs a system-trust CA (ENG-9745);
		// the agent env's CA vars carry that trust instead.
		err = enforce.Enforce(enforce.Config{
			Strategy:    strat,
			Params:      enforce.Params{ProxyIP: proxyIP, ProxyPort: proxyPort, AgentUID: uid},
			AgentUID:    uid,
			AgentGID:    gid,
			AgentGroups: groups,
			Env:         env,
			Command:     args,
			Preflight:   preflight,
			Logf:        func(f string, a ...any) { utils.Log(fmt.Sprintf(f, a...)) },
		})
		if err != nil {
			utils.HandleError(err, "enforce failed")
		}
	},
}

// resolveCredentialSources reads the developer's home and the proxy data dir
// from the current environment and flags.
func resolveCredentialSources(cmd *cobra.Command) []string {
	devHome := developerHome()
	dataDir, _ := cmd.Flags().GetString("proxy-data-dir")
	if dataDir == "" {
		dataDir = dataDirUnder(devHome)
	}
	return credentialSources(devHome, dataDir)
}

// resolveUser turns an os/user.User into numeric uid/gid and supplementary gids.
func resolveUser(u *user.User) (uid, gid int, groups []int) {
	uid, _ = strconv.Atoi(u.Uid)
	gid, _ = strconv.Atoi(u.Gid)
	if gidStrs, err := u.GroupIds(); err == nil {
		for _, g := range gidStrs {
			if n, err := strconv.Atoi(g); err == nil {
				groups = append(groups, n)
			}
		}
	}
	if len(groups) == 0 {
		groups = []int{gid}
	}
	return uid, gid, groups
}

// firstEnv returns the first non-empty value among the given env var names.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// envOr returns the env var's value, or fallback if it's unset/empty.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// proxyUserinfo returns the "user:pass@" prefix from the agent env's HTTPS_PROXY
// (the per-run proxy token), or "" if none. Used so `agent enforce` keeps the
// credential when it repoints the proxy host, instead of dropping it.
func proxyUserinfo(rawEnv []byte) string {
	for _, line := range strings.Split(string(rawEnv), "\n") {
		line = strings.TrimSpace(line)
		v, ok := strings.CutPrefix(line, "HTTPS_PROXY=")
		if !ok {
			v, ok = strings.CutPrefix(line, "HTTP_PROXY=")
		}
		if !ok {
			continue
		}
		v = strings.Trim(v, `'"`) // agent.env shell-quotes values
		if u, err := url.Parse(v); err == nil && u.User != nil {
			return u.User.String() + "@"
		}
	}
	return ""
}

func init() {
	agentCmd.AddCommand(agentRunCmd)

	agentDoctorCmd.Flags().Bool("enforced", false, "assert the full contract: an egress-containment failure is fatal")
	agentDoctorCmd.Flags().Bool("strict-dns", false, "treat an open external DNS resolver as a failure, not a warning")
	agentDoctorCmd.Flags().String("proxy", "", "proxy URL the agent should use (default $HTTPS_PROXY or http://127.0.0.1:14322)")
	agentDoctorCmd.Flags().String("ca", "", "proxy CA cert path (default $NODE_EXTRA_CA_CERTS or <data-dir>/ca.crt)")
	agentDoctorCmd.Flags().String("test-url", "https://example.com", "URL fetched through the proxy to test end-to-end CA trust")
	agentDoctorCmd.Flags().String("proxy-data-dir", "", "proxy data directory holding the CA key (default: <config-dir>/proxy)")
	agentCmd.AddCommand(agentDoctorCmd)

	agentEnforceCmd.Flags().String("strategy", "shared-box", "egress lock strategy: shared-box (compose onto an existing firewall) or owned-container (flush)")
	agentEnforceCmd.Flags().String("agent-user", "agent", "unprivileged user to drop to before running the agent")
	agentEnforceCmd.Flags().String("proxy-host", "127.0.0.1", "host the credential proxy is reachable at from inside this boundary")
	agentEnforceCmd.Flags().Int("proxy-port", 14322, "port the credential proxy is listening on")
	agentEnforceCmd.Flags().String("ca", "", "proxy CA cert path (default <data-dir>/ca.crt)")
	agentEnforceCmd.Flags().String("agent-env", "", "path to the proxy's agent.env (default <data-dir>/agent.env)")
	agentEnforceCmd.Flags().Bool("strict-dns", false, "treat an open external DNS resolver as a preflight failure")
	agentEnforceCmd.Flags().String("test-url", "https://example.com", "URL fetched through the proxy to test end-to-end CA trust")
	agentEnforceCmd.Flags().String("proxy-data-dir", "", "proxy data directory holding the CA key (default: <config-dir>/proxy)")
	agentCmd.AddCommand(agentEnforceCmd)

	rootCmd.AddCommand(agentCmd)
}
