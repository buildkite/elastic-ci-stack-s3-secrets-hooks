package secrets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/sentinel"
	"github.com/joho/godotenv"
)

const (
	// MinSecretSize is the minimum size for a secret to be redacted.
	// Secrets shorter than this will generate a warning and be skipped.
	MinSecretSize = 6
	// MaxSecretSize is the maximum size for a secret to be redacted (64KB) as per Agent limitations.
	// Secrets larger than this will generate a warning and be skipped.
	MaxSecretSize = 65536
	// MaxJSONChunkSize is the maximum size for a JSON chunk sent to buildkite-agent (1MB).
	// This is a conservative limit that helps with optimization, but it may be adjusted in the future as the Agent doesn't impose a limit.
	MaxJSONChunkSize = 1024 * 1024
	// BaseJSONOverhead estimates the minimum JSON structure size (braces, quotes, etc.)
	// used as a starting point for chunk size calculations.
	BaseJSONOverhead = 50
)

// defaultSecretSuffixes contains the default suffixes that identify secret environment variables
var defaultSecretSuffixes = []string{
	"_SECRET",
	"_SECRET_KEY",
	"_PASSWORD",
	"_TOKEN",
	"_ACCESS_KEY",
}

// Client represents interaction with AWS S3
type Client interface {
	Bucket() string
	Region() string
	Get(key string) ([]byte, error)
	ListSuffix(prefix string, suffix []string) ([]string, error)
	BucketExists() (bool, error)
}

// SSHAgent represents interaction with an ssh-agent process
type SSHAgent interface {
	Run() (bool, error)
	Add(key []byte) error
	Pid() int
	Stdout() io.Reader
}

// BuildkiteAgent represents interaction with the buildkite-agent binary
type BuildkiteAgent interface {
	Version() string
	SupportsRedactor() bool
	RedactorAddSecretsFromJSON(filepath string) error
}

// All functions use *Config (pointer receivers) because we accumulate secrets in secretsToRedact across multiple handler functions,
// which avoids copying struct containing 4 interfaces (Client, SSHAgent, BuildkiteAgent, io.Writer) and 2 slices and maintains consistent API - all handlers can modify shared state
type Config struct {
	// Repo from BUILDKITE_REPO
	Repo string

	// Bucket from BUILDKITE_PLUGIN_S3_SECRETS_BUCKET
	Bucket string

	// Prefix within bucket, from BUILDKITE_PLUGIN_S3_SECRETS_BUCKET_PREFIX,
	// defaulting to the value of BUILDKITE_PIPELINE_SLUG
	Prefix string

	// Client for S3
	Client Client

	// Logger is expected to output to stderr
	Logger *log.Logger

	// SSHAgent represents an ssh-agent process
	SSHAgent SSHAgent

	// BuildkiteAgent represents interaction with the buildkite-agent binary
	BuildkiteAgent BuildkiteAgent

	// EnvSink has the contents of environment files written to it
	EnvSink io.Writer

	// GitCredentialHelper is the path to git-credential-s3-secrets
	GitCredentialHelper string

	// Secret suffixes to look for in S3.
	// Defaults to "_SECRET", "_SECRET_KEY", "_PASSWORD", "_TOKEN", and "_ACCESS_KEY"
	SecretSuffixes []string

	// SkipSSHKeyNotFoundWarning suppresses the warning when no SSH key is found
	// Defaults to false
	SkipSSHKeyNotFoundWarning bool

	// secretsToRedact collects all secrets to redact in a single batch
	secretsToRedact []string
}

// Run is the programmatic (as opposed to CLI) entrypoint to all
// functionality; secrets are downloaded from S3, and loaded into ssh-agent
// etc.

