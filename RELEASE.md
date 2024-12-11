# Releasing

1. Decide what the version will be. Use a GitHub compare from the most recent release to the HEAD of the default branch e.g. https://github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/compare/v2.1.4...main
1. Find the most recent default branch build in the Buildkite pipeline
1. Unblock the block step supplying the version number that the tag will use, e.g. v2.1.5
1. Wait for the GitHub Release publish step to run
