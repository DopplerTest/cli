/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"os"
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

func TestParseMount(t *testing.T) {
	dir := t.TempDir()

	host, ro, err := ParseMount(dir)
	if err != nil || host != dir || ro {
		t.Fatalf("ParseMount(%q) = (%q, %v, %v)", dir, host, ro, err)
	}

	host, ro, err = ParseMount(dir + ":ro")
	if err != nil || host != dir || !ro {
		t.Fatalf("ParseMount(:ro) = (%q, %v, %v)", host, ro, err)
	}

	if _, _, err := ParseMount(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Fatal("ParseMount of a missing path should error")
	}

	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseMount(file); err == nil {
		t.Fatal("ParseMount of a file (not a directory) should error")
	}
}

func TestNextAgentPortFillsTheLowestGap(t *testing.T) {
	configDir := t.TempDir()
	seed := func(name string, port int) {
		if err := SaveAgent(configDir, &AgentProfile{Name: name, Project: "p", Config: "c"}); err != nil {
			t.Fatal(err)
		}
		path := AgentConfigPath(AgentDir(configDir, name))
		if _, _, err := LoadOrScaffold(path); err != nil {
			t.Fatal(err)
		}
		if err := SetListenAddress(path, "0.0.0.0:"+strconv.Itoa(port)); err != nil {
			t.Fatal(err)
		}
	}

	if p := NextAgentPort(configDir); p != DefaultAgentPort {
		t.Fatalf("with no agents, NextAgentPort = %d, want %d", p, DefaultAgentPort)
	}

	seed("a", DefaultAgentPort)
	seed("c", DefaultAgentPort+2)
	if p := NextAgentPort(configDir); p != DefaultAgentPort+1 {
		t.Fatalf("NextAgentPort should fill the gap at %d, got %d", DefaultAgentPort+1, p)
	}

	seed("b", DefaultAgentPort+1)
	if p := NextAgentPort(configDir); p != DefaultAgentPort+3 {
		t.Fatalf("NextAgentPort should return the next free port %d, got %d", DefaultAgentPort+3, p)
	}
}

func TestListenAddressRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := LoadOrScaffold(path); err != nil {
		t.Fatal(err)
	}
	if err := SetListenAddress(path, "0.0.0.0:14330"); err != nil {
		t.Fatal(err)
	}
	if got := ReadListenAddress(path); got != "0.0.0.0:14330" {
		t.Fatalf("ReadListenAddress = %q, want the value just set", got)
	}
}
