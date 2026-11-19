/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DopplerHQ/cli/pkg/configuration"
	"github.com/DopplerHQ/cli/pkg/proxy"
	agentproxy "github.com/DopplerTest/agent-proxy"
	"github.com/spf13/cobra"
)

// agent run ends with os.Exit, so its tests run the command in a child process: this
// test binary again, which agentCommandEnv tells to run one agent command and nothing
// else.
const agentCommandEnv = "DOPPLER_TEST_AGENT_COMMAND"

func TestMain(m *testing.M) {
	if os.Getenv(agentCommandEnv) == "" {
		os.Exit(m.Run())
	}
	command, args, err := agentCmd.Find(os.Args[1:])
	if err == nil {
		err = command.ParseFlags(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	loadFlags(command)
	command.Run(command, command.Flags().Args())
}

// runAgentCommand runs `agent <name> <args>` against configDir in a child process. It
// returns what the command printed and the code it exited with.
func runAgentCommand(t *testing.T, configDir, name string, args ...string) (string, int) {
	t.Helper()
	child := exec.Command(os.Args[0], append([]string{name, "--config-dir", configDir}, args...)...)
	child.Env = append(os.Environ(), agentCommandEnv+"=1")
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return string(output), child.ProcessState.ExitCode()
}

// seedStartedAgent saves the agent demo and the two files its proxy writes when it
// starts, and listens on the address they name. It returns the config directory, the
// agent's own directory and the listener that stands in for the proxy.
func seedStartedAgent(t *testing.T) (configDir, agentDir string, proxyListener net.Listener) {
	t.Helper()
	configDir, _ = seedAgent(t)
	agentDir = proxy.AgentDir(configDir, "demo")
	dataDir := proxy.AgentDataDir(agentDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proxyListener.Close() })
	agentEnv := "HTTPS_PROXY='http://doppler:tok@" + proxyListener.Addr().String() + "'\nSTRIPE_KEY='dp.mask.abc123'\n"
	for path, content := range map[string]string{agentproxy.CACertPath(dataDir): testCertificate("proxy"), agentproxy.AgentEnvPath(dataDir): agentEnv} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return configDir, agentDir, proxyListener
}

// testCertificate returns text in the form of a certificate file. The name stands for
// the certificate itself, which no test reads.
func testCertificate(name string) string {
	return "-----BEGIN CERTIFICATE-----\n" + name + "\n-----END CERTIFICATE-----\n"
}

// startAgentRun starts `agent run demo -- sh -c <script> <dir>` in a child process and
// waits until the script has written the file ready in dir, which it reads as $0. The
// script writes $PPID there, the process ID of agent run. With a wrapper the child
// process is started through it, as its arguments. What the child process prints is in
// run.Stdout, and what is written to stdin reaches it.
func startAgentRun(t *testing.T, ctx context.Context, configDir, dir, script string, wrapper ...string) (run *exec.Cmd, stdin io.Writer) {
	t.Helper()
	args := slices.Concat(wrapper, []string{os.Args[0], "run", "--config-dir", configDir, "demo", "--", "sh", "-c", script, dir})
	run = exec.CommandContext(ctx, args[0], args[1:]...)
	run.Env = append(os.Environ(), agentCommandEnv+"=1")
	output := &bytes.Buffer{}
	run.Stdout, run.Stderr = output, output
	stdin, err := run.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, ctx, filepath.Join(dir, "ready"))
	return run, stdin
}

// sigintCounter is a script for startAgentRun. It counts each SIGINT, stops half a second
// after the first one and writes the count to the file count. It waits with the wait
// builtin because a shell that runs a command in the foreground counts two signals as
// one.
const sigintCounter = `n=0; trap 'n=$((n+1))' INT; sleep 60 & timer=$!; echo $PPID > "$0/ready"
while [ $n -eq 0 ]; do wait $timer; done; kill $timer; sleep 0.5 & timer=$!
while kill -0 $timer 2>/dev/null; do wait $timer; done; echo $n > "$0/count"`

