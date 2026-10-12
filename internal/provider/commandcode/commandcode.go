// Package commandcode identifies CommandCode API keys and fetches their quota.
package commandcode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/dungsil/cpa-plugin-quota-plus/internal/provider"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Provider is the CommandCode provider module.
type Provider struct{}

// ID returns the provider identifier used in error messages.
func (Provider) ID() string { return "commandcode" }

// DisplayName returns the quota group name.
func (Provider) DisplayName() string { return "CommandCode" }

// BaseURLs returns the official CommandCode URL.
func (Provider) BaseURLs() []string {
	return []string{"https://api.commandcode.ai/provider/v1"}
}

const creditsPath = "/alpha/billing/credits"
const subscriptionsPath = "/alpha/billing/subscriptions"
const maxResponseBytes = 1 << 20

type creditsResponse struct {
	WindowLimits struct {
		FiveHour *quotaWindow `json:"fiveHour"`
		Weekly   *quotaWindow `json:"weekly"`
	} `json:"windowLimits"`
	Credits struct {
		Monthly   *float64 `json:"monthlyCredits"`
		Purchased *float64 `json:"purchasedCredits"`
		Free      *float64 `json:"freeCredits"`
	} `json:"credits"`
}

type quotaWindow struct {
	Used     *float64 `json:"used"`
	Cap      *float64 `json:"cap"`
	Exceeded bool     `json:"exceeded"`
	ResetAt  *int64   `json:"resetAt"`
}

// Fetch reads required credits and optional subscription information.
func (Provider) Fetch(ctx context.Context, client *http.Client, apiKey string) (pluginapi.QuotaFetchResponse, error) {
	var upstream creditsResponse
	status, err := fetchJSON(ctx, client, apiKey, creditsPath, &upstream)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	result := pluginapi.QuotaFetchResponse{Groups: []pluginapi.QuotaGroup{{DisplayName: "CommandCode"}}}
	for _, period := range []struct {
		name   string
		window *quotaWindow
	}{{"5h", upstream.WindowLimits.FiveHour}, {"weekly", upstream.WindowLimits.Weekly}} {
		w := period.window
		if w == nil || w.Used == nil || w.Cap == nil || *w.Cap <= 0 {
			return pluginapi.QuotaFetchResponse{}, provider.UnexpectedResponseError("commandcode", creditsPath, status)
		}
		bucket := pluginapi.QuotaBucket{Window: period.name, RemainingFraction: provider.ClampRemainingFraction(1 - *w.Used / *w.Cap)}
		if w.ResetAt != nil {
			reset := time.UnixMilli(*w.ResetAt).UTC()
			if reset.Year() < 0 || reset.Year() > 9999 {
				return pluginapi.QuotaFetchResponse{}, provider.UnexpectedResponseError("commandcode", creditsPath, status)
			}
			bucket.ResetTime = reset.Format(time.RFC3339Nano)
		}
		if w.Exceeded {
			bucket.RemainingFraction = 0
			bucket.Description = "Limit reached"
		}
		result.Groups[0].Buckets = append(result.Groups[0].Buckets, bucket)
	}
	for _, metric := range []struct {
		key, label string
		value      *float64
	}{
		{"monthlyCredits", "Monthly credits", upstream.Credits.Monthly},
		{"purchasedCredits", "Purchased credits", upstream.Credits.Purchased},
		{"freeCredits", "Free credits", upstream.Credits.Free},
	} {
		if metric.value != nil {
			result.Summary = append(result.Summary, pluginapi.QuotaMetric{Key: metric.key, Label: metric.label, Value: *metric.value, Format: "number", Unit: "credits"})
		}
	}
	var subscription struct {
		Data struct {
			PlanID string `json:"planId"`
		} `json:"data"`
	}
	if _, err := fetchJSON(ctx, client, apiKey, subscriptionsPath, &subscription); err == nil && subscription.Data.PlanID != "" {
		result.Subscription = &pluginapi.QuotaSubscription{Plan: subscription.Data.PlanID}
	}
	return result, nil
}

func fetchJSON(ctx context.Context, client *http.Client, apiKey, path string, target any) (int, error) {
	response, err := provider.DoRequest(ctx, client, http.MethodGet, "https://api.commandcode.ai"+path, apiKey)
	if err != nil {
		return 0, provider.RequestError("commandcode", path)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, provider.HTTPError("commandcode", path, response.StatusCode, "", provider.RetryAfterSeconds(response.Header.Get("Retry-After")), apiKey)
	}
	limited := &io.LimitedReader{R: response.Body, N: maxResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(target); err != nil || limited.N == 0 {
		return response.StatusCode, provider.UnexpectedResponseError("commandcode", path, response.StatusCode)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || limited.N == 0 {
		return response.StatusCode, provider.UnexpectedResponseError("commandcode", path, response.StatusCode)
	}
	return response.StatusCode, nil
}

var _ provider.Fetcher = Provider{}
