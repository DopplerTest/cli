/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProviderHostsByProgram(t *testing.T) {
	if got := ProviderHosts([]string{"claude", "--verbose"}); !reflect.DeepEqual(got, []string{"api.anthropic.com", "console.anthropic.com", "claude.ai", "claude.com"}) {
		t.Fatalf("claude: %v", got)
	}
	if got := ProviderHosts([]string{"/usr/local/bin/codex"}); len(got) == 0 || got[0] != "api.openai.com" {
		t.Fatalf("codex should resolve by basename to OpenAI hosts, got %v", got)
	}
	if got := ProviderHosts([]string{"gemini"}); len(got) == 0 || !contains(got, "generativelanguage.googleapis.com") {
		t.Fatalf("gemini: %v", got)
	}
	// Anything the app does not know gets no provider hosts: the user adds them, or saving
	// a credential in the app adds that provider's hosts.
	for _, cmd := range [][]string{{"bash"}, {"python", "agent.py"}, nil} {
		if got := ProviderHosts(cmd); len(got) != 0 {
			t.Errorf("%v: expected no hosts, got %v", cmd, got)
		}
	}
}

func TestScaffoldPassthroughFollowsTheAgent(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "codex.yaml")
	cfg, created, err := LoadOrScaffoldWith(path, ProviderHosts([]string{"codex"}))
	if err != nil || !created {
		t.Fatalf("scaffold: created=%v err=%v", created, err)
	}
	if !reflect.DeepEqual(cfg.Passthrough, ProviderHosts([]string{"codex"})) {
		t.Fatalf("codex scaffold passthrough = %v", cfg.Passthrough)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "anthropic") {
		t.Fatal("a codex agent's config must not carry Anthropic hosts")
	}

	// An unknown program starts with an empty, valid passthrough list.
	path = filepath.Join(dir, "other.yaml")
	cfg, _, err = LoadOrScaffoldWith(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Passthrough) != 0 {
		t.Fatalf("other scaffold passthrough = %v, want none", cfg.Passthrough)
	}

	// The plain proxy keeps today's default.
	path = filepath.Join(dir, "default.yaml")
	cfg, _, err = LoadOrScaffold(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Passthrough, ProviderHosts([]string{"claude"})) {
		t.Fatalf("default scaffold passthrough = %v", cfg.Passthrough)
	}
}

func TestProviderHostsForPrefersTheAgentField(t *testing.T) {
	// A wrapper in front of the program must not change the provider: the agent field wins.
	got := ProviderHostsFor("gemini", []string{"some-wrapper", "--", "claude"})
	if len(got) == 0 || got[0] != "generativelanguage.googleapis.com" {
		t.Fatalf("agent field ignored: %v", got)
	}
	if got := ProviderHostsFor("other", []string{"claude"}); got != nil {
		t.Fatalf("other should have no provider hosts, got %v", got)
	}
	// Older profiles have no agent field and still resolve by the command's program.
	if got := ProviderHostsFor("", []string{"/usr/local/bin/codex"}); len(got) == 0 || got[0] != "api.openai.com" {
		t.Fatalf("fallback by command broken: %v", got)
	}
	if !IsKnownAgent("aider") || IsKnownAgent("cursor") {
		t.Fatal("IsKnownAgent is wrong")
	}
}
