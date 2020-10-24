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
	envBucket   = "BUILDKITE_PLUGIN_S3_SECRETS_BUCKET"
	envPrefix   = "BUILDKITE_PLUGIN_S3_SECRETS_BUCKET_PREFIX"
	envPipeline = "BUILDKITE_PIPELINE_SLUG"
	envRepo     = "BUILDKITE_REPO"
	envEnvSink  = "BUILDKITE_PLUGIN_S3_SECRETS_ENV_SINK"

	gitCredentialsHelper = "git-credential-s3-secrets"
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

	client := s3.New()

	agent := &sshagent.Agent{}

	envSinkPath := os.Getenv(envEnvSink)
	if envSinkPath == "" {
		return fmt.Errorf("%s required", envEnvSink)
	}
	envSink, err := os.OpenFile(envSinkPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}

	//helper, err := exec.LookPath(gitCredentialsHelper)
	//if err != nil {
	//	return fmt.Errorf("error finding %s: %w", gitCredentialsHelper, err)
	//}
	helper := gitCredentialsHelper // TODO: find

	return secrets.Run(secrets.Config{
		Repo:                os.Getenv(envRepo),
		Bucket:              bucket,
		Prefix:              prefix,
		Client:              client,
		Logger:              log,
		SSHAgent:            agent,
		EnvSink:             envSink,
		GitCredentialHelper: helper,
	})
}
