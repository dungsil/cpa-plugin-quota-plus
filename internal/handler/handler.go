// Package handler implements every quota-plus RPC method behind one function
// that takes a method name and request JSON and returns envelope JSON.
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/dungsil/cpa-plugin-quota-plus/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// PluginID is the plugin identifier, host config key, and library file name.
const PluginID = "quota-plus"

// Version is the plugin version reported at registration. The host drops
// plugins whose metadata Version is blank.
const Version = "0.0.0-dev"

// HostCall invokes a host callback such as host.auth.list and returns the
// host envelope JSON.
type HostCall func(method string, request []byte) ([]byte, error)

// Handler dispatches RPC methods. Its only external dependencies are the host
// callback and the upstream HTTP transport, both injected at construction.
type Handler struct {
	host      HostCall
	transport http.RoundTripper
}

// New builds a Handler with the given host callback and upstream transport.
func New(host HostCall, transport http.RoundTripper) *Handler {
	return &Handler{host: host, transport: transport}
}

type registration struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  capabilities       `json:"capabilities"`
}

type capabilities struct {
	QuotaProvider bool `json:"quota_provider"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

// Handle runs one RPC method and always returns envelope JSON.
func (h *Handler) Handle(method string, request []byte) []byte {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return ok(registration{
			SchemaVersion: pluginabi.SchemaVersion,
			Metadata: pluginapi.Metadata{
				Name:             PluginID,
				Version:          Version,
				Author:           "dungsil",
				GitHubRepository: "https://github.com/dungsil/cpa-plugin-quota-plus",
			},
			Capabilities: capabilities{QuotaProvider: true},
		})
	case pluginabi.MethodPluginQuiesce, pluginabi.MethodPluginShutdown:
		return ok(struct{}{})
	case pluginabi.MethodQuotaIdentifier:
		return ok(identifierResponse{Identifier: PluginID})
	case pluginabi.MethodQuotaDescribe:
		return h.describe()
	case pluginabi.MethodQuotaReset:
		return fail("unsupported", "quota reset is not supported: providers have no upstream reset API")
	case pluginabi.MethodQuotaFetch:
		return fail("unsupported", "quota fetch is not supported yet")
	default:
		return fail("unknown_method", "unknown method: "+method)
	}
}

type authListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

func (h *Handler) describe() []byte {
	raw, err := h.host(pluginabi.MethodHostAuthList, nil)
	if err != nil {
		return fail("host_auth_list_failed", "host.auth.list failed: "+err.Error())
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fail("host_auth_list_failed", "host.auth.list returned an invalid envelope: "+err.Error())
	}
	if !env.OK {
		message := "host.auth.list failed"
		if env.Error != nil && env.Error.Message != "" {
			message += ": " + env.Error.Message
		}
		return fail("host_auth_list_failed", message)
	}
	var list authListResponse
	if err := json.Unmarshal(env.Result, &list); err != nil {
		return fail("host_auth_list_failed", "host.auth.list returned an invalid result: "+err.Error())
	}

	supported := []string{}
	seen := map[string]bool{}
	for _, entry := range list.Files {
		if entry.Provider == "" || seen[entry.Provider] {
			continue
		}
		if _, matched := provider.Match(providers, entry.BaseURL); matched {
			seen[entry.Provider] = true
			supported = append(supported, entry.Provider)
		}
	}
	return ok(pluginapi.QuotaDescribeResponse{
		SupportedProviders: supported,
		DisplayName:        "Quota Plus",
		SupportsReset:      false,
	})
}

func ok(result any) []byte {
	raw, err := json.Marshal(result)
	if err != nil {
		return fail("internal_error", "encode result: "+err.Error())
	}
	out, _ := json.Marshal(pluginabi.Envelope{OK: true, Result: raw})
	return out
}

func fail(code, message string) []byte {
	out, _ := pluginabi.NewErrorEnvelope(code, message)
	return out
}
