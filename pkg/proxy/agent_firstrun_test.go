/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"reflect"
	"strings"
	"testing"
)

func TestWrapFirstRunForClaudeWithToken(t *testing.T) {
	got := WrapFirstRun([]string{"claude", "--verbose", "a b"}, []string{"CLAUDE_CODE_OAUTH_TOKEN"})
	if len(got) < 5 || got[0] != "sh" || got[1] != "-c" {
		t.Fatalf("expected an sh -c wrapper, got %v", got)
	}
	// The original command, arguments intact, follows the script and the $0 placeholder.
	if !reflect.DeepEqual(got[4:], []string{"claude", "--verbose", "a b"}) {
		t.Fatalf("original command not preserved: %v", got[4:])
	}
	script := got[2]
	if !strings.Contains(script, "hasCompletedOnboarding") || !strings.HasSuffix(strings.TrimSpace(script), `exec "$@"`) {
		t.Fatalf("script should seed onboarding and then exec the command:\n%s", script)
	}
}

func TestWrapFirstRunMatchesByProgramBasename(t *testing.T) {
	got := WrapFirstRun([]string{"/usr/local/bin/claude"}, []string{"CLAUDE_CODE_OAUTH_TOKEN"})
	if got[0] != "sh" {
		t.Fatalf("an absolute path to claude should still be wrapped, got %v", got)
	}
}

func TestWrapFirstRunLeavesOthersAlone(t *testing.T) {
	cases := []struct {
		name    string
		command []string
		keys    []string
	}{
		{"no token stored", []string{"claude"}, nil},
		{"other token only", []string{"claude"}, []string{"OPENAI_API_KEY"}},
		{"different program", []string{"codex"}, []string{"CLAUDE_CODE_OAUTH_TOKEN"}},
		{"shell", []string{"bash"}, []string{"CLAUDE_CODE_OAUTH_TOKEN"}},
		{"empty command", nil, []string{"CLAUDE_CODE_OAUTH_TOKEN"}},
	}
	for _, c := range cases {
		if got := WrapFirstRun(c.command, c.keys); !reflect.DeepEqual(got, c.command) {
			t.Errorf("%s: command should be unchanged, got %v", c.name, got)
		}
	}
}