// Takes *Config because we need to collect secrets in secretsToRedact
// as we process different S3 objects, then batch them for redaction at the end
func Run(conf *Config) error {
	bucket := conf.Client.Bucket()
	log := conf.Logger

	log.Printf("~~~ Downloading secrets from :s3: %s", bucket)

	if ok, err := conf.Client.BucketExists(); !ok {
		if err != nil {
			log.Printf("+++ :warning: Bucket %q not found", bucket)
		} else {
			log.Printf("+++ :warning: Bucket %q doesn't exist", bucket)
		}
		return fmt.Errorf("S3 bucket %q not found", bucket)
	}

	resultsSSH := make(chan getResult)
	getSSHKeys(*conf, resultsSSH)

	resultsEnv := make(chan getResult)
	getEnvs(*conf, resultsEnv)

	resultsGit := make(chan getResult)
	getGitCredentials(*conf, resultsGit)

	resultsSecrets := make(chan getResult)
	getSecrets(*conf, resultsSecrets)

	if err := handleSSHKeys(conf, resultsSSH); err != nil {
		return err
	}
	if err := handleEnvs(conf, resultsEnv); err != nil {
		return err
	}
	if err := handleGitCredentials(conf, resultsGit); err != nil {
		return err
	}
	if err := handleSecrets(conf, resultsSecrets); err != nil {
		return err
	}

	if len(conf.secretsToRedact) > 0 {
		if err := redactSecrets(conf); err != nil {
			conf.Logger.Printf("Warning: Failed to add secrets to redactor: %v", err)
		}
	} else {
		conf.Logger.Printf("No secrets collected for redaction")
	}

	return nil
}

func getSSHKeys(conf Config, results chan<- getResult) {
	keys := []string{
		conf.Prefix + "/private_ssh_key",
		conf.Prefix + "/id_rsa_github",
		"private_ssh_key",
		"id_rsa_github",
	}
	conf.Logger.Printf("Checking S3 for SSH keys:")
	for _, k := range keys {
		conf.Logger.Printf("- %s", k)
	}
	go GetAll(conf.Client, conf.Client.Bucket(), keys, results)
}

func getEnvs(conf Config, results chan<- getResult) {
	keys := []string{
		"env",
		"environment",
		conf.Prefix + "/env",
		conf.Prefix + "/environment",
	}
	conf.Logger.Printf("Checking S3 for environment files:")
	for _, k := range keys {
		conf.Logger.Printf("- %s", k)
	}
	go GetAll(conf.Client, conf.Client.Bucket(), keys, results)
}

func getSecrets(conf Config, results chan<- getResult) {
	suffixes := append(conf.SecretSuffixes, defaultSecretSuffixes...)

	prefixes := []string{
		"secret-files",
		conf.Prefix + "/secret-files",
	}

	conf.Logger.Printf("Checking S3 for secret-files")
	keys := []string{}
	for _, p := range prefixes {
		conf.Logger.Printf("- %s", p)
		files, err := conf.Client.ListSuffix(p, suffixes)
		if err != nil {
			conf.Logger.Printf("+++ :warning: Failed to list secrets: %v", err)
			continue
		}
		keys = append(keys, files...)
	}
	go GetAll(conf.Client, conf.Client.Bucket(), keys, results)
}

func getGitCredentials(conf Config, results chan<- getResult) {
	keys := []string{
		"git-credentials",
		conf.Prefix + "/git-credentials",
	}
	conf.Logger.Printf("Checking S3 for git credentials:")
	for _, k := range keys {
		conf.Logger.Printf("- %s", k)
	}
	go GetAll(conf.Client, conf.Client.Bucket(), keys, results)
}

func handleSSHKeys(conf *Config, results <-chan getResult) error {
	log := conf.Logger
	keyFound := false
	for r := range results {
		if r.err != nil {
			if r.err != sentinel.ErrNotFound && r.err != sentinel.ErrForbidden {
				log.Printf("+++ :warning: Failed to download ssh-key %s/%s", r.bucket, r.key)
			}
			continue
		}
		if started, err := conf.SSHAgent.Run(); err != nil {
			return err
		} else if started {
			log.Printf("Started ephemeral ssh-agent (pid %d)", conf.SSHAgent.Pid())
		}
		log.Printf(
			"Loading %s/%s (%d bytes) into ssh-agent (pid %d)",
			r.bucket, r.key, len(r.data), conf.SSHAgent.Pid(),
		)
		if err := conf.SSHAgent.Add(r.data); err != nil {
			return fmt.Errorf("failed to add ssh-agent")
		}
		keyFound = true
	}
	if !keyFound && strings.HasPrefix(conf.Repo, "git@") && !conf.SkipSSHKeyNotFoundWarning {
		log.Printf("+++ :warning: Failed to find an SSH key in secret bucket")
		log.Printf(
			"The repository %q appears to use SSH for transport, but the elastic-ci-stack-s3-secrets-hooks plugin did not find any SSH keys in the %q S3 bucket.",
			conf.Repo, conf.Bucket,
		)
		log.Printf("See https://buildkite.com/docs/agent/v3/aws/elastic-ci-stack/ec2-linux-and-windows/secrets-bucket for more information.")
	}
	if _, err := io.Copy(conf.EnvSink, conf.SSHAgent.Stdout()); err != nil {
		return fmt.Errorf("failed in copying ssh-agent env")
	}
	return nil
}

