package secrets

import (
	"fmt"
	"io"
	"log"
	"strings"
)

// Client represents interaction with AWS S3
type Client interface {
	Get(bucket, key string) ([]byte, error)
	BucketExists(bucket string) (bool, error)
}

// Agent represents interaction with an ssh-agent process
type Agent interface {
	Add(key []byte) error
	Pid() uint
}

// Config holds all the parameters for Run()
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

	// LogWriter is an io.Writer sink for the logger
	LogWriter io.Writer

	// SSHAgent represents an ssh-agent process
	SSHAgent Agent
}

// Run is the programmatic (as opposed to CLI) entrypoint to all
// functionality; secrets are downloaded from S3, and loaded into ssh-agent
// etc.
func Run(conf Config) error {
	bucket := conf.Bucket
	prefix := conf.Prefix
	client := conf.Client
	logger := log.New(conf.LogWriter, "", log.LstdFlags)
	agent := conf.SSHAgent

	logger.Printf("~~~ Downloading secrets from :s3: %s", bucket)

	if ok, err := client.BucketExists(bucket); !ok {
		logger.Printf("+++ :warning: Bucket %q doesn't exist", bucket)
		if err != nil {
			logger.Println(err)
		}
		return fmt.Errorf("bucket %q not found", bucket)
	}

	keys := []string{
		prefix + "/private_ssh_key",
		prefix + "/id_rsa_github",
		"private_ssh_key",
		"id_rsa_github",
	}
	logger.Printf("Trying to download from S3:")
	for _, k := range keys {
		logger.Printf("- %s", k)
	}
	keyFound := false
	results := make(chan getResult)
	go GetAll(client, bucket, keys, results)
	for res := range results {
		if res.err != nil {
			// TODO: silently ignore NotFound & Forbidden errors
			logger.Printf("+++ :warning: Failed to download ssh-key %s/%s: %v", bucket, res.key, res.err)
			continue
		}
		logger.Printf(
			"Loading %s/%s (%d bytes) into ssh-agent (pid %d)",
			res.bucket,
			res.key,
			len(res.data),
			agent.Pid(),
		)
		if err := agent.Add(res.data); err != nil {
			return fmt.Errorf("ssh-agent add: %w", err)
		}
		keyFound = true
	}
	if !keyFound && strings.HasPrefix(conf.Repo, "git@") {
		logger.Println("+++ :warning: Failed to find an SSH key in secret bucket")
		logger.Println("The repository '$BUILDKITE_REPO' appears to use SSH for transport, but the elastic-ci-stack-s3-secrets-hooks plugin did not find any SSH keys in the $s3_bucket S3 bucket.")
		logger.Println("See https://github.com/buildkite/elastic-ci-stack-for-aws#build-secrets for more information.")
	}
	return nil
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
			data, err := c.Get(bucket, k)
			results := <-link // wait for results channel from previous goroutine
			results <- getResult{bucket: bucket, key: k, data: data, err: err}
			nextLink <- results // send results channel to the next goroutine
			close(nextLink)
		}(k, link, nextLink)

		link = nextLink // our `nextLink` becomes `link` for the next goroutine.
	}
	close(<-link) // wait for final goroutine, close results channel
}
