/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"reflect"
	"testing"
)

const inspectFixture = `[
 {"Id":"aaa111","Config":{"Image":"doppler-agent-sandbox:c235ea2c15d2","Env":["PROXY_PORT=14331","HOME=/home/agent"]},
  "Mounts":[{"Type":"volume","Name":"doppler-agent-billing-home","Destination":"/home/agent"},{"Type":"bind","Source":"/Users/me/repo","Destination":"/workspace/repo"}]},
 {"Id":"bbb222","Config":{"Image":"doppler-agent-sandbox:c235ea2c15d2","Env":["PROXY_PORT=14330"]},
  "Mounts":[]},
 {"Id":"ccc333","Config":{"Image":"postgres:16","Env":["PROXY_PORT=14330","POSTGRES_PASSWORD=x"]},
  "Mounts":[{"Type":"volume","Name":"pgdata","Destination":"/var/lib/postgresql/data"}]},
 {"Id":"ddd444","Config":{"Image":"doppler-agent-sandbox:old","Env":["PROXY_PORT=14332"]},
  "Mounts":[{"Type":"volume","Name":"doppler-agent-billing-home","Destination":"/home/agent"}]}
]`

func TestParseInspect(t *testing.T) {
	got, err := parseInspect([]byte(inspectFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 containers, got %d", len(got))
	}
	want := ContainerInfo{ID: "aaa111", Image: "doppler-agent-sandbox:c235ea2c15d2", Env: []string{"PROXY_PORT=14331", "HOME=/home/agent"}, Volumes: []string{"doppler-agent-billing-home"}}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("first container = %+v, want %+v", got[0], want)
	}
	if len(got[1].Volumes) != 0 {
		t.Fatalf("a container with no volume mounts should have no volumes, got %v", got[1].Volumes)
	}
}

func TestMatchSandboxContainers(t *testing.T) {
	all, err := parseInspect([]byte(inspectFixture))
	if err != nil {
		t.Fatal(err)
	}

	// Port match catches a --fresh run (no home volume); volume match catches a run
	// from an older image. A non-sandbox image is never touched even if its port collides.
	got := MatchSandboxContainers(all, 14330, "doppler-agent-other-home")
	if !reflect.DeepEqual(got, []string{"bbb222"}) {
		t.Fatalf("port match = %v, want [bbb222]", got)
	}

	got = MatchSandboxContainers(all, 14331, "doppler-agent-billing-home")
	if !reflect.DeepEqual(got, []string{"aaa111", "ddd444"}) {
		t.Fatalf("port+volume match = %v, want [aaa111 ddd444]", got)
	}

	if got = MatchSandboxContainers(all, 15000, "doppler-agent-nobody-home"); len(got) != 0 {
		t.Fatalf("expected no match, got %v", got)
	}

	// An agent with no recorded port (0) only matches by volume.
	got = MatchSandboxContainers(all, 0, "doppler-agent-billing-home")
	if !reflect.DeepEqual(got, []string{"aaa111", "ddd444"}) {
		t.Fatalf("volume-only match = %v, want [aaa111 ddd444]", got)
	}
}

func TestPortOfListenAddress(t *testing.T) {
	cases := map[string]int{"127.0.0.1:14331": 14331, "0.0.0.0:14330": 14330, ":8080": 8080, "": 0, "nonsense": 0}
	for in, want := range cases {
		if got := PortOfListenAddress(in); got != want {
			t.Errorf("PortOfListenAddress(%q) = %d, want %d", in, got, want)
		}
	}
}
