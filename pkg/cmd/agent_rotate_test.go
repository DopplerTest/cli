/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package cmd

import (
	"errors"
	"testing"

	"github.com/DopplerHQ/cli/pkg/models"
	"github.com/DopplerHQ/cli/pkg/proxy"
)

func seedAgent(t *testing.T) (configDir string, p *proxy.AgentProfile) {
	t.Helper()
	configDir = t.TempDir()
	p = &proxy.AgentProfile{Name: "demo", Project: "proj", Config: "dev", Command: []string{"claude"}, TokenSlug: "old-slug"}
	if err := proxy.SaveAgent(configDir, p); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	if err := proxy.WriteAgentToken(proxy.AgentDir(configDir, "demo"), "dp.st.old"); err != nil {
		t.Fatalf("WriteAgentToken: %v", err)
	}
	return configDir, p
}

func TestRotateAgentTokenMintsThenRevokes(t *testing.T) {
	configDir, p := seedAgent(t)
	var revoked string
	mint := func() (models.ConfigServiceToken, error) {
		return models.ConfigServiceToken{Token: "dp.st.new", Slug: "new-slug", Name: "n", ExpiresAt: "2026-10-08T00:00:00Z"}, nil
	}
	revoke := func(slug string) error { revoked = slug; return nil }

	if err := rotateAgentToken(configDir, p, mint, revoke); err != nil {
		t.Fatalf("rotateAgentToken: %v", err)
	}

	if revoked != "old-slug" {
		t.Fatalf("revoked %q, want the old slug", revoked)
	}
	if tok := proxy.ReadAgentToken(proxy.AgentDir(configDir, "demo")); tok != "dp.st.new" {
		t.Fatalf("token file = %q, want the new token", tok)
	}
	saved, _ := proxy.LoadAgent(configDir, "demo")
	if saved.TokenSlug != "new-slug" || saved.TokenExpiresAt != "2026-10-08T00:00:00Z" {
		t.Fatalf("profile not updated: slug=%q expires=%q", saved.TokenSlug, saved.TokenExpiresAt)
	}
}

func TestRotateAgentTokenKeepsOldTokenWhenMintFails(t *testing.T) {
	configDir, p := seedAgent(t)
	revokeCalled := false
	mint := func() (models.ConfigServiceToken, error) { return models.ConfigServiceToken{}, errors.New("api down") }
	revoke := func(slug string) error { revokeCalled = true; return nil }

	if err := rotateAgentToken(configDir, p, mint, revoke); err == nil {
		t.Fatal("expected an error when minting fails")
	}
	if revokeCalled {
		t.Fatal("must not revoke the old token when minting fails")
	}
	if tok := proxy.ReadAgentToken(proxy.AgentDir(configDir, "demo")); tok != "dp.st.old" {
		t.Fatalf("token file = %q, want the old token untouched", tok)
	}
}
