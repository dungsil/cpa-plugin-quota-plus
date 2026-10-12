package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dungsil/cpa-plugin-quota-plus/internal/handler"
)

const commandCodeBaseURL = "https://api.commandcode.ai/provider/v1"
const commandCodeCredits = `{"windowLimits":{"fiveHour":{"used":25,"cap":100,"resetAt":0},"weekly":{"used":20,"cap":80}},"credits":{"monthlyCredits":12.5,"purchasedCredits":0,"freeCredits":3}}`

func newCommandCodeHandler(t *testing.T, serve http.HandlerFunc) *handler.Handler {
	t.Helper()
	server := httptest.NewServer(serve)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return handler.New(noHost, roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.commandcode.ai" {
			return nil, fmt.Errorf("unexpected upstream URL")
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		copy.URL, copy.Host = &u, target.Host
		return http.DefaultTransport.RoundTrip(copy)
	}))
}

func TestCommandCodeWindowRules(t *testing.T) {
	for _, tc := range []struct {
		name, window, reset, description string
		remaining                        float64
	}{
		{"negative usage", `{"used":-5,"cap":10}`, "", "", 1},
		{"over cap", `{"used":15,"cap":10}`, "", "", 0},
		{"exceeded", `{"used":1,"cap":10,"exceeded":true}`, "", "Limit reached", 0},
		{"milliseconds", `{"used":1,"cap":10,"resetAt":1001}`, "1970-01-01T00:00:01.001Z", "", .9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCommandCodeHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/alpha/billing/credits" {
					_, _ = fmt.Fprintf(w, `{"windowLimits":{"fiveHour":%s,"weekly":%s}}`, tc.window, tc.window)
				} else {
					_, _ = io.WriteString(w, `{}`)
				}
			})
			env := requestQuotaFetch(t, h, commandCodeBaseURL, openCodeKey, "")
			if !env.OK {
				t.Fatalf("fetch: %+v", env.Error)
			}
			var result quotaResult
			if err := json.Unmarshal(env.Result, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Groups) != 1 || len(result.Groups[0].Buckets) != 2 || len(result.Summary) != 0 || result.Subscription != nil {
				t.Fatalf("result = %+v", result)
			}
			for _, b := range result.Groups[0].Buckets {
				if b.RemainingFraction != tc.remaining || b.ResetTime != tc.reset || b.Description != tc.description {
					t.Errorf("bucket = %+v", b)
				}
			}
		})
	}
}

func TestCommandCodeRejectsMalformedCredits(t *testing.T) {
	for _, window := range []string{`null`, `{}`, `{"used":"1","cap":2}`, `{"used":null,"cap":2}`, `{"used":1,"cap":0}`, `{"used":1,"cap":-1}`, `{"used":1,"cap":"2"}`, `{"used":1,"cap":2,"resetAt":"bad"}`, `{"used":1,"cap":2,"resetAt":9223372036854775807}`} {
		for _, period := range []string{"fiveHour", "weekly"} {
			t.Run(period+window, func(t *testing.T) {
				body := `{"windowLimits":{"fiveHour":{"used":1,"cap":2},"weekly":{"used":1,"cap":2}}}`
				body = strings.Replace(body, `"`+period+`":{"used":1,"cap":2}`, `"`+period+`":`+window, 1)
				h := newCommandCodeHandler(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201); _, _ = io.WriteString(w, body) })
				env := requestQuotaFetch(t, h, commandCodeBaseURL, openCodeKey, "")
				if env.OK || env.Error == nil || env.Error.Message != "commandcode /alpha/billing/credits: unexpected response format (HTTP 201)" {
					t.Fatalf("error = %+v", env.Error)
				}
			})
		}
	}
}

func TestCommandCodeSubscriptionFailureIsOptional(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":{}}`, `{"data":{"planId":null}}`, `{"data":{"planId":42}}`, `{"data":{"planId":"partial"}} trailing`, `broken`, strings.Repeat(" ", (1<<20)+1)} {
		t.Run(fmt.Sprintf("body-%d", len(body))+body[:min(20, len(body))], func(t *testing.T) {
			h := newCommandCodeHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/alpha/billing/credits" {
					_, _ = io.WriteString(w, commandCodeCredits)
				} else {
					_, _ = io.WriteString(w, body)
				}
			})
			assertCommandCodePartialQuota(t, h)
		})
	}
	for _, status := range []int{401, 403, 429, 500, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h := newCommandCodeHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/alpha/billing/credits" {
					_, _ = io.WriteString(w, commandCodeCredits)
				} else {
					w.WriteHeader(status)
				}
			})
			assertCommandCodePartialQuota(t, h)
		})
	}
	h := newCommandCodeHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/alpha/billing/credits" {
			_, _ = io.WriteString(w, commandCodeCredits)
		} else {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}
	})
	assertCommandCodePartialQuota(t, h)
}

