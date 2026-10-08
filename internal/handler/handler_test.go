package handler_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/dungsil/cpa-plugin-quota-plus/internal/handler"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func noHost(string, []byte) ([]byte, error) {
	return nil, errors.New("host must not be called")
}

func call(t *testing.T, h *handler.Handler, method string, request string) envelope {
	t.Helper()
	raw := h.Handle(method, []byte(request))
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%s: response is not an envelope: %v (%s)", method, err, raw)
	}
	return env
}

func mustOK(t *testing.T, env envelope, method string) {
	t.Helper()
	if !env.OK || env.Error != nil {
		t.Fatalf("%s: expected ok envelope, got %+v", method, env)
	}
}

func mustFail(t *testing.T, env envelope, method string) {
	t.Helper()
	if env.OK || env.Error == nil || env.Error.Code == "" || env.Error.Message == "" {
		t.Fatalf("%s: expected error envelope with code and message, got %+v", method, env)
	}
}

func TestRegisterDeclaresOnlyQuotaProviderWithoutConfigFields(t *testing.T) {
	h := handler.New(noHost, http.DefaultTransport)
	for _, method := range []string{"plugin.register", "plugin.reconfigure"} {
		env := call(t, h, method, `{"config_yaml":"ZW5hYmxlZDogdHJ1ZQo=","schema_version":6}`)
		mustOK(t, env, method)

		var reg struct {
			SchemaVersion uint32                     `json:"schema_version"`
			Metadata      map[string]json.RawMessage `json:"metadata"`
			Capabilities  map[string]any             `json:"capabilities"`
		}
		if err := json.Unmarshal(env.Result, &reg); err != nil {
			t.Fatalf("%s: decode registration: %v", method, err)
		}
		// The host rejects registrations newer than its own schema version.
		if reg.SchemaVersion == 0 || reg.SchemaVersion > pluginabi.SchemaVersion {
			t.Errorf("%s: schema_version = %d, want 1..%d", method, reg.SchemaVersion, pluginabi.SchemaVersion)
		}
		var name string
		if err := json.Unmarshal(reg.Metadata["Name"], &name); err != nil || name != "quota-plus" {
			t.Errorf("%s: metadata Name = %s, want quota-plus", method, reg.Metadata["Name"])
		}
		// The host drops plugins whose Version, Author, or GitHubRepository is blank.
		for _, field := range []string{"Version", "Author", "GitHubRepository"} {
			var value string
			if err := json.Unmarshal(reg.Metadata[field], &value); err != nil || strings.TrimSpace(value) == "" {
				t.Errorf("%s: metadata %s = %s, want non-empty", method, field, reg.Metadata[field])
			}
		}
		if fields, ok := reg.Metadata["ConfigFields"]; ok && string(fields) != "null" && string(fields) != "[]" {
			t.Errorf("%s: ConfigFields = %s, want none", method, fields)
		}
		enabled := []string{}
		for key, value := range reg.Capabilities {
			if value == true {
				enabled = append(enabled, key)
			}
		}
		if len(enabled) != 1 || enabled[0] != "quota_provider" {
			t.Errorf("%s: enabled capabilities = %v, want only quota_provider", method, enabled)
		}
	}
}

func TestQuiesceAndShutdownSucceed(t *testing.T) {
	h := handler.New(noHost, http.DefaultTransport)
	for _, method := range []string{"plugin.quiesce", "plugin.shutdown"} {
		mustOK(t, call(t, h, method, `{}`), method)
	}
}

func TestQuotaIdentifierIsPluginID(t *testing.T) {
	h := handler.New(noHost, http.DefaultTransport)
	env := call(t, h, "quota.identifier", `{}`)
	mustOK(t, env, "quota.identifier")
	var resp struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil || resp.Identifier != "quota-plus" {
		t.Fatalf("identifier = %s, want quota-plus", env.Result)
	}
}

type authEntry struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url,omitempty"`
}

// hostWithAuths answers host.auth.list with the given entries, as the host
// does, and rejects any other callback.
func hostWithAuths(t *testing.T, entries ...authEntry) handler.HostCall {
	return func(method string, request []byte) ([]byte, error) {
		if method != "host.auth.list" {
			t.Errorf("unexpected host callback %q", method)
			return nil, errors.New("unexpected callback")
		}
		result, err := json.Marshal(map[string]any{"files": entries})
		if err != nil {
			t.Fatal(err)
		}
		return json.Marshal(map[string]any{"ok": true, "result": json.RawMessage(result)})
	}
}

type describeResult struct {
	SupportedProviders []string `json:"supported_providers"`
	DisplayName        string   `json:"display_name"`
	SupportsReset      bool     `json:"supports_reset"`
}

func describe(t *testing.T, host handler.HostCall) describeResult {
	t.Helper()
	env := call(t, handler.New(host, http.DefaultTransport), "quota.describe", `{"plugin":{"Name":"quota-plus"}}`)
	mustOK(t, env, "quota.describe")
	var res describeResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("decode describe: %v", err)
	}
	return res
}

