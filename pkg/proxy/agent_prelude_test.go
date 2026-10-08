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

func scriptOf(t *testing.T, wrapped []string) string {
	t.Helper()
	if len(wrapped) < 4 || wrapped[0] != "sh" || wrapped[1] != "-c" {
		t.Fatalf("expected an sh -c wrapper, got %v", wrapped)
	}
	return wrapped[2]
}

func TestWrapRunAlwaysDeliversTheNotesAndPreservesTheCommand(t *testing.T) {
	got := WrapRun([]string{"bash", "-lc", "a b"}, nil)
	script := scriptOf(t, got)
	if !reflect.DeepEqual(got[4:], []string{"bash", "-lc", "a b"}) {
		t.Fatalf("original command not preserved: %v", got[4:])
	}
	if !strings.Contains(script, "DOPPLER_SANDBOX.md") {
		t.Error("every agent gets the generic notes file")
	}
	if !strings.Contains(script, "unset DOPPLER_SANDBOX_NOTES") || !strings.HasSuffix(strings.TrimSpace(script), `exec "$@"`) {
		t.Errorf("script must clear the notes variable and then exec the command:\n%s", script)
	}
	// A shell is not a known agent: no instruction-file merge, no onboarding.
	if strings.Contains(script, "CLAUDE.md") || strings.Contains(script, "hasCompletedOnboarding") {
		t.Error("a plain shell should get neither an instruction file nor onboarding")
	}
}

func TestWrapRunMergesIntoEachAgentsInstructionFile(t *testing.T) {
	cases := map[string]string{
		"claude":                ".claude/CLAUDE.md",
		"/usr/local/bin/claude": ".claude/CLAUDE.md",
		"codex":                 ".codex/AGENTS.md",
		"gemini":                ".gemini/GEMINI.md",
	}
	for program, file := range cases {
		script := scriptOf(t, WrapRun([]string{program}, nil))
		if !strings.Contains(script, file) {
			t.Errorf("%s: expected a merge into %s", program, file)
		}
		if !strings.Contains(script, "doppler-sandbox:start") || !strings.Contains(script, "doppler-sandbox:end") {
			t.Errorf("%s: the block must be fenced with markers so a rerun replaces it", program)
		}
	}
}

func TestWrapRunOnboardingOnlyForClaudeWithAToken(t *testing.T) {
	with := scriptOf(t, WrapRun([]string{"claude"}, []string{"CLAUDE_CODE_OAUTH_TOKEN", "DOPPLER_SANDBOX_NOTES"}))
	if !strings.Contains(with, "hasCompletedOnboarding") {
		t.Error("claude with a stored token should get the onboarding prelude")
	}
	without := scriptOf(t, WrapRun([]string{"claude"}, []string{"DOPPLER_SANDBOX_NOTES"}))
	if strings.Contains(without, "hasCompletedOnboarding") {
		t.Error("claude without a token should not have onboarding marked complete")
	}
	codex := scriptOf(t, WrapRun([]string{"codex"}, []string{"CLAUDE_CODE_OAUTH_TOKEN"}))
	if strings.Contains(codex, "hasCompletedOnboarding") {
		t.Error("onboarding is a Claude quirk and must not apply to other agents")
	}
}

func TestWrapRunLeavesAnEmptyCommandAlone(t *testing.T) {
	if got := WrapRun(nil, []string{"CLAUDE_CODE_OAUTH_TOKEN"}); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
