/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"errors"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	agentproxy "github.com/DopplerTest/agent-proxy"
)

func notesFixture() *ProxyConfig {
	return &ProxyConfig{
		Passthrough: []string{"api.anthropic.com", "console.anthropic.com"},
		Bindings: map[string][]agentproxy.Rule{
			"STRIPE_KEY":   {{Host: "api.stripe.com"}},
			"GITHUB_TOKEN": {{Host: "api.github.com"}, {Host: "uploads.github.com", Paths: []string{"/repos/**"}}},
		},
	}
}

// notesText renders the notes as one line, so where the prose wraps does not decide
// what a test finds in it.
func notesText(cfg *ProxyConfig) string {
	return strings.Join(strings.Fields(RenderAgentNotes(cfg)), " ")
}

func TestAgentNotesSayWhatTheAgentNeeds(t *testing.T) {
	n := notesText(notesFixture())
	for _, want := range []string{
		"dp.mask.",                 // what a placeholder looks like
		"exactly as you find them", // how to use one
		"do not put one in a URL, query string, or request body", // where one is refused
		"GITHUB_TOKEN: api.github.com, uploads.github.com",       // allowed destinations, hosts joined
		"STRIPE_KEY: api.stripe.com",
		"Any other secret",                                         // the rest have no destination yet
		"Doppler agent-proxy refused",                              // how a refusal looks
		"Tell the user",                                            // the correct response to one
		"The AWS CLI and SDKs show",                                // a 403 with no body is the same refusal
		"waiting for them to approve",                              // what to say while the user decides
		"try the request again",                                    // once the user has approved
		"set the policy for secrets with no binding to \"Ask me\"", // the remedy when nobody is asked
		"do the task the way you would anywhere else",              // the proxy is not the job
		"Do not ask the user for a credential's value",             // the agent never needs a real one
		"do not use it",                                            // a credential pasted into the conversation
		"a refusal is the user's decision",                         // never route around one
		"AWS requests are signed for you",                          // so the agent never signs one itself
		"either AWS key",                                           // a one-off call works with either half
		"Never compute an AWS signature yourself",                  // the placeholders cannot sign anyway
		"client secret's placeholder",                              // an OAuth client's token is supplied
		"WebSocket",                                                // refused unless the user allowed it
		"HTTPS remotes",                                            // a placeholder does not work with git over SSH
		"git -c credential.helper= -c credential.helper='!f()",     // git over HTTPS with the token, for one command
		"whichever variable holds the token",                       // the example names one variable only
	} {
		if !strings.Contains(n, want) {
			t.Errorf("notes should contain %q\n%s", want, n)
		}
	}
	// Bindings are listed in name order so the text is stable between runs.
	if strings.Index(n, "GITHUB_TOKEN:") > strings.Index(n, "STRIPE_KEY:") {
		t.Error("bindings should be listed in name order")
	}
}

func TestAgentNotesNeverHelpAnAgentMisbehave(t *testing.T) {
	cfg := notesFixture()
	n := strings.ToLower(notesText(cfg))
	// Blind-tunneled hosts are the one route the proxy does not inspect; naming them
	// would be a map to the exit. Mechanism details and evasion vocabulary likewise.
	for _, host := range cfg.Passthrough {
		if strings.Contains(n, strings.ToLower(host)) {
			t.Errorf("notes must not name the passthrough host %q", host)
		}
	}
	// The agent runs on the user's machine, where a request can skip the proxy and the
	// proxy's own files are readable. The notes say neither how nor where.
	for _, banned := range []string{"passthrough", "tunnel", "inspect", "config.yaml", "dns", "port ", "ip address", "header-only", "scrub", "http_proxy", "https_proxy", "no_proxy", "bypass", ".doppler"} {
		if strings.Contains(n, banned) {
			t.Errorf("notes must not mention %q", banned)
		}
	}
}