func handleEnvs(conf *Config, results <-chan getResult) error {
	log := conf.Logger
	for r := range results {
		if r.err != nil {
			if r.err != sentinel.ErrNotFound && r.err != sentinel.ErrForbidden {
				log.Printf("+++ :warning: Failed to download env from %s/%s", r.bucket, r.key)
			}
			continue
		}
		data := r.data

		if len(data) > 0 {
			if data[len(data)-1] != '\n' {
				data = append(data, '\n')
			}
			log.Printf("Loading %s/%s (%d bytes) of env", r.bucket, r.key, len(r.data))

			// Parse the environment file to extract values for redaction
			// Use godotenv library to properly handle multi-line secrets and avoid parsing bugs
			envMap, err := godotenv.UnmarshalBytes(r.data)
			if err != nil {
				log.Printf("Warning: failed to parse env file %s/%s", r.bucket, r.key)
			} else {
				for key, value := range envMap {
					if isSecretVar(key) && len(value) > 0 {
						markSecretForRedaction(conf, value)
					}
				}
			}

			if _, err := bytes.NewReader(data).WriteTo(conf.EnvSink); err != nil {
				return fmt.Errorf("failed to write environment data")
			}
		}
	}
	return nil
}

func handleGitCredentials(conf *Config, results <-chan getResult) error {
	log := conf.Logger
	var helpers []string
	for r := range results {
		if r.err != nil {
			if r.err != sentinel.ErrNotFound && r.err != sentinel.ErrForbidden {
				log.Printf("+++ :warning: Failed to check %s/%s", r.bucket, r.key)
			}
			continue
		}
		log.Printf("Adding git-credentials in %s/%s as a credential helper", r.bucket, r.key)

		// Replace spaces ' ' in the helper path with an escaped space '\ '
		escapedCredentialHelper := strings.ReplaceAll(conf.GitCredentialHelper, " ", "\\ ")

		helper := fmt.Sprintf("credential.helper=%s %s %s %s", escapedCredentialHelper, r.bucket, conf.Client.Region(), r.key)

		helpers = append(helpers, helper)
	}
	if len(helpers) == 0 {
		return nil
	}

	// Build an environment variable for interpretation by a shell
	var singleQuotedHelpers []string
	for _, helper := range helpers {
		// Escape any escape sequences, the shell will interpret the first level
		// of escaping.

		// Replace backslash '\' with double backslash '\\'
		helper = strings.ReplaceAll(helper, "\\", "\\\\")

		singleQuotedHelpers = append(singleQuotedHelpers, "'"+helper+"'")
	}
	env := "GIT_CONFIG_PARAMETERS=\"" + strings.Join(singleQuotedHelpers, " ") + "\"\n"

	if _, err := io.WriteString(conf.EnvSink, env); err != nil {
		return fmt.Errorf("failed to write GIT_CONFIG_PARAMETERS env")
	}
	return nil
}

