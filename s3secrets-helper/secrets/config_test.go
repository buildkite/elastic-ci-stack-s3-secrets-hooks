package secrets

// Expose the secretsToRedact field for testing purposes only
func (c *Config) GetSecretsToRedactForTesting() []string {
	return c.secretsToRedact
}