// signalAgentRun sends the signal to the agent run process that startAgentRun started,
// and to no other process.
func signalAgentRun(t *testing.T, ctx context.Context, dir string, sig os.Signal) {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(waitForFile(t, ctx, filepath.Join(dir, "ready"))))
	if err != nil {
		t.Fatal(err)
	}
	agentRun, _ := os.FindProcess(pid)
	if err := agentRun.Signal(sig); err != nil {
		t.Fatal(err)
	}
}

// onATerminal is the wrapper that gives a run a terminal of its own. What the test
// writes to the stdin of script(1) is typed on that terminal.
var onATerminal = []string{"script", "-q", "/dev/null"}

func skipWithoutATerminal(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the run gets its terminal from script(1), with the arguments macOS takes")
	}
}

// standInForClaudeCode puts a claude first on PATH that prints each argument it was
// started with, one to a line.
func standInForClaudeCode(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// untilReleased is a script for startAgentRun. It runs until the test writes the file
// go, and then runs the script it is given.
func untilReleased(then string) string {
	return `echo $PPID > "$0/ready"; while [ ! -e "$0/go" ]; do sleep 0.05; done; ` + then
}

// release lets a run that startAgentRun started with untilReleased go on, and waits for
// it to end.
func release(t *testing.T, run *exec.Cmd, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run.Wait()
}

// waitForFile returns the content of the file once it exists and is not empty.
func waitForFile(t *testing.T, ctx context.Context, path string) string {
	t.Helper()
	for {
		if content, err := os.ReadFile(path); err == nil && len(content) > 0 {
			return string(content)
		}
		if ctx.Err() != nil {
			t.Fatalf("%s was not written", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAgentRunGivesTheCommandTheProxysEnvironmentAndExitsWithItsCode(t *testing.T) {
	configDir, agentDir, proxyListener := seedStartedAgent(t)
	t.Setenv("STRIPE_KEY", "sk_live_real")
	command := []string{"sh", "-c", `echo "$HTTPS_PROXY $STRIPE_KEY"; exit 7`}

	output, code := runAgentCommand(t, configDir, "run", append([]string{"demo", "--"}, command...)...)
	if code != 7 {
		t.Errorf("exit code = %d, want the command's own, 7", code)
	}
	if want := "http://doppler:tok@" + proxyListener.Addr().String() + " dp.mask.abc123\n"; output != want {
		t.Errorf("the command printed %q, want the proxy's entries, %q", output, want)
	}
	run, err := proxy.ReadLastRun(agentDir)
	if err != nil || run == nil {
		t.Fatalf("ReadLastRun = %+v, %v, want the record of this run", run, err)
	}
	if !run.Finished() || *run.ExitCode != 7 || !reflect.DeepEqual(run.Command, command) {
		t.Errorf("last run = %+v, want it ended with exit code 7 and the command %q", run, command)
	}
}

func TestAgentRunGivesClaudeCodeTheNotesAndRecordsTheCommandWithoutThem(t *testing.T) {
	configDir, agentDir, _ := seedStartedAgent(t)
	standInForClaudeCode(t)

	output, code := runAgentCommand(t, configDir, "run", "demo")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0. Output:\n%s", code, output)
	}
	cfg, _, err := proxy.LoadOrScaffold(proxy.AgentConfigPath(agentDir))
	if err != nil {
		t.Fatal(err)
	}
	if want := "--append-system-prompt\n" + proxy.RenderAgentNotes(cfg) + "\n"; output != want {
		t.Errorf("claude was started with\n%s\nwant the flag and then the agent notes as one argument", output)
	}
	run, err := proxy.ReadLastRun(agentDir)
	if err != nil || run == nil {
		t.Fatalf("ReadLastRun = %+v, %v, want the record of this run", run, err)
	}
	if !reflect.DeepEqual(run.Command, []string{"claude"}) {
		t.Errorf("recorded command = %q, want the agent's own command without the notes", run.Command)
	}
}

// Claude Code refuses --append-system-prompt beside --append-system-prompt-file. So a
// command that names a file gets no second flag: the run writes a file of its own, the
// text of the user's file and then the notes, and names that one to Claude Code.
func TestAgentRunAddsTheNotesToTheFileOfTextTheCommandNames(t *testing.T) {
	configDir, agentDir, _ := seedStartedAgent(t)
	standInForClaudeCode(t)
	mine := filepath.Join(t.TempDir(), "mine.md")
	if err := os.WriteFile(mine, []byte("the user's own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := []string{"claude", "--append-system-prompt-file", mine, "-p", "task"}

	output, code := runAgentCommand(t, configDir, "run", append([]string{"demo", "--"}, command...)...)
	started := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if code != 0 || len(started) != 4 {
		t.Fatalf("exit code = %d, claude was started with %q, want the four arguments of the command", code, started)
	}
	written := started[1]
	if want := []string{"--append-system-prompt-file", written, "-p", "task"}; !reflect.DeepEqual(started, want) {
		t.Errorf("claude was started with %q, want %q", started, want)
	}
	if pattern := "^" + regexp.QuoteMeta(proxy.AgentDataDir(agentDir)) + `/append-system-prompt-[0-9a-f]{12}\.md$`; !regexp.MustCompile(pattern).MatchString(written) {
		t.Errorf("the file named to claude is %q, want one in the agent's proxy data directory", written)
	}
	cfg, _, err := proxy.LoadOrScaffold(proxy.AgentConfigPath(agentDir))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(written)
	if want := "the user's own\n\n" + proxy.RenderAgentNotes(cfg); err != nil || string(content) != want {
		t.Errorf("the file holds %q, %v, want the user's text, a blank line and the agent notes", content, err)
	}
	if info, err := os.Stat(written); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("%s: %v, %v, want mode 0600", written, info, err)
	}
	run, err := proxy.ReadLastRun(agentDir)
	if err != nil || run == nil || !reflect.DeepEqual(run.Command, command) {
		t.Errorf("last run = %+v, %v, want the command as the user gave it, %q", run, err, command)
	}
}

// Without the text of the user's file there is nothing right to write, and a second flag
// would have Claude Code refuse the command. So it runs as the user wrote it.
func TestAgentRunLeavesACommandWhoseFileOfTextCannotBeReadAsItIs(t *testing.T) {
	configDir, _, _ := seedStartedAgent(t)
	standInForClaudeCode(t)
	gone := filepath.Join(t.TempDir(), "gone.md")

	output, code := runAgentCommand(t, configDir, "run", "demo", "--", "claude", "--append-system-prompt-file", gone)
	if want := "--append-system-prompt-file\n" + gone + "\n"; code != 0 || !strings.HasSuffix(output, want) || strings.Contains(output, "--append-system-prompt\n") {
		t.Errorf("exit code = %d, output = %q, want claude started with the user's own two arguments", code, output)
	}
	if warnings := strings.Count(output, "Unable to read the file of --append-system-prompt-file"); warnings != 1 || !strings.Contains(output, "without the agent notes") {
		t.Errorf("output = %q, want one warning that the notes were left out, got %d", output, warnings)
	}
}

// The desktop app reads the record to tell a run that is going from one that ended, so
// it is written before the command starts, with no end yet.
func TestAgentRunRecordsTheStartOfARunWhileTheCommandRuns(t *testing.T) {
	configDir, agentDir, _ := seedStartedAgent(t)
	command := []string{"cat", proxy.AgentLastRunPath(agentDir)}
	before := time.Now()

	output, code := runAgentCommand(t, configDir, "run", append([]string{"demo", "--"}, command...)...)
	if code != 0 || !strings.Contains(output, `"ended_at":null,"exit_code":null`) {
		t.Fatalf("exit code = %d, the command read %q, want a record with no end and no exit code", code, output)
	}
	var whileRunning proxy.LastRun
	if err := json.Unmarshal([]byte(output), &whileRunning); err != nil {
		t.Fatal(err)
	}
	if whileRunning.StartedAt.Before(before) || whileRunning.StartedAt.After(time.Now()) || !reflect.DeepEqual(whileRunning.Command, command) {
		t.Errorf("the record while the command ran = %+v, want it started during this test with the command %q", whileRunning, command)
	}
}

// An agent can be deleted while its command is still running. The end of that run must
// not bring the agent's directory back, or an agent created with the same name later
// would start with this run as its last.
func TestAgentRunRecordsNoEndForAnAgentDeletedWhileItsCommandRan(t *testing.T) {
	configDir, agentDir, _ := seedStartedAgent(t)
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, _ := startAgentRun(t, ctx, configDir, dir, untilReleased("exit 3"))
	// What agent delete does to the agent's files.
	if err := os.RemoveAll(agentDir); err != nil {
		t.Fatal(err)
	}
	release(t, run, dir)

	if code, output := run.ProcessState.ExitCode(), run.Stdout.(*bytes.Buffer).String(); code != 3 || output != "" {
		t.Errorf("exit code = %d, output = %q, want the command's own code and nothing said about the record", code, output)
	}
	if _, err := os.Stat(agentDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the directory of the deleted agent is there again: %v", err)
	}
}

func TestAgentRunNamesTheProxyToStartWhenItHasNotStarted(t *testing.T) {
	configDir, _ := seedAgent(t)

	output, code := runAgentCommand(t, configDir, "run", "demo", "--", "true")
	if code != 1 || !strings.Contains(output, "proxy start --agent demo") {
		t.Errorf("exit code = %d, output = %q, want exit 1 and the command that starts the agent's proxy", code, output)
	}
	if run, _ := proxy.ReadLastRun(proxy.AgentDir(configDir, "demo")); run != nil {
		t.Errorf("last run = %+v, want none for a command that never started", run)
	}
}

func TestAgentRunRefusesAnAgentEnvWithNoProxyAddress(t *testing.T) {
	for _, agentEnv := range []string{"", "STRIPE_KEY='dp.mask.abc123'\n", "HTTPS_PROXY=''\nSTRIPE_KEY='dp.mask.abc123'\n"} {
		configDir, agentDir, _ := seedStartedAgent(t)
		if err := os.WriteFile(agentproxy.AgentEnvPath(proxy.AgentDataDir(agentDir)), []byte(agentEnv), 0o600); err != nil {
			t.Fatal(err)
		}

		output, code := runAgentCommand(t, configDir, "run", "demo", "--", "true")
		if code != 1 || !strings.Contains(output, "proxy address not found") || !strings.Contains(output, "proxy start --agent demo") {
			t.Errorf("agent.env %q: exit code = %d, output = %q, want exit 1 and the command that restarts the agent's proxy", agentEnv, code, output)
		}
		if run, _ := proxy.ReadLastRun(agentDir); run != nil {
			t.Errorf("agent.env %q: last run = %+v, want none for a command that never started", agentEnv, run)
		}
	}
}

// The proxy leaves ca.crt and agent.env behind when it exits, so the files alone do not
// show that it is running.
func TestAgentRunRefusesAProxyThatHasStopped(t *testing.T) {
	configDir, agentDir, proxyListener := seedStartedAgent(t)
	proxyListener.Close()

	output, code := runAgentCommand(t, configDir, "run", "demo", "--", "true")
	if code != 1 || !strings.Contains(output, "proxy not running ("+proxyListener.Addr().String()+")") || !strings.Contains(output, "proxy start --agent demo") {
		t.Errorf("exit code = %d, output = %q, want exit 1 and the command that starts the agent's proxy", code, output)
	}
	if run, _ := proxy.ReadLastRun(agentDir); run != nil {
		t.Errorf("last run = %+v, want none for a command that never started", run)
	}
}

// A client that reads SSL_CERT_FILE trusts nothing outside that file, so the run gets a
// bundle that ends with the user's own roots and the proxy CA, after the roots of the
// system the test runs on. Node adds NODE_EXTRA_CA_CERTS to its own roots, so the user's
// file for it is kept the same way.
func TestAgentRunWritesTheRootsTheCommandTrusts(t *testing.T) {
	configDir, agentDir, _ := seedStartedAgent(t)
	dataDir := proxy.AgentDataDir(agentDir)
	corp := t.TempDir()
	for name, content := range map[string]string{"roots.pem": testCertificate("corp"), "node.pem": testCertificate("node")} {
		if err := os.WriteFile(filepath.Join(corp, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SSL_CERT_FILE", filepath.Join(corp, "roots.pem"))
	t.Setenv("NODE_EXTRA_CA_CERTS", filepath.Join(corp, "node.pem"))
	for _, name := range []string{"REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
		t.Setenv(name, "")
	}
	printed := `echo "$SSL_CERT_FILE $REQUESTS_CA_BUNDLE $CURL_CA_BUNDLE $GIT_SSL_CAINFO $NODE_EXTRA_CA_CERTS"`
	// Each file is named by a hash of its content.
	files := regexp.MustCompile("^((" + regexp.QuoteMeta(dataDir) + `/ca-bundle-[0-9a-f]{12}\.crt) ){4}(` + regexp.QuoteMeta(dataDir) + `/node-extra-ca-certs-[0-9a-f]{12}\.crt)\n$`)

	// The second run shows that a file is replaced, and not added to.
	firstBundle := ""
	for range 2 {
		output, code := runAgentCommand(t, configDir, "run", "demo", "--", "sh", "-c", printed)
		named := files.FindStringSubmatch(output)
		if code != 0 || named == nil || strings.Count(output, named[2]) != 4 {
			t.Fatalf("exit code = %d, the command printed %q, want the bundle of this run four times and then its file for Node", code, output)
		}
		bundle, nodeExtra := named[2], named[3]
		content, err := os.ReadFile(bundle)
		if want := testCertificate("corp") + testCertificate("proxy"); err != nil || !strings.HasSuffix(string(content), want) {
			t.Errorf("the bundle = %q, %v, want it to end with %q", content, err, want)
		}
		if firstBundle != "" && string(content) != firstBundle {
			t.Errorf("the bundle changed from %d to %d bytes on a second run, want it the same", len(firstBundle), len(content))
		}
		firstBundle = string(content)
		content, err = os.ReadFile(nodeExtra)
		if want := testCertificate("node") + testCertificate("proxy"); err != nil || string(content) != want {
			t.Errorf("the file for Node = %q, %v, want %q", content, err, want)
		}
		for _, path := range []string{bundle, nodeExtra} {
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
				t.Errorf("%s: %v, %v, want mode 0600", filepath.Base(path), info, err)
			}
		}
	}
}

// Two runs of one agent can be going at the same time, each started with roots of its
// own. The second must leave the file the first still reads as it is.
func TestAgentRunLeavesTheRootsOfARunThatIsStillGoing(t *testing.T) {
	configDir, _, _ := seedStartedAgent(t)
	corp, dir := t.TempDir(), t.TempDir()
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(corp, name+".pem"), []byte(testCertificate(name)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO", "NODE_EXTRA_CA_CERTS"} {
		t.Setenv(name, "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Setenv("SSL_CERT_FILE", filepath.Join(corp, "first.pem"))
	first, _ := startAgentRun(t, ctx, configDir, dir, untilReleased(`cat "$SSL_CERT_FILE"`))
	t.Setenv("SSL_CERT_FILE", filepath.Join(corp, "second.pem"))
	output, code := runAgentCommand(t, configDir, "run", "demo", "--", "sh", "-c", `cat "$SSL_CERT_FILE"`)
	if want := testCertificate("second") + testCertificate("proxy"); code != 0 || !strings.HasSuffix(output, want) {
		t.Fatalf("exit code = %d, the second run trusts %q, want it to end with %q", code, output, want)
	}
	release(t, first, dir)

	if got, want := first.Stdout.(*bytes.Buffer).String(), testCertificate("first")+testCertificate("proxy"); !strings.HasSuffix(got, want) {
		t.Errorf("after the second run the first trusts %q, want it to still end with its own roots, %q", got, want)
	}
}

// A terminal sends Ctrl-C to every process in its foreground group, so the command gets
// it from the terminal. Passed on by agent run as well, it would arrive two times. The
// group decides that and not the streams, so a pipe on stdin or a file on stdout
// changes nothing.
func TestAgentRunOnATerminalLeavesCtrlCToTheTerminal(t *testing.T) {
	skipWithoutATerminal(t)
	for _, tc := range []struct {
		name    string
		wrapper []string
	}{
		{"with every stream on the terminal", onATerminal},
		{"with stdout sent to a file", slices.Concat(onATerminal, []string{"sh", "-c", `exec "$@" > /dev/null`, "sh"})},
		{"with stdin from a pipe", slices.Concat(onATerminal, []string{"sh", "-c", `: | "$@"`, "sh"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configDir, _, _ := seedStartedAgent(t)
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			run, keyboard := startAgentRun(t, ctx, configDir, dir, sigintCounter, tc.wrapper...)
			if _, err := keyboard.Write([]byte{0x03}); err != nil {
				t.Fatal(err)
			}
			run.Wait()
			if count, _ := os.ReadFile(filepath.Join(dir, "count")); string(count) != "1\n" {
				t.Errorf("the command counted %q SIGINT for one Ctrl-C, want 1", count)
			}
		})
	}
}

// A SIGTERM or a SIGHUP is sent to agent run alone, so the command gets it only when
// agent run passes it on, in the foreground of a terminal or anywhere else. Only a
// SIGTERM stops a run on purpose, so only that one is said to have stopped it.
func TestAgentRunPassesASIGTERMAndASIGHUPOnToTheCommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		signal  syscall.Signal
		wrapper []string
		code    int
		stopped bool
	}{
		{"a SIGTERM, started by this test", syscall.SIGTERM, nil, 143, true},
		{"a SIGTERM, on a terminal", syscall.SIGTERM, onATerminal, 143, true},
		{"a SIGHUP, started by this test", syscall.SIGHUP, nil, 129, false},
		{"a SIGHUP, on a terminal", syscall.SIGHUP, onATerminal, 129, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wrapper != nil {
				skipWithoutATerminal(t)
			}
			configDir, agentDir, _ := seedStartedAgent(t)
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			run, _ := startAgentRun(t, ctx, configDir, dir, `echo $PPID > "$0/ready"; exec sleep 10`, tc.wrapper...)
			signalAgentRun(t, ctx, dir, tc.signal)
			run.Wait()
			if code, output := run.ProcessState.ExitCode(), run.Stdout.(*bytes.Buffer).String(); code != tc.code || strings.Contains(output, "Run stopped.") != tc.stopped {
				t.Errorf("exit code = %d, output = %q, want %d and the line that says the run was stopped: %v", code, output, tc.code, tc.stopped)
			}
			last, err := proxy.ReadLastRun(agentDir)
			if err != nil || last == nil || !last.Finished() || *last.ExitCode != tc.code {
				t.Errorf("last run = %+v, %v, want it ended with exit code %d", last, err, tc.code)
			}
		})
	}
}

// A job in the background gets no Ctrl-C from the terminal, so a SIGINT sent to agent
// run there reaches the command only when agent run passes it on.
func TestAgentRunInTheBackgroundPassesASIGINTOnToTheCommand(t *testing.T) {
	skipWithoutATerminal(t)
	configDir, _, _ := seedStartedAgent(t)
	dir := t.TempDir()
	// A shell with job control gives a background job a process group of its own.
	backgroundJob := []string{"sh", "-c", `set -m; "$@" & wait`, "sh"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, _ := startAgentRun(t, ctx, configDir, dir, sigintCounter, slices.Concat(onATerminal, backgroundJob)...)
	signalAgentRun(t, ctx, dir, os.Interrupt)
	run.Wait()
	if count, _ := os.ReadFile(filepath.Join(dir, "count")); string(count) != "1\n" {
		t.Errorf("the command counted %q SIGINT for the one sent to agent run, want 1", count)
	}
}

// Ctrl-Z stops every process of a job. agent run has to stop with its command, or the
// shell keeps waiting for it and never takes the terminal back.
func TestAgentRunOnATerminalStopsWithItsCommandOnCtrlZ(t *testing.T) {
	skipWithoutATerminal(t)
	configDir, agentDir, _ := seedStartedAgent(t)
	dir := t.TempDir()
	// A shell with job control makes agent run a job of its own. Without one a terminal
	// stops nothing, because no shell is left to continue it.
	jobShell := []string{"sh", "-c", `set -m; "$@"`, "sh"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, keyboard := startAgentRun(t, ctx, configDir, dir, `echo $PPID > "$0/ready"; sleep 2`, slices.Concat(onATerminal, jobShell)...)
	pid := strings.TrimSpace(waitForFile(t, ctx, filepath.Join(dir, "ready")))
	if _, err := keyboard.Write([]byte{0x1a}); err != nil {
		t.Fatal(err)
	}
	// ps prints a T in the state of a stopped process.
	for state := ""; !strings.Contains(state, "T"); time.Sleep(10 * time.Millisecond) {
		if ctx.Err() != nil {
			t.Fatalf("the state of agent run is %q after Ctrl-Z, want it stopped", state)
		}
		output, _ := exec.Command("ps", "-o", "stat=", "-p", pid).Output()
		state = strings.TrimSpace(string(output))
	}

	// What fg does: it continues every process of the job, which agent run leads.
	if output, err := exec.Command("kill", "-CONT", "--", "-"+pid).CombinedOutput(); err != nil {
		t.Fatalf("kill -CONT: %v: %s", err, output)
	}
	run.Wait()
	last, err := proxy.ReadLastRun(agentDir)
	if err != nil || last == nil || !last.Finished() || *last.ExitCode != 0 {
		t.Errorf("last run = %+v, %v, want the command to run to its end after it is continued", last, err)
	}
}

// The command given after -- wins over the agent's own, and claude is the command when
// there is neither. Only a named agent has notes and a run record.
func TestResolveHostRunPicksTheAgentAndTheCommand(t *testing.T) {
	configDir := t.TempDir()
	previous := configuration.UserConfigDir
	configuration.UserConfigDir = configDir
	t.Cleanup(func() { configuration.UserConfigDir = previous })
	for _, p := range []*proxy.AgentProfile{
		{Name: "demo", Project: "proj", Config: "dev", Command: []string{"codex", "--full-auto"}},
		{Name: "bare", Project: "proj", Config: "dev"},
	} {
		if err := proxy.SaveAgent(configDir, p); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name    string
		args    []string
		agent   string
		command []string
	}{
		{"the agent's own command", []string{"demo"}, "demo", []string{"codex", "--full-auto"}},
		{"a command after -- replaces it", []string{"demo", "--", "claude", "--verbose"}, "demo", []string{"claude", "--verbose"}},
		{"an agent with no command runs claude", []string{"bare"}, "bare", []string{"claude"}},
		{"no agent", []string{"--", "env"}, "", []string{"env"}},
		{"an agent's name after -- is a command", []string{"--", "demo"}, "", []string{"demo"}},
		{"a first argument that names no agent is the command", []string{"printenv", "HOME"}, "", []string{"printenv", "HOME"}},
		{"nothing at all", nil, "", []string{"claude"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "doppler-beta"}
			cmd := &cobra.Command{Use: "run"}
			root.AddCommand(cmd)
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			run := resolveHostRun(cmd, cmd.Flags().Args())
			if !reflect.DeepEqual(run.command, tc.command) {
				t.Errorf("command = %q, want %q", run.command, tc.command)
			}
			if tc.agent == "" {
				if run.agentDir != "" || run.notes != "" || run.dataDir != proxyDataDir() || run.startHint != "doppler-beta proxy start" {
					t.Errorf("without an agent the run should use the plain proxy and record nothing: %+v", run)
				}
				return
			}
			agentDir := proxy.AgentDir(configDir, tc.agent)
			if run.agentDir != agentDir || run.dataDir != proxy.AgentDataDir(agentDir) {
				t.Errorf("agentDir = %q, dataDir = %q, want the agent's own under %q", run.agentDir, run.dataDir, agentDir)
			}
			if run.startHint != "doppler-beta proxy start --agent "+tc.agent {
				t.Errorf("startHint = %q", run.startHint)
			}
			if !strings.Contains(run.notes, "dp.mask.") {
				t.Errorf("a named agent's run should carry the agent notes, got %q", run.notes)
			}
		})
	}
}

// The egress probes must cover BOTH IP families. A shared-box lock writes only
// iptables rules, so an IPv4-only probe list reports "contained" while the
// agent's IPv6 egress — external routes and `::1` services alike — is wide
// open. This asserts the wiring (both families, all IP literals) without dialing,
// so it can't go flaky; EgressBlockedTCP's own tests cover the dial behavior.
func TestEgressProbesCoverBothIPFamilies(t *testing.T) {
	var v4, v6External, v6Loopback bool
	for _, addr := range egressProbeTargets {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("probe %q is not a valid host:port: %v", addr, err)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			t.Fatalf("probe %q must be an IP literal, got host %q (a hostname would fail at resolution and falsely look contained)", addr, host)
		}
		switch {
		case ip.To4() != nil:
			v4 = true
		case ip.IsLoopback():
			v6Loopback = true
		default:
			v6External = true
		}
	}
	if !v4 {
		t.Error("no IPv4 egress probe")
	}
	if !v6External {
		t.Error("no external IPv6 egress probe; an IPv4-only iptables lock leaves IPv6 egress open")
	}
	if !v6Loopback {
		t.Error("no IPv6 loopback egress probe; `::1` services bypass an IPv4-only lock")
	}
}

// Clause 2 must cover BOTH the effective capability set and the bounding set: a
// clean effective set still lets a file-capability or setuid binary hand
// CAP_NET_ADMIN back, so a bounding-set check is required too (ENG-9749). A
// check's Name is set regardless of platform or result, so this reads the names
// without dialing the network or depending on /proc — it can't go flaky.
func TestPrivilegeChecksCoverBoundingSet(t *testing.T) {
	var names []string
	for _, c := range privilegeChecks() {
		names = append(names, c().Name)
	}
	if !slices.Contains(names, "CAP_NET_ADMIN") {
		t.Errorf("privilege checks must include the effective-set NET_ADMIN check; got %v", names)
	}
	if !slices.Contains(names, "CAP_NET_ADMIN (bounding set)") {
		t.Errorf("privilege checks must also cover the capability bounding set; got %v", names)
	}
}

// TestProxyUserinfo: `agent enforce` must keep the per-run proxy token from
// agent.env's HTTPS_PROXY when it repoints the proxy host — dropping it 407s every
// agent request.
func TestProxyUserinfo(t *testing.T) {
	cases := []struct{ name, env, want string }{
		{"tokened", "HTTPS_PROXY='http://doppler:abc123@127.0.0.1:14322'\n", "doppler:abc123@"},
		{"tokened double-quoted", `HTTPS_PROXY="http://doppler:abc123@127.0.0.1:14322"`, "doppler:abc123@"},
		{"no userinfo", "HTTPS_PROXY='http://127.0.0.1:14322'\n", ""},
		{"no proxy line", "FOO=bar\nBAZ=qux\n", ""},
		{"http_proxy fallback", "HTTP_PROXY='http://doppler:xyz@127.0.0.1:14322'\n", "doppler:xyz@"},
	}
	for _, c := range cases {
		if got := proxyUserinfo([]byte(c.env)); got != c.want {
			t.Errorf("%s: proxyUserinfo = %q, want %q", c.name, got, c.want)
		}
	}
}

// The preflight opens the files that hold brokered secrets, by name. A directory
// would prove nothing, since one the agent cannot list still lets it open a file
// inside.
func TestCredentialSourcesAreConcreteFiles(t *testing.T) {
	got := credentialSources("/home/dev", "/home/dev/.config/agent-proxy")
	want := []string{
		filepath.Join("/home/dev", ".doppler", ".doppler.yaml"),
		filepath.Join("/home/dev", ".config", "agent-proxy", "ca.key"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("credentialSources = %v, want %v", got, want)
	}
	if len(credentialSources("", "")) != 0 {
		t.Fatal("with nothing resolved there is nothing to check")
	}
}

// Under sudo the developer is SUDO_USER, not the root that runs enforce.
func TestDeveloperHomeFollowsSudoUser(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	t.Setenv("SUDO_USER", me.Username)
	if got := developerHome(); got != me.HomeDir {
		t.Fatalf("developerHome under sudo = %q, want %q", got, me.HomeDir)
	}
	t.Setenv("SUDO_USER", "")
	home, _ := os.UserHomeDir()
	if got := developerHome(); got != home {
		t.Fatalf("developerHome without sudo = %q, want %q", got, home)
	}
}