// handleSecrets loads secrets into the environment.
// The key is the last part of the S3 key.
func handleSecrets(conf *Config, results <-chan getResult) error {
	log := conf.Logger
	// Build an environment variable for interpretation by a shell
	var singleQuotedSecrets []string
	var envString string
	for r := range results {
		if r.err != nil {
			if r.err != sentinel.ErrNotFound && r.err != sentinel.ErrForbidden {
				log.Printf("+++ :warning: Failed to download secret %s/%s", r.bucket, r.key)
			}
			continue
		}
		log.Printf("Adding secret %s/%s to environment", r.bucket, r.key)
		envKey := strings.Split(r.key, "/")[len(strings.Split(r.key, "/"))-1]

		// Redact both original and shell-escaped versions of the secret to prevent leaks
		// This fixes an issue where multi-line secrets (like JWT tokens) would appear
		// unredacted in logs when strconv.Quote transforms them (e.g., newlines > \n).
		secretData := string(r.data)
		if len(secretData) > 0 {
			markSecretForRedaction(conf, secretData)
		}

		quotedValue := strconv.Quote(secretData)
		if len(quotedValue) >= 2 {
			unquotedContent := quotedValue[1 : len(quotedValue)-1]
			if unquotedContent != secretData {
				markSecretForRedaction(conf, unquotedContent)
			}
		}

		singleQuotedSecrets = append(singleQuotedSecrets, envKey+"="+quotedValue)
	}
	if len(singleQuotedSecrets) == 0 {
		log.Printf("No secrets found in %q", conf.Prefix)
		return nil
	}
	envString = strings.Join(singleQuotedSecrets, "\n") + "\n"
	if _, err := io.WriteString(conf.EnvSink, envString); err != nil {
		return fmt.Errorf("failed to write secrets to environment")
	}
	return nil
}

