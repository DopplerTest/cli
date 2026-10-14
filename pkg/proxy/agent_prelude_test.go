/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
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
	got := WrapRun([]string{"bash", "-lc", "a b"}, nil, false)
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
		script := scriptOf(t, WrapRun([]string{program}, nil, false))
		if !strings.Contains(script, file) {
			t.Errorf("%s: expected a merge into %s", program, file)
		}
		if !strings.Contains(script, "doppler-sandbox:start") || !strings.Contains(script, "doppler-sandbox:end") {
			t.Errorf("%s: the block must be fenced with markers so a rerun replaces it", program)
		}
	}
}

func TestWrapRunOnboardingOnlyForClaudeWithAToken(t *testing.T) {
	with := scriptOf(t, WrapRun([]string{"claude"}, []string{"CLAUDE_CODE_OAUTH_TOKEN", "DOPPLER_SANDBOX_NOTES"}, false))
	if !strings.Contains(with, "hasCompletedOnboarding") {
		t.Error("claude with a stored token should get the onboarding prelude")
	}
	without := scriptOf(t, WrapRun([]string{"claude"}, []string{"DOPPLER_SANDBOX_NOTES"}, false))
	if strings.Contains(without, "hasCompletedOnboarding") {
		t.Error("claude without a token should not have onboarding marked complete")
	}
	codex := scriptOf(t, WrapRun([]string{"codex"}, []string{"CLAUDE_CODE_OAUTH_TOKEN"}, false))
	if strings.Contains(codex, "hasCompletedOnboarding") {
		t.Error("onboarding is a Claude quirk and must not apply to other agents")
	}
}

func TestWrapRunLeavesAnEmptyCommandAlone(t *testing.T) {
	if got := WrapRun(nil, []string{"CLAUDE_CODE_OAUTH_TOKEN"}, false); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

// Runs the wrapped command under sh with a stand-in program that prints its arguments,
// so the prelude's decision is checked the way the sandbox makes it: against files in
// the home, not against the script's text.
func runWrapped(t *testing.T, home string, command []string, resume bool) string {
	t.Helper()
	bin := t.TempDir()
	stub := "#!/bin/sh\nprintf '%s\\n' \"$(basename \"$0\")\" \"$@\"\n"
	for _, name := range []string{"claude", "codex", "gemini"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wrapped := WrapRun(command, nil, resume)
	cmd := exec.Command(wrapped[0], wrapped[1:]...)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wrapped command failed: %v\n%s", err, out)
	}
	return string(out)
}

func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWrapRunResumeContinuesOnlyWhenAConversationExists(t *testing.T) {
	home := realTempDir(t)
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not on PATH")
	}
	cwdKey := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(home, "-")
	sum := sha256.Sum256([]byte(home))
	cases := []struct {
		program  string
		existing string // file that marks a conversation for this working directory
		content  string
		want     []string
	}{
		{"claude", filepath.Join(".claude", "projects", cwdKey, "s.jsonl"), "{}", []string{"claude", "--continue"}},
		{"codex", filepath.Join(".codex", "sessions", "2026", "10", "10", "rollout-1.jsonl"), `{"cwd":"` + home + `"}`, []string{"codex", "resume", "--last"}},
		{"gemini", filepath.Join(".gemini", "tmp", hex.EncodeToString(sum[:]), "chats", "session-1.json"), "{}", []string{"gemini", "--resume"}},
	}
	for _, c := range cases {
		fresh := runWrapped(t, home, []string{c.program}, true)
		if !strings.Contains(fresh, "starting a new one") || !strings.HasSuffix(fresh, c.program+"\n") {
			t.Errorf("%s with no conversation: expected a fresh start, got %q", c.program, fresh)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(home, c.existing)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, c.existing), []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		resumed := runWrapped(t, home, []string{c.program}, true)
		if !strings.Contains(resumed, "Continuing the previous conversation") || !strings.HasSuffix(resumed, strings.Join(c.want, "\n")+"\n") {
			t.Errorf("%s with a conversation: expected %v, got %q", c.program, c.want, resumed)
		}
		plain := runWrapped(t, home, []string{c.program}, false)
		if !strings.HasSuffix(plain, c.program+"\n") || strings.Contains(plain, "conversation") {
			t.Errorf("%s without --resume: expected the plain command, got %q", c.program, plain)
		}
	}
}

func TestWrapRunResumeKeepsACommandThatAlreadyResumes(t *testing.T) {
	home := realTempDir(t)
	cwdKey := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(home, "-")
	dir := filepath.Join(home, ".claude", "projects", cwdKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runWrapped(t, home, []string{"claude", "-c", "--model", "opus"}, true)
	if !strings.HasSuffix(got, "claude\n-c\n--model\nopus\n") {
		t.Errorf("expected the command untouched, got %q", got)
	}
}

func TestWrapRunResumeStartsFreshForAnUnknownProgram(t *testing.T) {
	script := scriptOf(t, WrapRun([]string{"aider"}, nil, true))
	if strings.Contains(script, "set --") {
		t.Errorf("an unknown program must not have its arguments changed:\n%s", script)
	}
	if !strings.Contains(script, "starting a new one") {
		t.Errorf("the outcome line is still printed:\n%s", script)
	}
}