// The agent runs on the user's machine, so nothing the sandbox used to give it is
// described: no home volume, no mounted directories, no missing root.
func TestAgentNotesDoNotDescribeASandbox(t *testing.T) {
	n := strings.ToLower(notesText(notesFixture()))
	for _, gone := range []string{"sandbox", "container", "docker", "/workspace", "/home/agent", "mounted", "without root", "browser"} {
		if strings.Contains(n, gone) {
			t.Errorf("notes must not mention %q", gone)
		}
	}
}

// The agent shares the user's home. A placeholder saved in a config file, or a login
// command that changes git's credential helper, would reach every shell outside the run.
func TestAgentNotesKeepPlaceholdersOutOfTheUsersFiles(t *testing.T) {
	n := notesText(notesFixture())
	for _, want := range []string{
		"on the command line of the one command that needs it",
		"Do not save a placeholder into a config file",
		"do not run a login or setup command that stores one",
		"Those files belong to the user and are read outside this session",
		"git does not read from, or save into, a credential store the user has set up",
		"Nothing is saved this way",
	} {
		if !strings.Contains(n, want) {
			t.Errorf("notes should contain %q\n%s", want, n)
		}
	}
	for _, banned := range []string{"setup-git", "in a tool's config file", "in its login command", "--global"} {
		if strings.Contains(n, banned) {
			t.Errorf("notes must not mention %q", banned)
		}
	}
}

func TestAgentNotesWithNoBindings(t *testing.T) {
	n := notesText(&ProxyConfig{Passthrough: []string{"api.anthropic.com"}})
	if !strings.Contains(n, "No secret has an allowed destination yet") {
		t.Fatalf("empty bindings should be stated plainly:\n%s", n)
	}
	if strings.Contains(n, "anthropic") {
		t.Fatal("still must not name passthrough hosts")
	}
}

func TestCommandWithNotesGivesClaudeCodeTheNotesAsOneArgument(t *testing.T) {
	notes := "line one\nline two with 'quotes' and $VARS"
	for _, tc := range []struct{ command, want []string }{
		{[]string{"claude"}, []string{"claude", "--append-system-prompt", notes}},
		{[]string{"/opt/homebrew/bin/claude", "--model", "opus"}, []string{"/opt/homebrew/bin/claude", "--append-system-prompt", notes, "--model", "opus"}},
		// Claude Code refuses the flag after a subcommand, so it goes before one.
		{[]string{"claude", "mcp", "list"}, []string{"claude", "--append-system-prompt", notes, "mcp", "list"}},
	} {
		given := slices.Clone(tc.command)
		if got, files, err := CommandWithNotes(tc.command, notes, "/data", nil); err != nil || files != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("CommandWithNotes(%q) = %q, %v, %v, want %q and no file to write", tc.command, got, files, err, tc.want)
		}
		if !reflect.DeepEqual(tc.command, given) {
			t.Errorf("the command passed in was changed to %q", tc.command)
		}
	}
}

// The user's own text comes first, then a blank line, then the notes, all in the one
// argument the command already has.
func TestCommandWithNotesAddsTheNotesToTheTextTheUserAlreadyGives(t *testing.T) {
	for _, tc := range []struct{ command, want []string }{
		{[]string{"claude", "--append-system-prompt", "the user's own"}, []string{"claude", "--append-system-prompt", "the user's own\n\nnotes"}},
		{[]string{"claude", "--append-system-prompt=the user's own"}, []string{"claude", "--append-system-prompt=the user's own\n\nnotes"}},
		{[]string{"claude", "--model", "opus", "--append-system-prompt", "the user's own", "-p", "task"}, []string{"claude", "--model", "opus", "--append-system-prompt", "the user's own\n\nnotes", "-p", "task"}},
	} {
		given := slices.Clone(tc.command)
		if got, files, err := CommandWithNotes(tc.command, "notes", "/data", nil); err != nil || files != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("CommandWithNotes(%q) = %q, %v, %v, want %q and no file to write", tc.command, got, files, err, tc.want)
		}
		if !reflect.DeepEqual(tc.command, given) {
			t.Errorf("the command passed in was changed to %q", tc.command)
		}
	}
}

