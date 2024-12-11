#!/bin/bash

set -euo pipefail

echo '--- Getting release version from buildkite meta-data'

VERSION=$(buildkite-agent meta-data get 'version')

if [ -z "${VERSION}" ]
then
	echo "Error: Missing \$VERSION, set buildkite-agent meta-data version before invoking this step"
	exit 1
fi

if [ -z "${GITHUB_RELEASE_ACCESS_TOKEN}" ]
then
  echo "Error: Missing \$GITHUB_RELEASE_ACCESS_TOKEN"
  exit 1
fi

echo '--- Downloading releases'

rm -rf pkg
mkdir -p pkg
buildkite-agent artifact download "pkg/*" .

echo '--- Creating GitHub Release'

export GITHUB_RELEASE_REPOSITORY="buildkite/elastic-ci-stack-s3-secrets-hooks"

github-release "${VERSION}" pkg/* --commit "$(git rev-parse HEAD)"
