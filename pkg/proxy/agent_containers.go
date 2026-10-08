/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// Finding an agent's running sandbox. The sandbox runs the container without a name
// or label, so it is recognised by what docker does stamp on it: the image family,
// the PROXY_PORT the agent was given (unique per agent, present even for --fresh runs
// with no home volume), and the agent's home volume when it has one.
const sandboxImagePrefix = "doppler-agent-sandbox"

type ContainerInfo struct {
	ID      string
	Image   string
	Env     []string
	Volumes []string
}

// PortOfListenAddress returns the port of a host:port listen address, or 0 if it has none.
func PortOfListenAddress(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return n
}

// MatchSandboxContainers returns the IDs of sandbox containers that belong to the agent
// with this proxy port or home volume. Containers from other images are never matched,
// even if their environment happens to carry the same port.
func MatchSandboxContainers(all []ContainerInfo, port int, homeVolume string) []string {
	var ids []string
	portEnv := fmt.Sprintf("PROXY_PORT=%d", port)
	for _, c := range all {
		if !strings.HasPrefix(c.Image, sandboxImagePrefix) {
			continue
		}
		byPort := port != 0 && contains(c.Env, portEnv)
		byVolume := homeVolume != "" && contains(c.Volumes, homeVolume)
		if byPort || byVolume {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ListSandboxContainers inspects every running container. The docker CLI is used,
// like the rest of the sandbox code, so podman and friends work through --docker.
func ListSandboxContainers(ctx context.Context, dockerBin string) ([]ContainerInfo, error) {
	out, err := exec.CommandContext(ctx, dockerBin, "ps", "-q").Output()
	if err != nil {
		return nil, fmt.Errorf("%s ps: %w", dockerBin, err)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil, nil
	}
	raw, err := exec.CommandContext(ctx, dockerBin, append([]string{"inspect"}, ids...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s inspect: %w", dockerBin, err)
	}
	return parseInspect(raw)
}

func parseInspect(raw []byte) ([]ContainerInfo, error) {
	var entries []struct {
		ID     string `json:"Id"`
		Config struct {
			Image string   `json:"Image"`
			Env   []string `json:"Env"`
		} `json:"Config"`
		Mounts []struct {
			Type string `json:"Type"`
			Name string `json:"Name"`
		} `json:"Mounts"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("unexpected docker inspect output: %w", err)
	}
	infos := make([]ContainerInfo, 0, len(entries))
	for _, e := range entries {
		info := ContainerInfo{ID: e.ID, Image: e.Config.Image, Env: e.Config.Env, Volumes: []string{}}
		for _, m := range e.Mounts {
			if m.Type == "volume" && m.Name != "" {
				info.Volumes = append(info.Volumes, m.Name)
			}
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// StopContainers asks each container to exit (SIGTERM, then SIGKILL after the grace
// period). The agent inside gets a chance to shut down and `agent run` returns normally,
// which is what lets the run record close with a real exit code.
func StopContainers(ctx context.Context, dockerBin string, ids []string, graceSeconds int) error {
	if len(ids) == 0 {
		return nil
	}
	args := append([]string{"stop", "-t", strconv.Itoa(graceSeconds)}, ids...)
	if out, err := exec.CommandContext(ctx, dockerBin, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s stop: %w: %s", dockerBin, err, strings.TrimSpace(string(out)))
	}
	return nil
}
