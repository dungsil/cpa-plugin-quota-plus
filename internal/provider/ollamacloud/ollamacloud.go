// Package ollamacloud identifies Ollama Cloud API keys.
package ollamacloud

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
