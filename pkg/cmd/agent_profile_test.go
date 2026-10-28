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
