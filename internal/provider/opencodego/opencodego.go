// Package opencodego identifies OpenCode Go API keys and fetches their quota.
package opencodego

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/dungsil/cpa-plugin-quota-plus/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Provider is the OpenCode Go provider module.
type Provider struct{}

// ID returns the provider identifier used in error messages.
func (Provider) ID() string { return "opencode-go" }

// DisplayName returns the quota group name.
func (Provider) DisplayName() string { return "OpenCode Go" }

// BaseURLs returns the official OpenCode Go URLs.
func (Provider) BaseURLs() []string {
	return []string{"https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go"}
}

const (
	usageEndpoint     = "https://opencode.ai/zen/go/v1/usage"
	usageEndpointPath = "/zen/go/v1/usage"
	maxResponseBytes = 1 << 20
)

type usageResponse struct {
	Usage *usageWindows `json:"usage"`
}

type usageWindows struct {
	Rolling *usageWindow `json:"rolling"`
	Weekly  *usageWindow `json:"weekly"`
	Monthly *usageWindow `json:"monthly"`
}

type usageWindow struct {
	Status   json.RawMessage `json:"status"`
	Percent  *float64        `json:"percent"`
	ResetsAt *string         `json:"resetsAt"`
}

type upstreamErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Fetch queries OpenCode Go usage and converts it to the host quota response.
func (Provider) Fetch(ctx context.Context, client *http.Client, apiKey string) (pluginapi.QuotaFetchResponse, error) {
	response, err := provider.DoRequest(ctx, client, http.MethodGet, usageEndpoint, apiKey)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, provider.RequestError("opencode-go", usageEndpointPath)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var upstream upstreamErrorResponse
		_ = json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&upstream)
		message := upstream.Error.Message
		if response.StatusCode == http.StatusForbidden && upstream.Error.Type == "EntitlementError" {
			message = "Go subscription required"
		}
		return pluginapi.QuotaFetchResponse{}, provider.HTTPError(
			"opencode-go",
			usageEndpointPath,
			response.StatusCode,
			message,
			provider.RetryAfterSeconds(response.Header.Get("Retry-After")),
			apiKey,
		)
	}

	var upstream usageResponse
	limited := &io.LimitedReader{R: response.Body, N: maxResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(&upstream); err != nil || limited.N == 0 {
		return pluginapi.QuotaFetchResponse{}, provider.UnexpectedResponseError("opencode-go", usageEndpointPath, response.StatusCode)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || limited.N == 0 {
		return pluginapi.QuotaFetchResponse{}, provider.UnexpectedResponseError("opencode-go", usageEndpointPath, response.StatusCode)
	}

	buckets, ok := convertWindows(upstream.Usage)
	if !ok {
		return pluginapi.QuotaFetchResponse{}, provider.UnexpectedResponseError("opencode-go", usageEndpointPath, response.StatusCode)
	}
	return pluginapi.QuotaFetchResponse{
		Subscription: &pluginapi.QuotaSubscription{Plan: "Go"},
		Groups: []pluginapi.QuotaGroup{{
			DisplayName: "OpenCode Go",
			Buckets:     buckets,
		}},
	}, nil
}

func convertWindows(windows *usageWindows) ([]pluginapi.QuotaBucket, bool) {
	if windows == nil || windows.Rolling == nil || windows.Weekly == nil || windows.Monthly == nil {
		return nil, false
	}
	periods := []struct {
		name   string
		window *usageWindow
	}{
		{"5h", windows.Rolling},
		{"weekly", windows.Weekly},
		{"monthly", windows.Monthly},
	}
	buckets := make([]pluginapi.QuotaBucket, 0, len(periods))
	for _, period := range periods {
		quota, ok := convertWindow(period.name, period.window)
		if !ok {
			return nil, false
		}
		buckets = append(buckets, quota)
	}
	return buckets, true
}

func convertWindow(name string, window *usageWindow) (pluginapi.QuotaBucket, bool) {
	if window.Percent == nil || window.ResetsAt == nil {
		return pluginapi.QuotaBucket{}, false
	}
	status := "ok"
	if len(window.Status) != 0 {
		var parsed *string
		if err := json.Unmarshal(window.Status, &parsed); err != nil || parsed == nil {
			return pluginapi.QuotaBucket{}, false
		}
		status = *parsed
		if status != "ok" && status != "rate-limited" {
			return pluginapi.QuotaBucket{}, false
		}
	}
	resetTime, err := provider.ResetTimeUTC(*window.ResetsAt)
	if err != nil {
		return pluginapi.QuotaBucket{}, false
	}
	bucket := pluginapi.QuotaBucket{
		Window:            name,
		RemainingFraction: provider.ClampRemainingFraction(1 - *window.Percent/100),
		ResetTime:         resetTime,
	}
	if status == "rate-limited" {
		bucket.RemainingFraction = 0
		bucket.Description = "Limit reached"
	}
	return bucket, true
}

var _ provider.Fetcher = Provider{}
