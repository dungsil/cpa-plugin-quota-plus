package handler_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dungsil/cpa-plugin-quota-plus/internal/handler"
)

const (
	openCodeBaseURL = "https://opencode.ai/zen/go/v1"
	openCodeKey     = "quota-test-key-do-not-leak"
)

type quotaEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type quotaResult struct {
	Subscription *struct {
		Plan string `json:"plan"`
	} `json:"subscription"`
	Summary []json.RawMessage `json:"summary"`
	Groups  []struct {
		DisplayName string `json:"displayName"`
		Buckets     []struct {
			Window            string  `json:"window"`
			RemainingFraction float64 `json:"remainingFraction"`
			ResetTime         string  `json:"resetTime"`
			Description       string  `json:"description"`
		} `json:"buckets"`
	} `json:"groups"`
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type rewriteTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (rt rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != "opencode.ai" {
		return nil, fmt.Errorf("unexpected upstream URL")
	}
	copy := request.Clone(request.Context())
	upstreamURL := *rt.target
	upstreamURL.Path = request.URL.Path
	upstreamURL.RawPath = request.URL.RawPath
	upstreamURL.RawQuery = request.URL.RawQuery
	copy.URL = &upstreamURL
	copy.Host = upstreamURL.Host
	return rt.next.RoundTrip(copy)
}

func newOpenCodeHandler(transport http.RoundTripper) *handler.Handler {
	return handler.New(noHost, transport)
}

func requestQuotaFetch(t *testing.T, h *handler.Handler, baseURL, apiKey, proxyURL string) quotaEnvelope {
	t.Helper()
	request := map[string]any{
		"auth_index": "quota-test-auth",
		"attributes": map[string]string{"base_url": baseURL, "api_key": apiKey},
	}
	if proxyURL != "" {
		request["host"] = map[string]string{"ProxyURL": proxyURL}
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := h.Handle("quota.fetch", raw)
	var env quotaEnvelope
	if err := json.Unmarshal(response, &env); err != nil {
		t.Fatalf("quota.fetch response is not an envelope: %v (%s)", err, response)
	}
	return env
}

func newMappedHandler(t *testing.T, handlerFunc http.HandlerFunc) *handler.Handler {
	t.Helper()
	server := httptest.NewServer(handlerFunc)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := rewriteTransport{target: target, next: http.DefaultTransport}
	return newOpenCodeHandler(transport)
}

func validUsageJSON(rolling, weekly, monthly string) string {
	return `{"usage":{"rolling":` + rolling + `,"weekly":` + weekly + `,"monthly":` + monthly + `}}`
}

func usageWindow(percent, resetsAt string) string {
	return `{"percent":` + percent + `,"resetsAt":"` + resetsAt + `"}`
}

func TestQuotaFetchConvertsOpenCodeUsage(t *testing.T) {
	response := validUsageJSON(
		usageWindow("-25", "2026-10-08T14:30:00.500+02:00"),
		usageWindow("50", "2026-10-15T00:00:00Z"),
		usageWindow("150", "2026-11-01T00:00:00Z"),
	)
	response = strings.Replace(response, `{"usage":`, `{"future":{"ignored":true},"usage":`, 1)
	var calls atomic.Int32
	h := newMappedHandler(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/zen/go/v1/usage" {
			t.Errorf("upstream request = %s %s, want GET /zen/go/v1/usage", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+openCodeKey {
			t.Errorf("Authorization = %q, want Bearer test key", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	})

	env := requestQuotaFetch(t, h, openCodeBaseURL, openCodeKey, "")
	if !env.OK || env.Error != nil {
		t.Fatalf("quota.fetch failed: %+v", env.Error)
	}
	var result quotaResult
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode quota result: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
	if result.Subscription == nil || result.Subscription.Plan != "Go" {
		t.Fatalf("subscription = %+v, want Go", result.Subscription)
	}
	if len(result.Summary) != 0 {
		t.Fatalf("summary = %v, want empty", result.Summary)
	}
	if len(result.Groups) != 1 || result.Groups[0].DisplayName != "OpenCode Go" {
		t.Fatalf("groups = %+v, want one OpenCode Go group", result.Groups)
	}
	buckets := result.Groups[0].Buckets
	if len(buckets) != 3 {
		t.Fatalf("buckets = %+v, want three buckets", buckets)
	}
	want := []struct {
		window      string
		remaining   float64
		reset       string
		description string
	}{
		{"5h", 1, "2026-10-08T12:30:00.5Z", ""},
		{"weekly", .5, "2026-10-15T00:00:00Z", ""},
		{"monthly", 0, "2026-11-01T00:00:00Z", ""},
	}
	for i, expected := range want {
		got := buckets[i]
		if got.Window != expected.window || got.RemainingFraction != expected.remaining ||
			got.ResetTime != expected.reset || got.Description != expected.description {
			t.Errorf("bucket[%d] = %+v, want %+v", i, got, expected)
		}
	}
}

func TestQuotaFetchRateLimitedWindowHasZeroRemaining(t *testing.T) {
	response := validUsageJSON(
		`{"status":"rate-limited","percent":10,"resetsAt":"2026-10-08T14:30:00Z"}`,
		usageWindow("20", "2026-10-15T00:00:00Z"),
		usageWindow("30", "2026-11-01T00:00:00Z"),
	)
	h := newMappedHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, response)
	})
	env := requestQuotaFetch(t, h, openCodeBaseURL, openCodeKey, "")
	if !env.OK {
		t.Fatalf("quota.fetch failed: %+v", env.Error)
	}
	var result quotaResult
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	got := result.Groups[0].Buckets[0]
	if got.RemainingFraction != 0 || got.Description != "Limit reached" {
		t.Fatalf("rate-limited bucket = %+v, want zero remaining and Limit reached", got)
	}
}

func TestQuotaFetchValidatesInputWithoutCallingUpstream(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("upstream should not be called")
	})
	h := handler.New(noHost, transport)
	cases := []struct {
		name    string
		baseURL string
		apiKey  string
		want    string
	}{
		{"unofficial URL", "https://opencode.ai/zen/go/v1?token=private", openCodeKey, "base_url"},
		{"empty API key", openCodeBaseURL, "", "api_key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := requestQuotaFetch(t, h, tc.baseURL, tc.apiKey, "")
			if env.OK || env.Error == nil || !strings.Contains(env.Error.Message, tc.want) {
				t.Fatalf("quota.fetch error = %+v, want %q validation error", env.Error, tc.want)
			}
			if strings.Contains(env.Error.Message, tc.baseURL) || strings.Contains(env.Error.Message, openCodeKey) || strings.Contains(env.Error.Message, "token=private") {
				t.Fatalf("validation error leaked credential data: %q", env.Error.Message)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
}

func TestQuotaFetchRejectsUnexpectedResponseFormats(t *testing.T) {
	valid := func() string {
		return validUsageJSON(
			usageWindow("10", "2026-10-08T14:30:00Z"),
			usageWindow("20", "2026-10-15T00:00:00Z"),
			usageWindow("30", "2026-11-01T00:00:00Z"),
		)
	}
	cases := []struct {
		name string
		body string
	}{
		{"missing period", `{"usage":{"weekly":{"percent":20,"resetsAt":"2026-10-15T00:00:00Z"},"monthly":{"percent":30,"resetsAt":"2026-11-01T00:00:00Z"}}}`},
		{"missing weekly", strings.Replace(valid(), `,"weekly":{"percent":20,"resetsAt":"2026-10-15T00:00:00Z"}`, "", 1)},
		{"missing monthly", strings.Replace(valid(), `,"monthly":{"percent":30,"resetsAt":"2026-11-01T00:00:00Z"}`, "", 1)},
		{"missing percent", strings.Replace(valid(), `"percent":10,`, ``, 1)},
		{"wrong percent type", strings.Replace(valid(), `"percent":10`, `"percent":"10"`, 1)},
		{"missing reset", `{"usage":{"rolling":{"percent":10},"weekly":{"percent":20,"resetsAt":"2026-10-15T00:00:00Z"},"monthly":{"percent":30,"resetsAt":"2026-11-01T00:00:00Z"}}}`},
		{"invalid reset", strings.Replace(valid(), "2026-10-08T14:30:00Z", "not-a-time", 1)},
		{"wrong reset type", strings.Replace(valid(), `"resetsAt":"2026-10-08T14:30:00Z"`, `"resetsAt":42`, 1)},
		{"wrong status type", strings.Replace(valid(), `"percent":10`, `"status":false,"percent":10`, 1)},
		{"unknown status", strings.Replace(valid(), `"percent":10`, `"status":"future","percent":10`, 1)},
		{"null status", strings.Replace(valid(), `"percent":10`, `"status":null,"percent":10`, 1)},
		{"oversized response", valid() + strings.Repeat(" ", (1<<20)+1)},
		{"invalid JSON", `{"usage":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMappedHandler(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			})
			env := requestQuotaFetch(t, h, openCodeBaseURL, openCodeKey, "")
			if env.OK || env.Error == nil || env.Error.Message != "opencode-go /zen/go/v1/usage: unexpected response format (HTTP 200)" {
				t.Fatalf("quota.fetch error = %+v, want unexpected response format", env.Error)
			}
		})
	}
}

func TestQuotaFetchFormatsHTTPFailures(t *testing.T) {
	longMessage := strings.Repeat("x", 250)
	cases := []struct {
		name        string
		status      int
		retryAfter  string
		body        string
		wantMessage string
	}{
		{"unauthorized", http.StatusUnauthorized, "", `{"error":{"type":"AuthError","message":"invalid key"}}`, "opencode-go /zen/go/v1/usage: unauthorized: invalid key (HTTP 401)"},
		{"forbidden", http.StatusForbidden, "", `{"error":{"type":"AuthError","message":"no subscription"}}`, "opencode-go /zen/go/v1/usage: forbidden: no subscription (HTTP 403)"},
		{"Go entitlement", http.StatusForbidden, "", `{"error":{"type":"EntitlementError","message":"OpenCode Go subscription required."}}`, "opencode-go /zen/go/v1/usage: forbidden: Go subscription required (HTTP 403)"},
		{"rate limited without retry-after", http.StatusTooManyRequests, "", `{"error":{"message":"slow down"}}`, "opencode-go /zen/go/v1/usage: rate limited: slow down (HTTP 429)"},
		{"rate limited with retry-after", http.StatusTooManyRequests, "9", `{"error":{"message":"slow down"}}`, "opencode-go /zen/go/v1/usage: rate limited: retry after 9s: slow down (HTTP 429)"},
		{"rate limited with HTTP-date Retry-After", http.StatusTooManyRequests, "Thu, 01 Jan 1970 00:00:00 GMT", `{"error":{"message":"slow down"}}`, "opencode-go /zen/go/v1/usage: rate limited: retry after 0s: slow down (HTTP 429)"},
		{"other 4xx", http.StatusBadRequest, "", `{"error":{"message":"bad request"}}`, "opencode-go /zen/go/v1/usage: upstream error: bad request (HTTP 400)"},
		{"other 5xx", http.StatusServiceUnavailable, "", `{"error":{"message":"temporarily unavailable"}}`, "opencode-go /zen/go/v1/usage: upstream error: temporarily unavailable (HTTP 503)"},
		{"truncate upstream message", http.StatusBadGateway, "", `{"error":{"message":"` + longMessage + `"}}`, "opencode-go /zen/go/v1/usage: upstream error: " + longMessage[:200] + " (HTTP 502)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			h := newMappedHandler(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			env := requestQuotaFetch(t, h, openCodeBaseURL, openCodeKey, "")
			if env.OK || env.Error == nil || env.Error.Message != tc.wantMessage {
				t.Fatalf("quota.fetch error = %+v, want %q", env.Error, tc.wantMessage)
			}
			if calls.Load() != 1 {
				t.Fatalf("upstream calls = %d, want one attempt", calls.Load())
			}
		})
	}
}

func TestQuotaFetchRedactsSecretsAndDoesNotExposeResponseBody(t *testing.T) {
	message := "denied " + openCodeKey + " Authorization: Bearer " + openCodeKey + " https://example.invalid/path?access_token=hidden"
	body := `{"error":{"message":"` + message + `"},"private":"full-body-marker"}`
	h := newMappedHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, body)
	})
	env := requestQuotaFetch(t, h, openCodeBaseURL, openCodeKey, "")
	if env.OK || env.Error == nil {
		t.Fatalf("quota.fetch error = %+v, want error", env.Error)
	}
	for _, forbidden := range []string{openCodeKey, "Authorization: Bearer", "full-body-marker", "access_token=hidden", "?"} {
		if strings.Contains(env.Error.Message, forbidden) {
			t.Errorf("error leaked %q: %q", forbidden, env.Error.Message)
		}
	}
	if !strings.HasPrefix(env.Error.Message, "opencode-go /zen/go/v1/usage: unauthorized") {
		t.Fatalf("error message = %q, want OpenCode endpoint context", env.Error.Message)
	}
}

func TestQuotaFetchRequestFailuresDoNotExposeTransportErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"timeout", context.DeadlineExceeded},
		{"connection failure", errors.New("dial https://opencode.ai/zen/go/v1/usage?token=hidden: Authorization: Bearer " + openCodeKey)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				deadline, ok := request.Context().Deadline()
				remaining := time.Until(deadline)
				if !ok || remaining > 10*time.Second || remaining < 9*time.Second {
					t.Errorf("request deadline = %v, present=%t; want about 10 seconds away", deadline, ok)
				}
				return nil, tc.err
			})
			h := newOpenCodeHandler(transport)
			env := requestQuotaFetch(t, h, openCodeBaseURL, openCodeKey, "")
			want := "opencode-go /zen/go/v1/usage: request failed"
			if env.OK || env.Error == nil || env.Error.Message != want {
				t.Fatalf("quota.fetch error = %+v, want %q", env.Error, want)
			}
			for _, forbidden := range []string{openCodeKey, "Authorization", "?token=hidden", "opencode.ai"} {
				if strings.Contains(env.Error.Message, forbidden) {
					t.Errorf("request failure leaked %q: %q", forbidden, env.Error.Message)
				}
			}
		})
	}
}

func TestQuotaFetchDoesNotCacheResults(t *testing.T) {
	var calls atomic.Int32
	response := validUsageJSON(
		usageWindow("10", "2026-10-08T14:30:00Z"),
		usageWindow("20", "2026-10-15T00:00:00Z"),
		usageWindow("30", "2026-11-01T00:00:00Z"),
	)
	h := newMappedHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, response)
	})
	for range 2 {
		env := requestQuotaFetch(t, h, openCodeBaseURL, openCodeKey, "")
		if !env.OK {
			t.Fatalf("quota.fetch failed: %+v", env.Error)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want one per fetch", calls.Load())
	}
}

func TestQuotaFetchUsesHostProxy(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/zen/go/v1/usage" {
			t.Errorf("upstream request = %s %s, want GET /zen/go/v1/usage", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, validUsageJSON(
			usageWindow("10", "2026-10-08T14:30:00Z"),
			usageWindow("20", "2026-10-15T00:00:00Z"),
			usageWindow("30", "2026-11-01T00:00:00Z"),
		))
	}))
	t.Cleanup(upstream.Close)

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("proxy method = %s, want CONNECT", r.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		clientConn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack proxy connection: %v", err)
			return
		}
		defer clientConn.Close()
		upstreamConn, err := net.Dial("tcp", upstream.Listener.Addr().String())
		if err != nil {
			t.Errorf("connect proxy tunnel: %v", err)
			return
		}
		defer upstreamConn.Close()
		if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			t.Errorf("write CONNECT response: %v", err)
			return
		}
		if err := rw.Flush(); err != nil {
			t.Errorf("flush CONNECT response: %v", err)
			return
		}
		finished := make(chan struct{}, 1)
		go func() {
			_, _ = io.Copy(upstreamConn, clientConn)
			finished <- struct{}{}
		}()
		_, _ = io.Copy(clientConn, upstreamConn)
		<-finished
	}))
	t.Cleanup(proxy.Close)

	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	h := newOpenCodeHandler(transport)
	env := requestQuotaFetch(t, h, openCodeBaseURL, openCodeKey, proxy.URL)
	if !env.OK {
		t.Fatalf("quota.fetch through proxy failed: %+v", env.Error)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls through proxy = %d, want 1", calls.Load())
	}
}
