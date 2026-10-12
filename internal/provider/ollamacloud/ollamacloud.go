// Package ollamacloud identifies Ollama Cloud API keys and fetches their quota.
package ollamacloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/dungsil/cpa-plugin-quota-plus/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Provider is the Ollama Cloud provider module.
type Provider struct{}

// ID returns the provider identifier used in error messages.
func (Provider) ID() string { return "ollama-cloud" }

// DisplayName returns the quota group name.
func (Provider) DisplayName() string { return "Ollama Cloud" }

// BaseURLs returns the official Ollama Cloud URLs.
func (Provider) BaseURLs() []string {
	return []string{"https://ollama.com/api", "https://ollama.com/v1"}
}

const (
	usageEndpoint     = "https://ollama.com/api/usage"
	usageEndpointPath = "/api/usage"
	maxResponseBytes  = 1 << 20
)

type usageResponse struct {
	Limits   *usageWindows `json:"limits"`
	Activity *struct {
		Cost *float64 `json:"cost"`
	} `json:"activity"`
}

type usageWindows struct {
	Session *usageWindow `json:"session"`
	Weekly  *usageWindow `json:"weekly"`
	Monthly *usageWindow `json:"monthly"`
}

type usageWindow struct {
	Usage *float64 `json:"usage"`
}

// Fetch queries Ollama Cloud usage and converts it to the host quota response.
func (Provider) Fetch(ctx context.Context, client *http.Client, apiKey string) (pluginapi.QuotaFetchResponse, error) {
	response, err := provider.DoRequest(ctx, client, http.MethodGet, usageEndpoint, apiKey)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, provider.RequestError("ollama-cloud", usageEndpointPath)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return pluginapi.QuotaFetchResponse{}, provider.HTTPError(
			"ollama-cloud", usageEndpointPath, response.StatusCode, "",
			provider.RetryAfterSeconds(response.Header.Get("Retry-After")), apiKey,
		)
	}

	var upstream usageResponse
	limited := &io.LimitedReader{R: response.Body, N: maxResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(&upstream); err != nil || limited.N == 0 {
		return pluginapi.QuotaFetchResponse{}, provider.UnexpectedResponseError("ollama-cloud", usageEndpointPath, response.StatusCode)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || limited.N == 0 {
		return pluginapi.QuotaFetchResponse{}, provider.UnexpectedResponseError("ollama-cloud", usageEndpointPath, response.StatusCode)
	}
	buckets, ok := convertWindows(upstream.Limits)
	if !ok {
		return pluginapi.QuotaFetchResponse{}, provider.UnexpectedResponseError("ollama-cloud", usageEndpointPath, response.StatusCode)
	}
	result := pluginapi.QuotaFetchResponse{
		Groups: []pluginapi.QuotaGroup{{DisplayName: "Ollama Cloud", Buckets: buckets}},
	}
	if upstream.Activity != nil && upstream.Activity.Cost != nil {
		result.Summary = []pluginapi.QuotaMetric{{
			Key: "cost", Label: "Cost this period", Value: *upstream.Activity.Cost,
			Format: "currency", Currency: "USD",
		}}
	}
	return result, nil
}

func convertWindows(windows *usageWindows) ([]pluginapi.QuotaBucket, bool) {
	if windows == nil {
		return nil, false
	}
	periods := []struct {
		name   string
		window *usageWindow
	}{
		{"5h", windows.Session},
		{"weekly", windows.Weekly},
		{"monthly", windows.Monthly},
	}
	buckets := make([]pluginapi.QuotaBucket, 0, len(periods))
	for _, period := range periods {
		if period.window == nil {
			continue
		}
		if period.window.Usage == nil {
			return nil, false
		}
		buckets = append(buckets, pluginapi.QuotaBucket{
			Window:            period.name,
			RemainingFraction: provider.ClampRemainingFraction(1 - *period.window.Usage),
		})
	}
	return buckets, len(buckets) != 0
}

var _ provider.Fetcher = Provider{}
