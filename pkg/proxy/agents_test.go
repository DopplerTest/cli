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

func TestAgentProfileRoundTripsTokenExpiry(t *testing.T) {
	dir := t.TempDir()
	p := &AgentProfile{
		Name:           "demo",
		Project:        "proj",
		Config:         "dev",
		Command:        []string{"claude"},
		TokenSlug:      "slug-1",
		TokenName:      "agent-proxy demo (host)",
		TokenExpiresAt: "2026-10-08T12:00:00Z",
		CreatedAt:      "2026-10-01T12:00:00Z",
	}
	if err := SaveAgent(dir, p); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	got, err := LoadAgent(dir, "demo")
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if got.TokenExpiresAt != "2026-10-08T12:00:00Z" {
		t.Fatalf("TokenExpiresAt = %q, want the saved value", got.TokenExpiresAt)
	}
}

func TestAgentProfileRoundTripsWorkplace(t *testing.T) {
	dir := t.TempDir()
	p := &AgentProfile{Name: "demo", Project: "proj", Config: "dev", WorkplaceSlug: "acme", WorkplaceName: "Acme"}
	if err := SaveAgent(dir, p); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	got, err := LoadAgent(dir, "demo")
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if got.WorkplaceSlug != "acme" || got.WorkplaceName != "Acme" {
		t.Fatalf("workplace = %q %q, want the saved values", got.WorkplaceSlug, got.WorkplaceName)
	}
	// A profile from before the fields existed reads back with them empty.
	legacy := &AgentProfile{Name: "old", Project: "proj", Config: "dev"}
	if err := SaveAgent(dir, legacy); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadAgent(dir, "old"); got.WorkplaceSlug != "" {
		t.Fatalf("legacy WorkplaceSlug = %q, want empty", got.WorkplaceSlug)
	}
}

// A profile written before the sandbox was removed lists the directories it mounted.
// It still loads, and the key is dropped the next time the profile is saved.
func TestLoadAgentReadsAProfileThatStillHasMounts(t *testing.T) {
	dir := t.TempDir()
	agentDir := AgentDir(dir, "old")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profile := `name: old
project: proj
config: dev
command:
    - claude
agent: claude
mounts:
    - /Users/dev/code/app
    - /Users/dev/docs:ro
token_slug: slug-1
created_at: "2026-10-01T12:00:00Z"
`
	if err := os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAgent(dir, "old")
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	want := &AgentProfile{Name: "old", Project: "proj", Config: "dev", Command: []string{"claude"}, Agent: "claude", TokenSlug: "slug-1", CreatedAt: "2026-10-01T12:00:00Z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadAgent = %+v, want %+v", got, want)
	}
	if agents, err := ListAgents(dir); err != nil || len(agents) != 1 {
		t.Fatalf("ListAgents = %+v, %v, want the one old profile", agents, err)
	}

	if err := SaveAgent(dir, got); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(agentDir, "agent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "mounts") {
		t.Fatalf("a saved profile should no longer carry mounts:\n%s", saved)
	}
}

// A profile no longer stores a command, so agent run starts the program of the agent's
// kind. The command a profile of an older version stores was written for the container,
// with flags such as --dangerously-skip-permissions, and is not run on the user's
// machine.
func TestAgentProfileRunCommandIsTheProgramOfItsKindAndNeverTheStoredCommand(t *testing.T) {
	for _, tc := range []struct {
		profile AgentProfile
		want    []string
	}{
		{AgentProfile{Agent: "claude"}, []string{"claude"}},
		{AgentProfile{Agent: "codex"}, []string{"codex"}},
		{AgentProfile{Agent: "gemini"}, []string{"gemini"}},
		{AgentProfile{Agent: "aider"}, []string{"aider"}},
		{AgentProfile{Agent: "other"}, []string{"claude"}},
		{AgentProfile{}, []string{"claude"}},
		{AgentProfile{Agent: "claude", Command: []string{"claude", "--dangerously-skip-permissions"}}, []string{"claude"}},
		{AgentProfile{Agent: "codex", Command: []string{"claude", "--verbose"}}, []string{"codex"}},
		{AgentProfile{Command: []string{"my-wrapper"}}, []string{"claude"}},
	} {
		if got := tc.profile.RunCommand(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("RunCommand of kind %q with the stored command %q = %q, want %q", tc.profile.Agent, tc.profile.Command, got, tc.want)
		}
	}
}

func TestSaveAgentWritesNoCommandForANewProfileAndKeepsAnOldOne(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []*AgentProfile{
		{Name: "new", Project: "proj", Config: "dev", Agent: "codex"},
		{Name: "old", Project: "proj", Config: "dev", Agent: "codex", Command: []string{"codex", "--full-auto"}},
	} {
		if err := SaveAgent(dir, p); err != nil {
			t.Fatal(err)
		}
	}
	written, err := os.ReadFile(filepath.Join(AgentDir(dir, "new"), "agent.yaml"))
	if err != nil || strings.Contains(string(written), "command") {
		t.Errorf("the new profile was written as\n%s\n%v, want no command in it", written, err)
	}
	old, err := LoadAgent(dir, "old")
	if err != nil || !reflect.DeepEqual(old.Command, []string{"codex", "--full-auto"}) {
		t.Errorf("LoadAgent = %+v, %v, want the stored command kept", old, err)
	}
}
