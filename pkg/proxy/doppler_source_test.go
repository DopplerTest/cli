/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import "testing"

// Doppler's description of the config is never handed to the proxy as a secret, so it
// is neither masked in the agent's environment nor guarded or redacted in traffic.
func TestDopplerMetadataIsNotASecret(t *testing.T) {
	for _, name := range []string{"DOPPLER_PROJECT", "DOPPLER_CONFIG", "DOPPLER_ENVIRONMENT"} {
		if !dopplerMetadata[name] {
			t.Errorf("%s should be left out of the proxy's secrets", name)
		}
	}
	for _, name := range []string{"GITHUB_TOKEN", "DOPPLER_TOKEN", "DOPPLER_CONFIG_DIR"} {
		if dopplerMetadata[name] {
			t.Errorf("%s is not Doppler's config metadata and must stay a secret", name)
		}
	}
}
