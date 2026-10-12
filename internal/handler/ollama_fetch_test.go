package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dungsil/cpa-plugin-quota-plus/internal/handler"
)

const ollamaKey = "ollama-test-key-do-not-leak"

func newOllamaHandler(t *testing.T, serve http.HandlerFunc) *handler.Handler {
	t.Helper()
	server := httptest.NewServer(serve)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return handler.New(noHost, roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "ollama.com" {
			return nil, fmt.Errorf("unexpected upstream URL")
		}
		copy := request.Clone(request.Context())
		copy.URL.Scheme = target.Scheme
		copy.URL.Host = target.Host
		copy.Host = target.Host
		return http.DefaultTransport.RoundTrip(copy)
	}))
}

func TestOllamaFetchOptionalWindowsAndActivity(t *testing.T) {
	for _, activity := range []string{"", `,"activity":null`, `,"activity":{}`, `,"activity":{"cost":null}`} {
		t.Run(activity, func(t *testing.T) {
			h := newOllamaHandler(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"limits":{"session":{"usage":0},"monthly":null}`+activity+`}`)
			})
			env := requestQuotaFetch(t, h, "https://ollama.com/api", ollamaKey, "")
			if !env.OK {
				t.Fatalf("quota.fetch failed: %+v", env.Error)
			}
			var result quotaResult
			if err := json.Unmarshal(env.Result, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Summary) != 0 || len(result.Groups) != 1 || len(result.Groups[0].Buckets) != 1 {
				t.Fatalf("unexpected optional fields result: %+v", result)
			}
			got := result.Groups[0].Buckets[0]
			if got.Window != "5h" || got.RemainingFraction != 1 || got.ResetTime != "" {
				t.Fatalf("unexpected session bucket: %+v", got)
			}
		})
	}
}

func TestOllamaFetchRejectsMalformedUsage(t *testing.T) {
	cases := map[string]string{
		"missing limits":        `{}`,
		"null limits":           `{"limits":null}`,
		"wrong limits type":     `{"limits":[]}`,
		"empty limits":          `{"limits":{}}`,
		"all null":              `{"limits":{"session":null,"weekly":null,"monthly":null}}`,
		"missing usage":         `{"limits":{"monthly":{}}}`,
		"null usage":            `{"limits":{"monthly":{"usage":null}}}`,
		"string usage":          `{"limits":{"monthly":{"usage":"0.25"}}}`,
		"boolean usage":         `{"limits":{"monthly":{"usage":true}}}`,
		"invalid legacy window": `{"limits":{"session":{},"monthly":{"usage":0.25}}}`,
		"wrong window type":     `{"limits":{"weekly":0}}`,
		"overflow usage":        `{"limits":{"monthly":{"usage":1e999}}}`,
		"wrong cost type":       `{"limits":{"monthly":{"usage":0.25}},"activity":{"cost":"5"}}`,
		"invalid JSON":          `{"limits":`,
		"null response":         `null`,
		"trailing JSON":         `{"limits":{"monthly":{"usage":0.25}}} {}`,
		"trailing garbage":      `{"limits":{"monthly":{"usage":0.25}}} garbage`,
		"oversized response":    `{"limits":{"monthly":{"usage":0.25}}}` + strings.Repeat(" ", (1<<20)+1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := newOllamaHandler(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			})
			env := requestQuotaFetch(t, h, "https://ollama.com/api", ollamaKey, "")
			if env.OK || env.Error == nil || env.Error.Code != "quota_fetch_failed" || env.Error.Message != "ollama-cloud /api/usage: unexpected response format (HTTP 200)" {
				t.Fatalf("unexpected format error: %+v", env)
			}
		})
	}
}

func TestOllamaFetchHTTPFailuresOmitUpstreamBody(t *testing.T) {
	cases := []struct {
		status     int
		retryAfter string
		want       string
	}{
		{401, "", "unauthorized (HTTP 401)"},
		{403, "", "forbidden (HTTP 403)"},
		{429, "", "rate limited (HTTP 429)"},
		{429, "9", "rate limited: retry after 9s (HTTP 429)"},
		{429, "Thu, 01 Jan 1970 00:00:00 GMT", "rate limited: retry after 0s (HTTP 429)"},
		{429, "invalid", "rate limited (HTTP 429)"},
		{400, "", "upstream error (HTTP 400)"},
		{503, "", "upstream error (HTTP 503)"},
		{302, "", "upstream error (HTTP 302)"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			h := newOllamaHandler(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", tc.retryAfter)
				w.Header().Set("Location", "/redirect-must-not-be-followed")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{"message":"Authorization: Bearer `+ollamaKey+` ?token=private"}}`)
			})
			env := requestQuotaFetch(t, h, "https://ollama.com/api", ollamaKey, "")
			if env.OK || env.Error == nil || env.Error.Code != "quota_fetch_failed" || env.Error.Message != "ollama-cloud /api/usage: "+tc.want {
				t.Fatalf("unexpected HTTP error: %+v", env)
			}
		})
	}
}

