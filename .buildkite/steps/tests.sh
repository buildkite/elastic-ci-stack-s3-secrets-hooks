#!/usr/bin/env bash
set -euo pipefail

echo "--- Running Go tests"

cd s3secrets-helper

go version
echo arch is "$(uname -m)"

go install gotest.tools/gotestsum@v1.8.0

if [[ "$(go env GOOS)" == "windows" ]]; then
  gotestsum --junitfile="junit-${BUILDKITE_JOB_ID:-local}.xml" -- -count=1 -race ./...
else
  gotestsum --junitfile="junit-${BUILDKITE_JOB_ID:-local}.xml" -- -count=1 -race -cover ./...
fi

echo "--- Go tests completed successfully"
