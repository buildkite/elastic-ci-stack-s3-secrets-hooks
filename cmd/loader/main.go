package main

import (
	"fmt"
	"log"
	"os"

	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/secrets"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/sshagent"
)

const (
	envBucket     = "BUILDKITE_PLUGIN_S3_SECRETS_BUCKET"
	envPrefix     = "BUILDKITE_PLUGIN_S3_SECRETS_BUCKET_PREFIX"
	envPipeline   = "BUILDKITE_PIPELINE_SLUG"
	envRepo       = "BUILDKITE_REPO"
	envEnvSink    = "BUILDKITE_PLUGIN_S3_SECRETS_ENV_SINK"
	envCredHelper = "BUILDKITE_PLUGIN_S3_SECRETS_GIT_CREDENTIAL_HELPER"

	envDefaultRegion = "AWS_DEFAULT_REGION"
	defaultRegion    = "us-east-1"
)

func main() {
	log := log.New(os.Stderr, "[secrets] ", log.Lmsgprefix)
	if err := mainWithError(log); err != nil {
		log.Fatalf("fatal error: %v", err)
	}
}

func mainWithError(log *log.Logger) error {
	bucket := os.Getenv(envBucket)
	if bucket == "" {
		return nil
	}

	prefix := os.Getenv(envPrefix)
	if prefix == "" {
		prefix = os.Getenv(envPipeline)
	}
	if prefix == "" {
		return fmt.Errorf("%s or %s required", envPrefix, envPipeline)
	}

	region := os.Getenv(envDefaultRegion)
	if region == "" {
		region = defaultRegion
	}
	client, err := s3.New(region)
	if err != nil {
		return err
	}

	agent := &sshagent.Agent{}

	envSinkPath := os.Getenv(envEnvSink)
	if envSinkPath == "" {
		return fmt.Errorf("%s required", envEnvSink)
	}
	envSink, err := os.OpenFile(envSinkPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}

	credHelper := os.Getenv(envCredHelper)
	if credHelper == "" {
		return fmt.Errorf("%s required", envCredHelper)
	}

	return secrets.Run(secrets.Config{
		Repo:                os.Getenv(envRepo),
		Bucket:              bucket,
		Prefix:              prefix,
		Client:              client,
		Logger:              log,
		SSHAgent:            agent,
		EnvSink:             envSink,
		GitCredentialHelper: credHelper,
	})
}
