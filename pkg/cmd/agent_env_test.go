/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package cmd

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/DopplerHQ/cli/pkg/configuration"
	"github.com/DopplerHQ/cli/pkg/proxy"
	"github.com/spf13/cobra"
)

// The JSON view keeps env_keys as the agent's own names and reports the shared
// names separately, so the desktop app can tell "set" from "set (shared)".
func TestAgentViewSeparatesOwnAndSharedEnvKeys(t *testing.T) {
	configDir, p := seedAgent(t)
	previous := configuration.UserConfigDir
	configuration.UserConfigDir = configDir
	t.Cleanup(func() { configuration.UserConfigDir = previous })

	if err := proxy.SetAgentEnv(proxy.AgentDir(configDir, p.Name), "OPENAI_API_KEY", "own"); err != nil {
		t.Fatal(err)
	}
	if err := proxy.SetSharedEnv(configDir, "CLAUDE_CODE_OAUTH_TOKEN", "shared"); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(viewOf(*p))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		EnvKeys       []string `json:"env_keys"`
		SharedEnvKeys []string `json:"shared_env_keys"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.EnvKeys, []string{"OPENAI_API_KEY"}) {
		t.Fatalf("env_keys = %v, want only the agent's own names", got.EnvKeys)
	}
	if !reflect.DeepEqual(got.SharedEnvKeys, []string{"CLAUDE_CODE_OAUTH_TOKEN"}) {
		t.Fatalf("shared_env_keys = %v, want the shared names", got.SharedEnvKeys)
	}
}

func TestAgentViewSharedEnvKeysIsEmptyListNotNull(t *testing.T) {
	configDir, p := seedAgent(t)
	previous := configuration.UserConfigDir
	configuration.UserConfigDir = configDir
	t.Cleanup(func() { configuration.UserConfigDir = previous })

	data, err := json.Marshal(viewOf(*p))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"env_keys", "shared_env_keys"} {
		if string(got[field]) != "[]" {
			t.Errorf("%s = %s, want [] when nothing is stored", field, got[field])
		}
	}
}

// --shared drops the agent argument, so the arg count each command accepts depends
// on the flag.
func TestEnvArgsDependOnSharedFlag(t *testing.T) {
	cases := []struct {
		name     string
		trailing int
		shared   bool
		args     []string
		wantErr  bool
	}{
		{"set: agent and key", 1, false, []string{"demo", "KEY"}, false},
		{"set: key only without --shared", 1, false, []string{"KEY"}, true},
		{"set --shared: key only", 1, true, []string{"KEY"}, false},
		{"set --shared: agent and key", 1, true, []string{"demo", "KEY"}, true},
		{"set --shared: nothing", 1, true, nil, true},
		{"list: agent", 0, false, []string{"demo"}, false},
		{"list: nothing without --shared", 0, false, nil, true},
		{"list --shared: nothing", 0, true, nil, false},
		{"list --shared: agent", 0, true, []string{"demo"}, true},
	}
	for _, c := range cases {
		cmd := &cobra.Command{Use: "x"}
		cmd.Flags().Bool("shared", false, "")
		if c.shared {
			if err := cmd.Flags().Set("shared", "true"); err != nil {
				t.Fatal(err)
			}
		}
		err := envArgs(c.trailing)(cmd, c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
	}
}
