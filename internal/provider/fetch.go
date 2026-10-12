package provider

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Fetcher is a provider that can query and normalize upstream quota data.
type Fetcher interface {
	Provider
	Fetch(context.Context, *http.Client, string) (pluginapi.QuotaFetchResponse, error)
}

// ClampRemainingFraction keeps a normalized quota value within the host's 0..1 range.
func ClampRemainingFraction(value float64) float64 {
	if math.IsNaN(value) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// ResetTimeUTC parses an ISO 8601 timestamp and returns its UTC RFC3339 form.
func ResetTimeUTC(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}
