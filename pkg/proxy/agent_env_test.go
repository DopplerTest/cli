/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"os"
	"reflect"
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
