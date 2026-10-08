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
	"testing"
)

func TestAddMountValidatesAndDedupes(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	docs := filepath.Join(dir, "docs")
	for _, d := range []string{repo, docs} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := &AgentProfile{Name: "a"}

	if spec, err := AddMount(p, repo); err != nil || spec != repo {
		t.Fatalf("add: spec=%q err=%v", spec, err)
	}
	if spec, err := AddMount(p, docs+":ro"); err != nil || spec != docs+":ro" {
		t.Fatalf("add ro: spec=%q err=%v", spec, err)
	}
	if !reflect.DeepEqual(p.Mounts, []string{repo, docs + ":ro"}) {
		t.Fatalf("mounts = %v", p.Mounts)
	}

	// Adding the same directory again replaces its entry, so :ro can be toggled.
	if _, err := AddMount(p, repo+":ro"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Mounts, []string{repo + ":ro", docs + ":ro"}) {
		t.Fatalf("after toggle = %v", p.Mounts)
	}

	if _, err := AddMount(p, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a directory that does not exist must be refused")
	}
}

func TestRemoveMountMatchesWithOrWithoutSuffix(t *testing.T) {
	p := &AgentProfile{Name: "a", Mounts: []string{"/x/repo:ro", "/x/docs"}}
	if !RemoveMount(p, "/x/repo") {
		t.Fatal("expected to remove /x/repo regardless of its :ro suffix")
	}
	if !reflect.DeepEqual(p.Mounts, []string{"/x/docs"}) {
		t.Fatalf("mounts = %v", p.Mounts)
	}
	if RemoveMount(p, "/x/nothing") {
		t.Fatal("removing an unknown path must report false")
	}
	if !RemoveMount(p, "/x/docs:ro") {
		t.Fatal("a :ro suffix on the argument should still match the plain entry")
	}
	if len(p.Mounts) != 0 {
		t.Fatalf("expected no mounts, got %v", p.Mounts)
	}
}
