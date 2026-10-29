/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	agentproxy "github.com/DopplerTest/agent-proxy"
	"github.com/DopplerHQ/cli/pkg/proxy"
)

type staticSource map[string]string

func (s staticSource) List(context.Context) ([]string, error) {
	names := make([]string, 0, len(s))
	for n := range s {
		names = append(names, n)
	}
	return names, nil
}

func (s staticSource) Fetch(_ context.Context, ref agentproxy.SecretRef) (string, error) {
	return s[ref.Name], nil
}

// Every setting proxy start resolves has to reach the engine; a dropped field
// here would silently disable a feature.
func TestEngineOptionsCarryEverySetting(t *testing.T) {
	dir := t.TempDir()
	cfg := &proxy.ProxyConfig{
		Bindings:    map[string][]agentproxy.Rule{"GH": {{Host: "api.github.com"}}},
		Methods:     map[string]proxy.CredentialMethod{"OA": {Kind: "oauth2_client_credentials", TokenURL: "https://p/token", ClientID: "cid"}},
		PassByValue: []string{"MODEL_TOKEN"},
	}
	opts, err := engineOptions(cfg, proxyStartInputs{
		address:            "127.0.0.1:14322",
		dataDir:            dir,
		logOut:             io.Discard,
		passthrough:        []string{"api.anthropic.com"},
		upstreamProxy:      "http://127.0.0.1:3128",
		proxyToken:         "per-run-token",
		allowPrivateEgress: true,
		source:             staticSource{"GH": "ghp_x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AllowPrivateEgress || opts.ProxyAuthToken != "per-run-token" || opts.UpstreamProxy != "http://127.0.0.1:3128" || opts.ListenAddr != "127.0.0.1:14322" {
		t.Fatalf("flags did not reach the engine: %+v", opts)
	}
	if opts.AgentEnvPath != filepath.Join(dir, "agent.env") || opts.DataDir != dir {
		t.Fatalf("data dir paths wrong: %+v", opts)
	}
	if len(opts.PassByValue) != 1 || opts.PassByValue[0] != "MODEL_TOKEN" {
		t.Fatalf("pass_by_value did not reach the engine: %+v", opts.PassByValue)
	}
	if _, ok := opts.Secrets.(*agentproxy.RefreshingSource); !ok {
		t.Fatalf("secrets should be wrapped in RefreshingSource, got %T", opts.Secrets)
	}
	rules, ok := opts.Binding.(*agentproxy.RuleResolver)
	if !ok {
		t.Fatalf("binding should be the rule resolver, got %T", opts.Binding)
	}
	if allowed, _ := rules.Allowed(agentproxy.BindingRequest{Name: "GH", Value: "ghp_x", Dest: agentproxy.Destination{Host: "api.github.com:443", Path: "/", Method: "GET"}}); !allowed {
		t.Fatal("the declared rule should allow its host")
	}
	if allowed, _ := rules.Allowed(agentproxy.BindingRequest{Name: "DB", Value: "plain-value", Dest: agentproxy.Destination{Host: "db.example.com:443", Path: "/", Method: "GET"}}); allowed {
		t.Fatal("an undeclared secret must be refused by default")
	}
	if m := opts.Methods["OA"]; m.Kind != "oauth2_client_credentials" || m.TokenURL != "https://p/token" {
		t.Fatalf("credential method did not reach the engine: %+v", opts.Methods)
	}
}

// An agent's own provider login reaches the engine to be guarded once its provider's
// hosts are all passed through, and another provider's never does.
func TestEngineOptionsGuardTheAgentsOwnCredential(t *testing.T) {
	cfg := &proxy.ProxyConfig{Passthrough: proxy.ProviderHosts([]string{"claude"})}
	opts, err := engineOptions(cfg, proxyStartInputs{
		dataDir:   t.TempDir(),
		logOut:    io.Discard,
		source:    staticSource{},
		agentType: "claude",
		agentEnv:  map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "claude-login", "OPENAI_API_KEY": "openai-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.AgentCredentials) != 1 || opts.AgentCredentials["CLAUDE_CODE_OAUTH_TOKEN"] != "claude-login" {
		t.Fatalf("only the Claude login should be guarded; got %v", opts.AgentCredentials)
	}
}

func TestEngineOptionsRejectUnknownUnboundPolicy(t *testing.T) {
	if _, err := engineOptions(&proxy.ProxyConfig{Unbound: "maybe"}, proxyStartInputs{logOut: io.Discard, source: staticSource{}}); err == nil {
		t.Fatal("an unknown unbound policy must be an error")
	}
}

// recordingReloader stands in for the running engine.
type recordingReloader struct{ policies []agentproxy.Policy }

func (r *recordingReloader) Reload(p agentproxy.Policy) { r.policies = append(r.policies, p) }

func collectLogs() (*[]string, func(string, ...any)) {
	var lines []string
	return &lines, func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
}

// A saved config reaches the running proxy with the overrides for its Doppler config
// applied, the same way proxy start read it.
func TestReloadPolicyAppliesTheConfigsOverrides(t *testing.T) {
	r := &recordingReloader{}
	logs, logf := collectLogs()
	next := &proxy.ProxyConfig{
		Bindings: map[string][]agentproxy.Rule{"GH": {{Host: "api.github.com"}}},
		ConfigOverrides: map[string]proxy.ConfigOverride{
			"prd": {Bindings: map[string][]agentproxy.Rule{"GH": {{Host: "ghe.example.com"}}}},
		},
	}
	reloadPolicy(r, next, reloadBaseline{configName: "prd"}, logf)
	if len(r.policies) != 1 {
		t.Fatalf("expected one reload, got %d (logs %v)", len(r.policies), *logs)
	}
	dest := func(host string) agentproxy.BindingRequest {
		return agentproxy.BindingRequest{Name: "GH", Dest: agentproxy.Destination{Host: host}}
	}
	if ok, _ := r.policies[0].Binding.Allowed(dest("ghe.example.com")); !ok {
		t.Fatal("the prd override should be in force")
	}
	if ok, _ := r.policies[0].Binding.Allowed(dest("api.github.com")); ok {
		t.Fatal("the override replaces the base bindings")
	}
}

// A config that is not valid leaves the running policy alone and says why.
func TestReloadPolicyKeepsTheRunningPolicyOnAnInvalidConfig(t *testing.T) {
	for _, next := range []*proxy.ProxyConfig{{Unbound: "maybe"}, {MissingBindings: "sometimes"}, {ProtocolUpgrades: "always"}} {
		r := &recordingReloader{}
		logs, logf := collectLogs()
		reloadPolicy(r, next, reloadBaseline{}, logf)
		if len(r.policies) != 0 {
			t.Fatalf("an invalid config must not be reloaded: %+v", next)
		}
		if len(*logs) != 1 || !strings.Contains((*logs)[0], "was not reloaded") {
			t.Fatalf("the refusal should be logged; got %v", *logs)
		}
	}
}

// The listen address and the pass-by-value list are in the running agent's
// environment, so a change to either waits for a restart, and the log says so.
func TestReloadPolicyReportsSettingsThatWaitForARestart(t *testing.T) {
	r := &recordingReloader{}
	logs, logf := collectLogs()
	base := reloadBaseline{address: "0.0.0.0:14331", passByValue: []string{"B", "A"}}
	reloadPolicy(r, &proxy.ProxyConfig{ListenAddress: "0.0.0.0:14331", PassByValue: []string{"A", "B"}}, base, logf)
	if len(*logs) != 0 {
		t.Fatalf("nothing changed that needs a restart; got %v", *logs)
	}
	reloadPolicy(r, &proxy.ProxyConfig{ListenAddress: "0.0.0.0:14400", PassByValue: []string{"A"}}, base, logf)
	if len(r.policies) != 2 {
		t.Fatalf("the reloadable settings should still be applied; got %d reloads", len(r.policies))
	}
	joined := strings.Join(*logs, "\n")
	if !strings.Contains(joined, "listen_address is now 0.0.0.0:14400") || !strings.Contains(joined, "pass_by_value changed") {
		t.Fatalf("both restart-only changes should be reported; got %v", *logs)
	}
	// An address given with --address is not the config's to change.
	*logs = nil
	reloadPolicy(r, &proxy.ProxyConfig{ListenAddress: "0.0.0.0:14400"}, reloadBaseline{address: "127.0.0.1:9", addressFromFlag: true}, logf)
	if len(*logs) != 0 {
		t.Fatalf("an address from the flag is not reported; got %v", *logs)
	}
}
