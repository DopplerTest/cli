/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DopplerHQ/cli/pkg/models"
	"github.com/DopplerHQ/cli/pkg/proxy"
)

func TestRecordAgentWorkplaceFillsInAndSaves(t *testing.T) {
	dir := t.TempDir()
	p := &proxy.AgentProfile{Name: "old", Project: "proj", Config: "dev"}
	if err := proxy.SaveAgent(dir, p); err != nil {
		t.Fatal(err)
	}
	lookup := func() (models.ActorWorkplaceInfo, error) {
		return models.ActorWorkplaceInfo{Slug: "acme", Name: "Acme"}, nil
	}
	if err := recordAgentWorkplace(dir, p, lookup); err != nil {
		t.Fatalf("recordAgentWorkplace: %v", err)
	}
	got, err := proxy.LoadAgent(dir, "old")
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkplaceSlug != "acme" || got.WorkplaceName != "Acme" {
		t.Fatalf("saved workplace = %q %q, want acme/Acme", got.WorkplaceSlug, got.WorkplaceName)
	}
}

func TestRecordAgentWorkplaceLeavesARecordedOneAlone(t *testing.T) {
	dir := t.TempDir()
	p := &proxy.AgentProfile{Name: "demo", Project: "proj", Config: "dev", WorkplaceSlug: "acme", WorkplaceName: "Acme"}
	called := false
	lookup := func() (models.ActorWorkplaceInfo, error) {
		called = true
		return models.ActorWorkplaceInfo{Slug: "other", Name: "Other"}, nil
	}
	if err := recordAgentWorkplace(dir, p, lookup); err != nil {
		t.Fatalf("recordAgentWorkplace: %v", err)
	}
	if called || p.WorkplaceSlug != "acme" {
		t.Fatalf("a recorded workplace must not be looked up again; called=%v slug=%q", called, p.WorkplaceSlug)
	}
}

func TestRecordAgentWorkplaceLeavesTheProfileAloneWhenTheLookupFails(t *testing.T) {
	dir := t.TempDir()
	p := &proxy.AgentProfile{Name: "old", Project: "proj", Config: "dev"}
	if err := proxy.SaveAgent(dir, p); err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []func() (models.ActorWorkplaceInfo, error){
		func() (models.ActorWorkplaceInfo, error) { return models.ActorWorkplaceInfo{}, errors.New("offline") },
		func() (models.ActorWorkplaceInfo, error) { return models.ActorWorkplaceInfo{Name: "no slug"}, nil },
	} {
		if err := recordAgentWorkplace(dir, p, lookup); err == nil {
			t.Fatal("expected the failure to be reported")
		}
		if got, _ := proxy.LoadAgent(dir, "old"); got.WorkplaceSlug != "" || p.WorkplaceSlug != "" {
			t.Fatalf("a failed lookup must leave the workplace empty; got %q", got.WorkplaceSlug)
		}
	}
}

// A new agent's proxy accepts connections from this machine only.
func TestAgentCreateSetsALoopbackListenAddress(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/configs/config/tokens" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"token":{"name":"agent-proxy demo","key":"dp.st.new","slug":"new-slug"}}`)
	}))
	t.Cleanup(api.Close)
	configDir := t.TempDir()

	output, code := runAgentCommand(t, configDir, "create", "demo", "--project", "proj", "--config", "dev", "--token", "dp.pt.test", "--api-host", api.URL)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0. Output:\n%s", code, output)
	}
	want := fmt.Sprintf("127.0.0.1:%d", proxy.DefaultAgentPort)
	if got := proxy.ReadListenAddress(proxy.AgentConfigPath(proxy.AgentDir(configDir, "demo"))); got != want {
		t.Fatalf("listen_address = %q, want %q", got, want)
	}
}

func TestBackfillWorkplacesRecordsFromEachAgentsOwnToken(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []*proxy.AgentProfile{
		{Name: "recorded", Project: "proj", Config: "dev", WorkplaceSlug: "acme", WorkplaceName: "Acme"},
		{Name: "legacy", Project: "proj", Config: "dev"},
		{Name: "tokenless", Project: "proj", Config: "dev"},
		{Name: "failing", Project: "proj", Config: "dev"},
	} {
		if err := proxy.SaveAgent(dir, p); err != nil {
			t.Fatal(err)
		}
	}
	for name, token := range map[string]string{"recorded": "dp.st.acme", "legacy": "dp.st.other", "failing": "dp.st.gone"} {
		if err := proxy.WriteAgentToken(proxy.AgentDir(dir, name), token); err != nil {
			t.Fatal(err)
		}
	}
	var looked []string
	lookup := func(token string) (models.ActorWorkplaceInfo, error) {
		looked = append(looked, token)
		if token == "dp.st.other" {
			return models.ActorWorkplaceInfo{Slug: "other", Name: "Other"}, nil
		}
		return models.ActorWorkplaceInfo{}, errors.New("revoked")
	}
	agents, err := proxy.ListAgents(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := backfillWorkplaces(dir, agents, lookup)

	byName := map[string]proxy.AgentProfile{}
	for _, a := range got {
		byName[a.Name] = a
	}
	if byName["legacy"].WorkplaceSlug != "other" || byName["legacy"].WorkplaceName != "Other" {
		t.Fatalf("legacy agent not recorded in the listing: %+v", byName["legacy"])
	}
	if saved, _ := proxy.LoadAgent(dir, "legacy"); saved.WorkplaceSlug != "other" {
		t.Fatalf("legacy agent not saved: %+v", saved)
	}
	if byName["recorded"].WorkplaceSlug != "acme" || byName["failing"].WorkplaceSlug != "" || byName["tokenless"].WorkplaceSlug != "" {
		t.Fatalf("other agents changed: %+v", got)
	}
	// Only the agents that have a token and no workplace are looked up, each with its own token.
	if len(looked) != 2 || (looked[0] != "dp.st.other" && looked[1] != "dp.st.other") || (looked[0] != "dp.st.gone" && looked[1] != "dp.st.gone") {
		t.Fatalf("looked up %v, want the legacy and failing tokens only", looked)
	}
}
