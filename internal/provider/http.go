package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RequestTimeout bounds each upstream HTTP request.
const RequestTimeout = 10 * time.Second

var (
	authorizationHeaderPattern = regexp.MustCompile(`(?i)\bauthorization\s*:\s*[^,\s]+(?:\s+[^,\s]+)?`)
	bearerTokenPattern         = regexp.MustCompile(`(?i)\bbearer\s+\S+`)
	queryStringPattern         = regexp.MustCompile(`\?[^\s]*`)
)

// NewHTTPClient applies the host proxy, limits request time, and disables connection reuse.
func NewHTTPClient(transport http.RoundTripper, proxyURL string) (*http.Client, error) {
	if transport == nil {
		transport = http.DefaultTransport
	}
	var parsedProxy *url.URL
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Host == "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h") ||
			parsed.Opaque != "" {
			return nil, errors.New("invalid host proxy URL")
		}
		parsedProxy = parsed
	}
	if base, ok := transport.(*http.Transport); ok {
		cloned := base.Clone()
		cloned.DisableKeepAlives = true
		if parsedProxy != nil {
			cloned.Proxy = http.ProxyURL(parsedProxy)
		}
		transport = cloned
	} else if parsedProxy != nil {
		return nil, errors.New("host proxy cannot be applied to the configured transport")
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       RequestTimeout,
		CheckRedirect: rejectRedirect,
	}, nil
}

func rejectRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// DoRequest sends one authenticated JSON request without including credentials in errors.
func DoRequest(ctx context.Context, client *http.Client, method, endpoint, apiKey string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	return client.Do(request)
}

// HTTPError formats an upstream status failure using only a known message field.
func HTTPError(providerID, endpoint string, status int, upstreamMessage, retryAfter, apiKey string) error {
	kind := "upstream error"
	details := make([]string, 0, 2)
	switch status {
	case http.StatusUnauthorized:
		kind = "unauthorized"
	case http.StatusForbidden:
		kind = "forbidden"
	case http.StatusTooManyRequests:
		kind = "rate limited"
		if retryAfter != "" {
			details = append(details, retryAfter)
		}
	}
	if message := sanitizeMessage(upstreamMessage, apiKey); message != "" {
		details = append(details, message)
	}
	if len(details) != 0 {
		kind += ": " + strings.Join(details, ": ")
	}
	return fmt.Errorf("%s %s: %s (HTTP %d)", providerID, endpoint, kind, status)
}

// RequestError omits the transport error and HTTP status, which are unavailable or may contain credentials.
func RequestError(providerID, endpoint string) error {
	return fmt.Errorf("%s %s: request failed", providerID, endpoint)
}

// UnexpectedResponseError reports a malformed provider response without echoing its body.
func UnexpectedResponseError(providerID, endpoint string, status int) error {
	return fmt.Errorf("%s %s: unexpected response format (HTTP %d)", providerID, endpoint, status)
}

// RetryAfterSeconds converts Retry-After delta seconds or HTTP-date to seconds.
func RetryAfterSeconds(value string) string {
	value = strings.TrimSpace(value)
	seconds, err := strconv.ParseUint(value, 10, 64)
	if err == nil {
		return fmt.Sprintf("retry after %ds", seconds)
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return ""
	}
	delay := time.Until(retryAt)
	if delay <= 0 {
		return "retry after 0s"
	}
	secondsUntil := delay / time.Second
	if delay%time.Second != 0 {
		secondsUntil++
	}
	return fmt.Sprintf("retry after %ds", secondsUntil)
}

func sanitizeMessage(message, apiKey string) string {
	message = authorizationHeaderPattern.ReplaceAllString(message, "")
	message = bearerTokenPattern.ReplaceAllString(message, "")
	if apiKey != "" {
		message = strings.ReplaceAll(message, apiKey, "")
	}
	message = queryStringPattern.ReplaceAllString(message, "")
	message = strings.Join(strings.Fields(message), " ")
	if runes := []rune(message); len(runes) > 200 {
		message = string(runes[:200])
	}
	return message
}
