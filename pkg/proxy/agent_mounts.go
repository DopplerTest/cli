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

import "strings"

// Editing an agent's directories after creation. A mount is stored as the absolute
// host path, with ":ro" appended when it is read-only, the same shape `agent create
// --mount` writes. Changes take effect the next time the agent runs; a running sandbox
// keeps the mounts it started with.

// MountHostPath returns the host path of a stored mount spec, without any :ro/:rw.
func MountHostPath(spec string) string {
	return strings.TrimSuffix(strings.TrimSuffix(spec, ":ro"), ":rw")
}

// AddMount validates spec the way `agent create --mount` does and adds it to the
// profile. An existing entry for the same directory is replaced, so adding it again
// with or without :ro changes its access. Returns the stored spec.
func AddMount(p *AgentProfile, spec string) (string, error) {
	host, ro, err := ParseMount(spec)
	if err != nil {
		return "", err
	}
	stored := host
	if ro {
		stored += ":ro"
	}
	for i, m := range p.Mounts {
		if MountHostPath(m) == host {
			p.Mounts[i] = stored
			return stored, nil
		}
	}
	p.Mounts = append(p.Mounts, stored)
	return stored, nil
}

// RemoveMount removes the mount for hostPath, matching with or without an access
// suffix on either side, and reports whether anything was removed.
func RemoveMount(p *AgentProfile, hostPath string) bool {
	want := MountHostPath(hostPath)
	kept := p.Mounts[:0]
	removed := false
	for _, m := range p.Mounts {
		if MountHostPath(m) == want {
			removed = true
			continue
		}
		kept = append(kept, m)
	}
	p.Mounts = kept
	return removed
}