// isSecretVar checks if an environment variable name has any of the secret suffixes
func isSecretVar(key string) bool {
	for _, suffix := range defaultSecretSuffixes {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

type getResult struct {
	bucket string
	key    string
	data   []byte
	err    error
}

// GetAll fetches keys from an S3 bucket concurrently.
// Concurrency is unbounded; intended for use with a handful of keys.
// Results are sent to a channel in the originally requested order.
// This is done by creating a chain of channels between each goroutine.
// The results channel is passed through that chain.
func GetAll(c Client, bucket string, keys []string, results chan<- getResult) {
	// first link in chain; will pass results channel into the first goroutine
	link := make(chan chan<- getResult, 1)
	link <- results
	close(link)

	for _, k := range keys {
		// next link in chain; will pass results channel to the next goroutine.
		nextLink := make(chan chan<- getResult)

		// goroutine immediately fetches from S3, then waits for its turn to send
		// to the results channel; concurrent fetch, ordered results.
		go func(k string, link <-chan chan<- getResult, nextLink chan<- chan<- getResult) {
			data, err := c.Get(k)
			results := <-link // wait for results channel from previous goroutine
			results <- getResult{bucket: bucket, key: k, data: data, err: err}
			nextLink <- results // send results channel to the next goroutine
			close(nextLink)
		}(k, link, nextLink)

		link = nextLink // our `nextLink` becomes `link` for the next goroutine.
	}
	close(<-link) // wait for final goroutine, close results channel
}

func markSecretForRedaction(conf *Config, secretValue string) {
	if secretValue == "" {
		return
	}

	if len(secretValue) < MinSecretSize {
		conf.Logger.Printf("Warning: Secret is too short for redaction (%d bytes, min %d bytes)", len(secretValue), MinSecretSize)
		return
	}
	if len(secretValue) >= MaxSecretSize {
		conf.Logger.Printf("Warning: Secret is too large for redaction (%d bytes, max %d bytes)", len(secretValue), MaxSecretSize)
		return
	}

	// Check if we already have this secret to avoid unnecessary operations
	if !slices.Contains(conf.secretsToRedact, secretValue) {
		conf.secretsToRedact = append(conf.secretsToRedact, secretValue)
	}
}

func redactSecrets(conf *Config) error {
	if len(conf.secretsToRedact) == 0 {
		return nil
	}

	if conf.BuildkiteAgent.Version() == "" {
		conf.Logger.Printf("Warning: buildkite-agent not found, secrets will not be redacted")
		return nil
	}

	if !conf.BuildkiteAgent.SupportsRedactor() {
		conf.Logger.Printf("Warning: agent %s doesn't support secret redaction", conf.BuildkiteAgent.Version())
		conf.Logger.Printf("Upgrade to buildkite-agent v3.67.0 or later for automatic secret redaction")
		return nil
	}

	// Clean up the secrets list by removing empty entries
	validSecrets := make([]string, 0, len(conf.secretsToRedact))
	for _, secret := range conf.secretsToRedact {
		if trimmed := strings.TrimSpace(secret); trimmed != "" {
			validSecrets = append(validSecrets, trimmed)
		}
	}

	if len(validSecrets) == 0 {
		return nil
	}

	// Use JSON batch processing for efficiency
	chunks := chunkSecrets(validSecrets, MaxJSONChunkSize)
	conf.Logger.Printf("Processing %d secrets in %d chunk(s) using JSON format", len(validSecrets), len(chunks))

	successfulChunks := 0
	for i, chunk := range chunks {
		if err := processSingleChunk(conf.Logger, conf.BuildkiteAgent, chunk, i+1, len(chunks)); err != nil {
			conf.Logger.Printf("Warning: failed to process chunk %d/%d, some secrets may appear in logs", i+1, len(chunks))
		} else {
			successfulChunks++
		}
	}

	if successfulChunks > 0 {
		conf.Logger.Printf("Successfully added %d secrets to redactor (%d/%d chunks)", len(validSecrets), successfulChunks, len(chunks))
	}

	return nil
}

// chunkSecrets splits a large list of secrets into smaller chunks that fit within
// the specified JSON size limit. This prevents buildkite-agent from rejecting
// oversized files while maintaining the efficiency of batch processing.
func chunkSecrets(secrets []string, maxJSONSize int) [][]string {
	if len(secrets) == 0 {
		return nil
	}

	var chunks [][]string
	currentChunk := []string{}
	currentSize := BaseJSONOverhead

	// Calculate the JSON size for each secret including:
	// Key name ("secret_N": )
	// JSON escaping for quotes and backslashes
	// Structural overhead (quotes, colons, commas)
	for _, secret := range secrets {
		keySize := len(`"secret_`) + len(fmt.Sprintf("%d", len(currentChunk))) + len(`":`) + 3
		secretSize := len(secret) + strings.Count(secret, `"`) + strings.Count(secret, `\`)
		secretJSONSize := keySize + secretSize

		if len(currentChunk) > 0 && currentSize+secretJSONSize > maxJSONSize {
			chunks = append(chunks, currentChunk)
			currentChunk = []string{secret}
			currentSize = BaseJSONOverhead + secretJSONSize
		} else {
			currentChunk = append(currentChunk, secret)
			currentSize += secretJSONSize
		}
	}

	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}

	return chunks
}

// processSingleChunk handles one chunk of secrets by creating a temporary JSON file
// and passing it to buildkite-agent for redaction. The file is created and cleaned up regardless of success or failure.
func processSingleChunk(log *log.Logger, buildkiteAgent BuildkiteAgent, secrets []string, chunkNum, totalChunks int) error {
	jsonSecrets := make(map[string]string)
	for i, secret := range secrets {
		jsonSecrets[fmt.Sprintf("secret_%d", i)] = secret
	}

	jsonData, err := json.Marshal(jsonSecrets)
	if err != nil {
		return fmt.Errorf("failed to marshal chunk %d to JSON: %w", chunkNum, err)
	}

	tempFile, err := os.CreateTemp("", fmt.Sprintf("buildkite-secrets-chunk-%d-*.json", chunkNum))
	if err != nil {
		return fmt.Errorf("failed to create temporary file for chunk %d: %w", chunkNum, err)
	}

	// Ensure the temporary file is always cleaned up, even if something goes wrong.
	// Set restrictive permissions (0600) so only the current user can read the secrets.
	defer func() {
		tempFile.Close()
		if err := os.Remove(tempFile.Name()); err != nil {
			log.Printf("Warning: failed to remove temporary secrets file %s", tempFile.Name())
		}
	}()

	if err := tempFile.Chmod(0600); err != nil {
		return fmt.Errorf("failed to set permissions on temporary file for chunk %d", chunkNum)
	}

	if _, err := tempFile.Write(jsonData); err != nil {
		return fmt.Errorf("failed to write chunk %d to temporary file", chunkNum)
	}

	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temporary file for chunk %d", chunkNum)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file for chunk %d: %w", chunkNum, err)
	}

	if err := buildkiteAgent.RedactorAddSecretsFromJSON(tempFile.Name()); err != nil {
		return fmt.Errorf("buildkite-agent command failed for chunk %d: %w", chunkNum, err)
	}

	log.Printf("Processed chunk %d/%d (%d secrets, %d bytes)",
		chunkNum, totalChunks, len(secrets), len(jsonData))

	return nil
}
