/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
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

func TestSandboxNotesSayWhatTheAgentNeeds(t *testing.T) {
	n := RenderSandboxNotes(notesFixture())
	for _, want := range []string{
		"dp.mask.",                 // what a placeholder looks like
		"exactly as you find them", // how to use one
		"GITHUB_TOKEN: api.github.com, uploads.github.com", // allowed destinations, hosts joined
		"STRIPE_KEY: api.stripe.com",
		"Any other secret",            // the rest have no destination yet
		"Doppler agent-proxy refused", // how a refusal looks
		"Tell the user",               // the correct response to one
		"/home/agent",                 // what persists
		"/workspace/",
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

func TestSandboxNotesNeverHelpAnAgentMisbehave(t *testing.T) {
	cfg := notesFixture()
	n := strings.ToLower(RenderSandboxNotes(cfg))
	// Blind-tunneled hosts are the one route the proxy does not inspect; naming them
	// would be a map to the exit. Mechanism details and evasion vocabulary likewise.
	for _, host := range cfg.Passthrough {
		if strings.Contains(n, strings.ToLower(host)) {
			t.Errorf("notes must not name the passthrough host %q", host)
		}
	}
	for _, banned := range []string{"passthrough", "tunnel", "inspect", "config.yaml", "dns", "port ", "ip address", "header-only", "scrub", "http_proxy", "https_proxy"} {
		if strings.Contains(n, banned) {
			t.Errorf("notes must not mention %q", banned)
		}
	}
}

func TestSandboxNotesWithNoBindings(t *testing.T) {
	n := RenderSandboxNotes(&ProxyConfig{Passthrough: []string{"api.anthropic.com"}})
	if !strings.Contains(n, "No secret has an allowed destination yet") {
		t.Fatalf("empty bindings should be stated plainly:\n%s", n)
	}
	if strings.Contains(n, "anthropic") {
		t.Fatal("still must not name passthrough hosts")
	}
}
