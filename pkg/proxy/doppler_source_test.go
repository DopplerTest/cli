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
	"testing"

	"github.com/DopplerHQ/cli/pkg/models"
)

func computed(value string) models.ComputedSecret {
	return models.ComputedSecret{ComputedValue: &value}
}

func TestBrokerableSecretsSkipsDopplerMetadata(t *testing.T) {
	got := brokerableSecrets(map[string]models.ComputedSecret{
		"API_KEY":             computed("real-api-key"),
		"DOPPLER_PROJECT":     computed("mikes-test-proj"),
		"DOPPLER_CONFIG":      computed("dev"),
		"DOPPLER_ENVIRONMENT": computed("dev"),
	})

	if _, ok := got["API_KEY"]; !ok {
		t.Fatal("a real secret must still be brokered")
	}
	for _, name := range []string{"DOPPLER_PROJECT", "DOPPLER_CONFIG", "DOPPLER_ENVIRONMENT"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s is Doppler metadata and must not be masked", name)
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected only the real secret, got %v", got)
	}
}

// A secret with no computed value has nothing to mask.
func TestBrokerableSecretsSkipsNilValues(t *testing.T) {
	got := brokerableSecrets(map[string]models.ComputedSecret{"EMPTY": {}})
	if len(got) != 0 {
		t.Fatalf("expected no secrets, got %v", got)
	}
}
