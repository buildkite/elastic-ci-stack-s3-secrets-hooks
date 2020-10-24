package main

import (
	"log"

	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/secrets"
)

func main() {
	if err := mainWithError(); err != nil {
		log.Fatal(err)
	}
}

func mainWithError() error {
	conf := secrets.Config{}
	if err := secrets.Run(conf); err != nil {
		return err
	}
	return nil
}
