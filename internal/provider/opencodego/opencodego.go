// Package opencodego identifies OpenCode Go API keys.
package opencodego

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
