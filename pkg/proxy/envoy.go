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

	agentproxy "github.com/DopplerHQ/agent-proxy"
	envoyengine "github.com/DopplerHQ/agent-proxy/envoy"
)

// init registers the "envoy" engine: the same masked-hash injection as
// "masked-hash", but delivered by an Envoy data plane (CONNECT termination + MITM
// via SDS-minted certs + an ext_proc service). The CLI hosts the ext_proc and SDS
// gRPC services in-process — so the Doppler secrets never leave this process — and
// launches Envoy as a child. Selected with `doppler proxy start --engine envoy`.
func init() {
	Register("envoy", func(opts Options) (Engine, error) {
		signing := make([]envoyengine.SigningRoute, 0, len(opts.Signing))
		for _, s := range opts.Signing {
			signing = append(signing, envoyengine.SigningRoute{Host: s.Host, Service: s.Service, Region: s.Region})
		}
		oauth := make([]envoyengine.OAuthRouteConfig, 0, len(opts.OAuth))
		for _, o := range opts.OAuth {
			oauth = append(oauth, envoyengine.OAuthRouteConfig{
				Host:            o.Host,
				TokenEndpoint:   o.TokenEndpoint,
				ClientID:        o.ClientID,
				ClientSecretRef: o.ClientSecretRef,
				Scopes:          o.Scopes,
			})
		}
		return envoyengine.NewRuntime(envoyengine.RuntimeConfig{
			ListenAddr:   opts.ListenAddr,
			DataDir:      opts.DataDir,
			AgentEnvPath: opts.AgentEnvPath,
			Secrets:      envoySource{opts.Secrets},
			LogWriter:    opts.LogWriter,
			// Tier-1 hosts are resolved from the secret values (token-shape match)
			// inside the runtime. These extras are the tier-2/3 escape hatch: the
			// user's `intercept:` list from doppler-proxy.yaml (+ --intercept flag).
			ExtraInterceptHosts: opts.InterceptHosts,
			Signing:             signing,
			OAuth:               oauth,
		})
	})
}

// envoySource bridges an agentproxy.SecretSource to the envoy package's
// SecretSource. The method sets are identical, but each package declares its own
// SecretRef type, so Go's structural typing needs this thin adapter.
type envoySource struct{ src agentproxy.SecretSource }

func (a envoySource) List(ctx context.Context) ([]string, error) { return a.src.List(ctx) }
func (a envoySource) Fetch(ctx context.Context, ref envoyengine.SecretRef) (string, error) {
	return a.src.Fetch(ctx, agentproxy.SecretRef{Name: ref.Name})
}
