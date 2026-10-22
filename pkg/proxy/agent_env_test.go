/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAgentEnvRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := SetAgentEnv(dir, "CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-abc"); err != nil {
		t.Fatal(err)
	}
	// A value containing '=' must survive: only the first '=' separates key from value.
	if err := SetAgentEnv(dir, "OPENAI_API_KEY", "sk-x==y"); err != nil {
		t.Fatal(err)
	}

	env, err := ReadAgentEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-abc", "OPENAI_API_KEY": "sk-x==y"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("ReadAgentEnv = %v, want %v", env, want)
	}
	if got := AgentEnvKeys(dir); !reflect.DeepEqual(got, []string{"CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY"}) {
		t.Fatalf("AgentEnvKeys = %v, want sorted names only", got)
	}

	info, err := os.Stat(AgentLocalEnvPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode = %o, want 0600", info.Mode().Perm())
	}

	if err := UnsetAgentEnv(dir, "OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := UnsetAgentEnv(dir, "NOT_THERE"); err != nil {
		t.Fatalf("unsetting a missing key should not error: %v", err)
	}
	env, _ = ReadAgentEnv(dir)
	if _, ok := env["OPENAI_API_KEY"]; ok || env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-abc" {
		t.Fatalf("after unset: %v", env)
	}
}

func TestAgentEnvRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	for _, key := range []string{"", "lower", "1STARTS_WITH_DIGIT", "HAS-DASH", "HAS SPACE", "HAS=EQUALS"} {
		if err := SetAgentEnv(dir, key, "v"); err == nil {
			t.Errorf("key %q should be rejected", key)
		}
	}
	if err := SetAgentEnv(dir, "OK_KEY", "line1\nline2"); err == nil {
		t.Error("a value containing a newline should be rejected (it would corrupt the file)")
	}
	if err := SetAgentEnv(dir, "OK_KEY", ""); err == nil {
		t.Error("an empty value should be rejected")
	}
}

func TestReadAgentEnvMissingIsEmpty(t *testing.T) {
	env, err := ReadAgentEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Fatalf("expected empty map for a missing file, got %v", env)
	}
	if keys := AgentEnvKeys(t.TempDir()); len(keys) != 0 {
		t.Fatalf("expected no keys, got %v", keys)
	}
}

func TestExportAgentEnvSetsProcessEnvAndReturnsNamesOnly(t *testing.T) {
	t.Cleanup(func() { os.Unsetenv("ZZ_TEST_B"); os.Unsetenv("ZZ_TEST_A") })
	names := ExportAgentEnv(map[string]string{"ZZ_TEST_B": "two", "ZZ_TEST_A": "one"})
	// Names only, sorted: these go to docker as `-e NAME` so the value is inherited
	// from this process's environment and never appears in argv.
	if !reflect.DeepEqual(names, []string{"ZZ_TEST_A", "ZZ_TEST_B"}) {
		t.Fatalf("ExportAgentEnv = %v, want sorted names", names)
	}
	if os.Getenv("ZZ_TEST_A") != "one" || os.Getenv("ZZ_TEST_B") != "two" {
		t.Fatal("values should be exported into the process environment")
	}
}

func TestSharedEnvRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	if err := SetSharedEnv(configDir, "CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-shared"); err != nil {
		t.Fatal(err)
	}
	if err := SetSharedEnv(configDir, "OPENAI_API_KEY", "sk-x==y"); err != nil {
		t.Fatal(err)
	}

	env, err := ReadSharedEnv(configDir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-shared", "OPENAI_API_KEY": "sk-x==y"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("ReadSharedEnv = %v, want %v", env, want)
	}
	if got := SharedEnvKeys(configDir); !reflect.DeepEqual(got, []string{"CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY"}) {
		t.Fatalf("SharedEnvKeys = %v, want sorted names only", got)
	}

	// The shared file sits at the top of the config directory, beside the agents
	// directory rather than inside any agent's, with the same 0600 mode.
	if got, want := SharedEnvPath(configDir), filepath.Join(configDir, "env"); got != want {
		t.Fatalf("SharedEnvPath = %q, want %q", got, want)
	}
	info, err := os.Stat(SharedEnvPath(configDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("shared env file mode = %o, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(AgentLocalEnvPath(AgentDir(configDir, "demo"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a shared set must not touch an agent's own file: %v", err)
	}

	if err := UnsetSharedEnv(configDir, "OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := UnsetSharedEnv(configDir, "NOT_THERE"); err != nil {
		t.Fatalf("unsetting a missing key should not error: %v", err)
	}
	env, _ = ReadSharedEnv(configDir)
	if _, ok := env["OPENAI_API_KEY"]; ok || env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-shared" {
		t.Fatalf("after unset: %v", env)
	}
}

func TestSharedEnvRejectsBadInput(t *testing.T) {
	configDir := t.TempDir()
	if err := SetSharedEnv(configDir, "lower", "v"); err == nil {
		t.Error("a bad key should be rejected for the shared file too")
	}
	if err := SetSharedEnv(configDir, "OK_KEY", ""); err == nil {
		t.Error("an empty value should be rejected for the shared file too")
	}
}

func TestReadSharedEnvMissingIsEmpty(t *testing.T) {
	env, err := ReadSharedEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Fatalf("expected empty map for a missing file, got %v", env)
	}
	if keys := SharedEnvKeys(t.TempDir()); len(keys) != 0 {
		t.Fatalf("expected no keys, got %v", keys)
	}
}

func TestMergeAgentEnvAgentWins(t *testing.T) {
	agent := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "agent-token", "AGENT_ONLY": "a"}
	shared := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "shared-token", "SHARED_ONLY": "s"}
	got := MergeAgentEnv(agent, shared)
	want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "agent-token", "AGENT_ONLY": "a", "SHARED_ONLY": "s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeAgentEnv = %v, want %v", got, want)
	}
	// Neither input is changed by the merge.
	if agent["SHARED_ONLY"] != "" || shared["CLAUDE_CODE_OAUTH_TOKEN"] != "shared-token" {
		t.Fatal("MergeAgentEnv must not write into its inputs")
	}
	if got := MergeAgentEnv(nil, nil); len(got) != 0 {
		t.Fatalf("merging two empty environments = %v, want empty", got)
	}
}

// A credential stored only in the shared file still reaches the run: the merged
// names are what `agent run` exports and hands to the prelude, so the Claude
// onboarding check must fire from the shared token alone.
func TestMergedSharedTokenReachesTheClaudePrelude(t *testing.T) {
	t.Cleanup(func() { os.Unsetenv("CLAUDE_CODE_OAUTH_TOKEN") })
	names := ExportAgentEnv(MergeAgentEnv(map[string]string{}, map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "shared-token"}))
	if !reflect.DeepEqual(names, []string{"CLAUDE_CODE_OAUTH_TOKEN"}) {
		t.Fatalf("exported names = %v, want the shared token's name", names)
	}
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "shared-token" {
		t.Fatal("the shared value should be exported into the process environment")
	}
	script := scriptOf(t, WrapRun([]string{"claude"}, names, false))
	if !strings.Contains(script, "hasCompletedOnboarding") {
		t.Error("the Claude onboarding prelude should fire when the token comes from the shared file")
	}
}
