package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/buildkiteagent"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/env"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/s3"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/secrets"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/sshagent"
)

func main() {
	log := log.New(os.Stderr, "", log.Lmsgprefix)
	if err := mainWithError(log); err != nil {
		log.Fatalf("fatal error: %v", err)
	}
}

func mainWithError(log *log.Logger) error {
	bucket := os.Getenv(env.EnvBucket)
	if bucket == "" {
		return nil
	}

	// May be empty string
	regionHint := os.Getenv(env.EnvRegion)

	prefix := os.Getenv(env.EnvPrefix)
	if prefix == "" {
		prefix = os.Getenv(env.EnvPipeline)
	}
	if prefix == "" {
		return fmt.Errorf("One of the %s or %s environment variables is required, set one to configure the bucket key prefix that is scanned for secrets.", env.EnvPrefix, env.EnvPipeline)
	}

	client, err := s3.New(log, bucket, regionHint)
	if err != nil {
		return err
	}

	agent := &sshagent.Agent{}

	buildkiteAgent := buildkiteagent.New()

	credHelper := os.Getenv(env.EnvCredHelper)
	if credHelper == "" {
		return fmt.Errorf("The %s environment variable is required, set it to the path of the git-credential-s3-secrets script.", env.EnvCredHelper)
	}

	return secrets.Run(&secrets.Config{
		Repo:                      os.Getenv(env.EnvRepo),
		Bucket:                    bucket,
		Prefix:                    prefix,
		Client:                    client,
		Logger:                    log,
		SSHAgent:                  agent,
		BuildkiteAgent:            buildkiteAgent,
		EnvSink:                   os.Stdout,
		GitCredentialHelper:       credHelper,
		SkipSSHKeyNotFoundWarning: isEnvVarEnabled(env.EnvSkipSSHKeyNotFoundWarning),
	})
}

func isEnvVarEnabled(envVar string) bool {
	value := os.Getenv(envVar)
	return strings.ToLower(value) == "true" || value == "1"
}
