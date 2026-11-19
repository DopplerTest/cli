/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestValidateAgentName(t *testing.T) {
	valid := []string{"a", "a1", "backend-claude", "a_b", "0abc", "x-_-x"}
	for _, n := range valid {
		if err := ValidateAgentName(n); err != nil {
			t.Errorf("ValidateAgentName(%q) should be valid, got %v", n, err)
		}
	}
	invalid := []string{"", "A", "-a", "_a", "a b", "a/b", "a.b", string(make([]byte, 65))}
	for _, n := range invalid {
		if err := ValidateAgentName(n); err == nil {
			t.Errorf("ValidateAgentName(%q) should be rejected", n)
		}
	}
}

func TestNextAgentPortFillsTheLowestGap(t *testing.T) {
	configDir := t.TempDir()
	seed := func(name, host string, port int) {
		if err := SaveAgent(configDir, &AgentProfile{Name: name, Project: "p", Config: "c"}); err != nil {
			t.Fatal(err)
		}
		path := AgentConfigPath(AgentDir(configDir, name))
		if _, _, err := LoadOrScaffold(path); err != nil {
			t.Fatal(err)
		}
		if err := SetListenAddress(path, host+":"+strconv.Itoa(port)); err != nil {
			t.Fatal(err)
		}
	}

	if p := NextAgentPort(configDir); p != DefaultAgentPort {
		t.Fatalf("with no agents, NextAgentPort = %d, want %d", p, DefaultAgentPort)
	}

	// An agent made by an older version listens on every address. Its port is taken
	// all the same.
	seed("a", "0.0.0.0", DefaultAgentPort)
	seed("c", "127.0.0.1", DefaultAgentPort+2)
	if p := NextAgentPort(configDir); p != DefaultAgentPort+1 {
		t.Fatalf("NextAgentPort should fill the gap at %d, got %d", DefaultAgentPort+1, p)
	}

	seed("b", "127.0.0.1", DefaultAgentPort+1)
	if p := NextAgentPort(configDir); p != DefaultAgentPort+3 {
		t.Fatalf("NextAgentPort should return the next free port %d, got %d", DefaultAgentPort+3, p)
	}
}

func TestListenAddressRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := LoadOrScaffold(path); err != nil {
		t.Fatal(err)
	}
	// A wider bind than the loopback default is kept as the user wrote it.
	for _, address := range []string{"127.0.0.1:14330", "0.0.0.0:14330"} {
		if err := SetListenAddress(path, address); err != nil {
			t.Fatal(err)
		}
		if got := ReadListenAddress(path); got != address {
			t.Fatalf("ReadListenAddress = %q, want the value just set, %q", got, address)
		}
	}
}
