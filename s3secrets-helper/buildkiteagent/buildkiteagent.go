package buildkiteagent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Agent struct {
	supportsRedactor bool
	version          string
}

func New() *Agent {
	b := &BuildkiteAgent{
		version:          "",
		supportsRedactor: false,
	}
	b.detectAgentCapabilities()
	return b
}

func (b *Agent) Version() string {
	return b.version
}

func (b *Agent) SupportsRedactor() bool {
	return b.supportsRedactor
}	

func (b *Agent) RedactorAddSecretsFromJSON(filepath string) error {
	cmd := exec.Command("buildkite-agent", "redactor", "add", "--format", "json", filepath)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("buildkite-agent command failed: %w", err)
	}

	return nil
}

// detectAgentCapabilities discovers what the buildkite-agent supports.
// We detect the actual version and check if the redactor command exists.
// JSON format support is guaranteed when redactor is available.
func (b *Agent) detectAgentCapabilities() {
	_, err := exec.LookPath("buildkite-agent")
	if err != nil {
		return
	}

	// Extract version from "buildkite-agent, e.g. version 3.73.0"
	versionCmd := exec.Command("buildkite-agent", "--version")
	if versionOutput, err := versionCmd.Output(); err == nil {
		versionStr := strings.TrimSpace(string(versionOutput))
		if parts := strings.Fields(versionStr); len(parts) >= 3 {
			b.version = parts[2]
		} else {
			b.version = "unknown" // version command succeeded but couldn't parse returned version
		}
	} else {
		b.version = "unknown"   // agent is installed but version command failed
	}

	// Test if redactor command exists
	cmd := exec.Command("buildkite-agent", "redactor", "add", "--help")
	output, err := cmd.Output()
	if err != nil {
		// Command failed completely (e.g., buildkite-agent not found)
		return
	}

	// Check if the command actually succeeded by looking for redactor-specific content
	// If "redactor" isn't supported, buildkite-agent shows general help instead
	helpText := string(output)
	if !strings.Contains(helpText, "redactor") {
		// Command ran but redactor subcommand doesn't exist
		return
	}

	// Redactor command exists
	b.supportsRedactor = true
}