func TestOllamaFetchRequestFailures(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, errors.New("Authorization: Bearer " + ollamaKey + " ?token=private")} {
		h := handler.New(noHost, roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, failure
		}))
		env := requestQuotaFetch(t, h, "https://ollama.com/api", ollamaKey, "")
		if env.OK || env.Error == nil || env.Error.Message != "ollama-cloud /api/usage: request failed" {
			t.Fatalf("unexpected request error: %+v", env)
		}
	}
}

func TestOllamaFetchMonthlyUsage(t *testing.T) {
	h := newOllamaHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/usage" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s, want GET /api/usage", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer "+ollamaKey || r.Header.Get("Accept") != "application/json" {
			t.Error("missing authenticated JSON request headers")
		}
		_, _ = io.WriteString(w, `{"limits":{"session":null,"weekly":null,"monthly":{"usage":0.25}},"future":true}`)
	})
	for _, baseURL := range []string{"https://ollama.com/api", "https://ollama.com/v1"} {
		env := requestQuotaFetch(t, h, baseURL, ollamaKey, "")
		if !env.OK || env.Error != nil {
			t.Fatalf("quota.fetch failed: %+v", env.Error)
		}
		var result quotaResult
		if err := json.Unmarshal(env.Result, &result); err != nil {
			t.Fatal(err)
		}
		if result.Subscription != nil || len(result.Summary) != 0 {
			t.Fatalf("unexpected subscription or summary: %+v", result)
		}
		if len(result.Groups) != 1 || result.Groups[0].DisplayName != "Ollama Cloud" || len(result.Groups[0].Buckets) != 1 {
			t.Fatalf("expected one monthly bucket in Ollama Cloud group: %+v", result.Groups)
		}
		bucket := result.Groups[0].Buckets[0]
		if bucket.Window != "monthly" || bucket.RemainingFraction != .75 || bucket.ResetTime != "" {
			t.Fatalf("unexpected monthly bucket: %+v", bucket)
		}
	}
}

func TestOllamaFetchLegacyWindowsAndCost(t *testing.T) {
	for _, cost := range []float64{0, 12.5} {
		t.Run(fmt.Sprint(cost), func(t *testing.T) {
			h := newOllamaHandler(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"limits":{"session":{"usage":-0.25},"weekly":{"usage":0.5},"monthly":{"usage":1.25}},"activity":{"cost":%g}}`, cost)
			})
			env := requestQuotaFetch(t, h, "https://ollama.com/api", ollamaKey, "")
			if !env.OK || env.Error != nil {
				t.Fatalf("quota.fetch failed: %+v", env.Error)
			}
			var result quotaResult
			if err := json.Unmarshal(env.Result, &result); err != nil {
				t.Fatal(err)
			}
			if result.Subscription != nil || len(result.Groups) != 1 || len(result.Groups[0].Buckets) != 3 {
				t.Fatalf("unexpected legacy result: %+v", result)
			}
			for i, want := range []struct {
				window    string
				remaining float64
			}{{"5h", 1}, {"weekly", .5}, {"monthly", 0}} {
				got := result.Groups[0].Buckets[i]
				if got.Window != want.window || got.RemainingFraction != want.remaining || got.ResetTime != "" {
					t.Errorf("bucket[%d] = %+v, want %+v without reset time", i, got, want)
				}
			}
			if len(result.Summary) != 1 {
				t.Fatalf("summary = %s, want one cost metric", result.Summary)
			}
			var metric struct {
				Label    string  `json:"label"`
				Value    float64 `json:"value"`
				Format   string  `json:"format"`
				Currency string  `json:"currency"`
			}
			if err := json.Unmarshal(result.Summary[0], &metric); err != nil {
				t.Fatal(err)
			}
			if metric.Label != "Cost this period" || metric.Value != cost || metric.Format != "currency" || metric.Currency != "USD" {
				t.Fatalf("unexpected cost metric: %+v", metric)
			}
		})
	}
}
