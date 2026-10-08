// Package commandcode identifies CommandCode API keys.
package commandcode

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
