#!/bin/bash

set -euxo pipefail

pushd s3secrets-helper

mkdir -p pkg

go build -o pkg/s3secrets-helper

pushd pkg

buildkite-agent artifact upload s3secrets-helper "pkg/${GOOS}-${GOARCH}/"