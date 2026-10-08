// Package provider defines the provider module contract and the official URL
// comparison shared by quota.describe and quota.fetch.
package provider

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// Provider is one upstream service whose API keys this plugin can serve.
type Provider interface {
	// ID is the provider identifier used in error messages, e.g. "opencode-go".
	ID() string
	// DisplayName is the quota group name, e.g. "OpenCode Go".
	DisplayName() string
	// BaseURLs lists the official URLs that identify this provider's keys.
	BaseURLs() []string
}

// Match returns the provider whose official URL equals baseURL.
//
// Only https is accepted. The host name is compared case-insensitively and a
// single trailing "/" is ignored; everything else, including the path, must
// be identical. Prefix or partial matches never match.
func Match(providers []Provider, baseURL string) (Provider, bool) {
	key, ok := normalize(baseURL)
	if !ok {
		return nil, false
	}
	for _, p := range providers {
		for _, official := range p.BaseURLs() {
			if officialKey, okOfficial := normalize(official); okOfficial && officialKey == key {
				return p, true
			}
		}
	}
	return nil, false
}

func normalize(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !isASCII(u.Host) {
		return "", false
	}
	// Official hosts are ASCII, so only ASCII case is folded. Unicode folding
	// would map hosts such as "apİ" (U+0130) onto a different, official name.
	return strings.ToLower(u.Host) + strings.TrimSuffix(u.EscapedPath(), "/"), true
}

func isASCII(s string) bool {
	for _, r := range s {
		if r >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