func TestDescribeMatchesOnlyOfficialURLs(t *testing.T) {
	cases := []struct {
		baseURL string
		match   bool
	}{
		{"https://opencode.ai/zen/go/v1", true},
		{"https://opencode.ai/zen/go", true},
		{"https://ollama.com/api", true},
		{"https://ollama.com/v1", true},
		{"https://api.commandcode.ai/provider/v1", true},
		{"https://ollama.com/v1/", true},
		{"https://OpenCode.AI/zen/go/v1", true},
		{"https://API.CommandCode.ai/provider/v1/", true},
		{"http://ollama.com/v1", false},
		{"https://opencode.ai/zen/v1", false},
		{"http://localhost:11434/v1", false},
		{"https://ollama.com/v1/chat", false},
		{"https://opencode.ai/zen/go/v1/usage", false},
		{"https://ollama.com", false},
		{"https://ollama.com/V1", false},
		{"https://ollama.com:8443/v1", false},
		{"https://ollama.com/v1?x=1", false},
		{"https://ollama.com.evil.example/v1", false},
		{"https://apİ.commandcode.ai/provider/v1", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.baseURL, func(t *testing.T) {
			res := describe(t, hostWithAuths(t, authEntry{Provider: "openai-compatible-key", BaseURL: tc.baseURL}))
			got := len(res.SupportedProviders) == 1 && res.SupportedProviders[0] == "openai-compatible-key"
			if got != tc.match || (!tc.match && len(res.SupportedProviders) != 0) {
				t.Fatalf("base URL %q: supported_providers = %v, want match=%v", tc.baseURL, res.SupportedProviders, tc.match)
			}
		})
	}
}

func TestDescribeListsEachOfficialProviderKeyOnce(t *testing.T) {
	res := describe(t, hostWithAuths(t,
		authEntry{Provider: "openai-compatible-opencode-go", BaseURL: "https://opencode.ai/zen/go/v1"},
		authEntry{Provider: "openai-compatible-zen", BaseURL: "https://opencode.ai/zen/v1"},
		authEntry{Provider: "openai-compatible-ollama", BaseURL: "https://ollama.com/v1"},
		authEntry{Provider: "openai-compatible-opencode-go", BaseURL: "https://opencode.ai/zen/go"},
		authEntry{Provider: "openai-compatible-local", BaseURL: "http://localhost:11434/v1"},
		authEntry{Provider: "claude"},
		authEntry{Provider: "openai-compatible-ollama", BaseURL: "https://ollama.com/api/"},
		authEntry{Provider: "openai-compatible-cc", BaseURL: "https://api.commandcode.ai/provider/v1"},
	))
	want := map[string]bool{
		"openai-compatible-opencode-go": true,
		"openai-compatible-ollama":      true,
		"openai-compatible-cc":          true,
	}
	if len(res.SupportedProviders) != len(want) {
		t.Fatalf("supported_providers = %v, want each of %v once", res.SupportedProviders, want)
	}
	for _, key := range res.SupportedProviders {
		if !want[key] {
			t.Fatalf("supported_providers = %v, unexpected %q", res.SupportedProviders, key)
		}
		delete(want, key)
	}
	if res.DisplayName != "Quota Plus" || res.SupportsReset {
		t.Fatalf("display_name = %q, supports_reset = %v; want Quota Plus, false", res.DisplayName, res.SupportsReset)
	}
}

func TestDescribeWithoutOfficialCredentialsIsEmpty(t *testing.T) {
	res := describe(t, hostWithAuths(t,
		authEntry{Provider: "openai-compatible-zen", BaseURL: "https://opencode.ai/zen/v1"},
		authEntry{Provider: "codex"},
	))
	if len(res.SupportedProviders) != 0 || res.DisplayName != "Quota Plus" || res.SupportsReset {
		t.Fatalf("describe = %+v, want empty providers, Quota Plus, no reset", res)
	}
}

func TestDescribeFailsWhenHostAuthListFails(t *testing.T) {
	failures := map[string]handler.HostCall{
		"callback error": func(string, []byte) ([]byte, error) {
			return nil, errors.New("host unavailable")
		},
		"error envelope": func(string, []byte) ([]byte, error) {
			return []byte(`{"ok":false,"error":{"code":"host_call_failed","message":"auth manager unavailable"}}`), nil
		},
		"malformed response": func(string, []byte) ([]byte, error) {
			return []byte(`not json`), nil
		},
	}
	for name, host := range failures {
		t.Run(name, func(t *testing.T) {
			mustFail(t, call(t, handler.New(host, http.DefaultTransport), "quota.describe", `{}`), "quota.describe")
		})
	}
}

func TestQuotaResetIsNeverSupported(t *testing.T) {
	h := handler.New(noHost, http.DefaultTransport)
	request := `{"auth_index":"1","provider":"openai-compatible-ollama","attributes":{"base_url":"https://ollama.com/v1","api_key":"fake-test-key"}}`
	env := call(t, h, "quota.reset", request)
	mustFail(t, env, "quota.reset")
	if !strings.Contains(env.Error.Message, "not supported") {
		t.Fatalf("reset error = %q, want it to say reset is not supported", env.Error.Message)
	}
	if strings.Contains(env.Error.Message, "fake-test-key") {
		t.Fatalf("reset error leaks the API key: %q", env.Error.Message)
	}
}

func TestUnsupportedMethodsReturnErrorEnvelopes(t *testing.T) {
	h := handler.New(noHost, http.DefaultTransport)
	for _, method := range []string{"executor.execute", "quota.unknown", ""} {
		mustFail(t, call(t, h, method, `{}`), method)
	}
}