// Claude Code refuses --append-system-prompt together with --append-system-prompt-file,
// so a command that names a file gets no second flag. The file it names is replaced by
// one for the run to write: the user's own text, a blank line, then the notes.
func TestCommandWithNotesAddsTheNotesToTheFileOfTextTheUserAlreadyNames(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		if path != "/mine/prompt.md" {
			return nil, os.ErrNotExist
		}
		return []byte("the user's own\n"), nil
	}
	for _, tc := range []struct {
		command []string
		want    func(file string) []string
	}{
		{[]string{"claude", "--append-system-prompt-file", "/mine/prompt.md"}, func(file string) []string { return []string{"claude", "--append-system-prompt-file", file} }},
		{[]string{"claude", "--append-system-prompt-file=/mine/prompt.md"}, func(file string) []string { return []string{"claude", "--append-system-prompt-file=" + file} }},
		{[]string{"claude", "--model", "opus", "--append-system-prompt-file", "/mine/prompt.md", "-p", "task"}, func(file string) []string {
			return []string{"claude", "--model", "opus", "--append-system-prompt-file", file, "-p", "task"}
		}},
	} {
		given := slices.Clone(tc.command)
		got, files, err := CommandWithNotes(tc.command, "notes", "/data", readFile)
		if err != nil || len(files) != 1 {
			t.Fatalf("CommandWithNotes(%q) = %q, %v, %v, want one file to write", tc.command, got, files, err)
		}
		if !regexp.MustCompile(`^/data/append-system-prompt-[0-9a-f]{12}\.md$`).MatchString(files[0].Path) {
			t.Errorf("the file is written to %q, want a file of /data with a hash in its name", files[0].Path)
		}
		if want := "the user's own\n\nnotes"; string(files[0].Content) != want {
			t.Errorf("the file holds %q, want %q", files[0].Content, want)
		}
		if want := tc.want(files[0].Path); !reflect.DeepEqual(got, want) {
			t.Errorf("CommandWithNotes(%q) = %q, want %q", tc.command, got, want)
		}
		if !reflect.DeepEqual(tc.command, given) {
			t.Errorf("the command passed in was changed to %q", tc.command)
		}
	}
}

// Without the user's text there is nothing right to put in the file, and a second flag
// would have Claude Code refuse the command. So the command stays as the user wrote it.
func TestCommandWithNotesLeavesACommandWhoseFileOfTextCannotBeReadAlone(t *testing.T) {
	unreadable := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	command := []string{"claude", "--append-system-prompt-file", "/mine/gone.md", "-p", "task"}

	got, files, err := CommandWithNotes(command, "notes", "/data", unreadable)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want the error of the read", err)
	}
	if files != nil || !reflect.DeepEqual(got, command) {
		t.Errorf("CommandWithNotes(%q) = %q, %v, want it unchanged and no file to write", command, got, files)
	}
}

func TestCommandWithNotesLeavesEveryOtherCommandAlone(t *testing.T) {
	for _, command := range [][]string{
		{"codex"},
		{"gemini", "--resume"},
		{"aider"},
		{"sh", "-c", "claude"},
		{"claude-wrapper"},
		nil,
	} {
		if got, files, err := CommandWithNotes(command, "notes", "/data", nil); err != nil || files != nil || !reflect.DeepEqual(got, command) {
			t.Errorf("CommandWithNotes(%q) = %q, %v, %v, want it unchanged", command, got, files, err)
		}
	}
	if got, files, err := CommandWithNotes([]string{"claude"}, "", "/data", nil); err != nil || files != nil || !reflect.DeepEqual(got, []string{"claude"}) {
		t.Errorf("with no notes the command should be unchanged, got %q, %v, %v", got, files, err)
	}
}