func assertCommandCodePartialQuota(t *testing.T, h *handler.Handler) {
	t.Helper()
	env := requestQuotaFetch(t, h, commandCodeBaseURL, openCodeKey, "")
	if !env.OK {
		t.Fatalf("fetch = %+v", env.Error)
	}
	var result quotaResult
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Subscription != nil || len(result.Summary) != 3 || len(result.Groups) != 1 || len(result.Groups[0].Buckets) != 2 || result.Groups[0].Buckets[0].RemainingFraction != .75 {
		t.Fatalf("partial result = %+v", result)
	}
}

func TestCommandCodeCreditsErrors(t *testing.T) {
	for _, tc := range []struct {
		status      int
		retry, kind string
	}{{401, "", "unauthorized"}, {403, "", "forbidden"}, {429, "", "rate limited"}, {429, "7", "rate limited: retry after 7s"}, {400, "", "upstream error"}, {503, "", "upstream error"}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			h := newCommandCodeHandler(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", tc.retry)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{"message":"Authorization: Bearer `+openCodeKey+` ?token=secret"}}`)
			})
			env := requestQuotaFetch(t, h, commandCodeBaseURL, openCodeKey, "")
			want := fmt.Sprintf("commandcode /alpha/billing/credits: %s (HTTP %d)", tc.kind, tc.status)
			if env.OK || env.Error == nil || env.Error.Message != want {
				t.Fatalf("error = %+v, want %s", env.Error, want)
			}
		})
	}
	for _, failure := range []error{context.DeadlineExceeded, fmt.Errorf("Authorization: Bearer %s ?token=secret", openCodeKey)} {
		h := handler.New(noHost, roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, failure }))
		env := requestQuotaFetch(t, h, commandCodeBaseURL, openCodeKey, "")
		if env.OK || env.Error == nil || env.Error.Message != "commandcode /alpha/billing/credits: request failed" {
			t.Fatalf("error = %+v", env.Error)
		}
	}
}

func TestCommandCodeRejectsInvalidCreditDocuments(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `broken`, commandCodeCredits + `{}`,
		commandCodeCredits + strings.Repeat(" ", (1<<20)+1),
		strings.Replace(commandCodeCredits, `"monthlyCredits":12.5`, `"monthlyCredits":"12.5"`, 1),
	} {
		h := newCommandCodeHandler(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		env := requestQuotaFetch(t, h, commandCodeBaseURL, openCodeKey, "")
		if env.OK || env.Error == nil || env.Error.Message != "commandcode /alpha/billing/credits: unexpected response format (HTTP 200)" {
			t.Fatalf("error = %+v", env.Error)
		}
	}
}

func TestCommandCodeConvertsQuota(t *testing.T) {
	h := newCommandCodeHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+openCodeKey || r.Header.Get("Accept") != "application/json" {
			t.Errorf("incorrect authenticated JSON request")
		}
		switch r.URL.Path {
		case "/alpha/billing/credits":
			_, _ = io.WriteString(w, commandCodeCredits)
		case "/alpha/billing/subscriptions":
			_, _ = io.WriteString(w, `{"data":{"planId":"test-plan"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	env := requestQuotaFetch(t, h, commandCodeBaseURL, openCodeKey, "")
	if !env.OK {
		t.Fatalf("fetch failed: %+v", env.Error)
	}
	var result quotaResult
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Subscription == nil || result.Subscription.Plan != "test-plan" {
		t.Fatalf("subscription = %+v", result.Subscription)
	}
	if len(result.Groups) != 1 || result.Groups[0].DisplayName != "CommandCode" || len(result.Groups[0].Buckets) != 2 {
		t.Fatalf("groups = %+v", result.Groups)
	}
	b := result.Groups[0].Buckets
	if b[0].Window != "5h" || b[0].RemainingFraction != .75 || b[0].ResetTime != "1970-01-01T00:00:00Z" || b[1].Window != "weekly" || b[1].RemainingFraction != .75 || b[1].ResetTime != "" {
		t.Fatalf("buckets = %+v", b)
	}
	if len(result.Summary) != 3 {
		t.Fatalf("summary = %s", result.Summary)
	}
	for i, label := range []string{"Monthly credits", "Purchased credits", "Free credits"} {
		var metric struct {
			Label, Format, Unit string
			Value               float64
		}
		if err := json.Unmarshal(result.Summary[i], &metric); err != nil {
			t.Fatal(err)
		}
		if metric.Label != label || metric.Format != "number" || metric.Unit != "credits" || metric.Value != []float64{12.5, 0, 3}[i] {
			t.Errorf("metric = %+v", metric)
		}
	}
}
