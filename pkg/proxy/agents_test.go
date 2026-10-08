/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import "testing"

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
