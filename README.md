# AWS S3 Secrets Buildkite Plugin

A set of agent hooks that expose secrets to build steps via Amazon S3 (encrypted-at-rest). Used in the [Elastic CI Stack for AWS](https://github.com/buildkite/elastic-ci-stack-for-aws).

Different types of secrets are supported and exposed to your builds in appropriate ways:

- `ssh-agent` for SSH Private Keys
- Environment Variables for strings
- `git-credential` via git's credential.helper
- Other secrets, which must be suffixed with one of the following:
  - `_SECRET`
  - `_SECRET_KEY`
  - `_PASSWORD`
  - `_TOKEN`
  - `_ACCESS_KEY`

## Installation

The hooks needs to be installed directly in the agent so that secrets can be downloaded before jobs attempt checking out your repository. We are going to assume that buildkite has been installed at `/buildkite`, but this will vary depending on your operating system. Change the instructions accordingly.

The core of the hook is an `s3secrets-helper` binary. This can be built using
`go build` in the [`s3secrets-helper/`](s3secrets-helper) directory in this
repository, or downloaded from the assets attached to a [GitHub Release](https://github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/releases).
It must be placed in `$PATH` to be found by the `hooks/environment` wrapper script.

```bash
# clone to a path your buildkite-agent can access
git clone https://github.com/buildkite-plugins/s3-secrets-buildkite-plugin.git /buildkite/s3_secrets
(cd /buildkite/s3_secrets/s3secrets-helper && go build -o /usr/local/bin/s3secrets-helper)
```

Modify your agent's hooks (see [Hook Locations](https://buildkite.com/docs/agent/v3/hooks#hook-locations)):

### `${BUILDKITE_ROOT}/hooks/environment`

```bash
if [[ "${SECRETS_PLUGIN_ENABLED:-1}" == "1" ]] ; then
  export BUILDKITE_PLUGIN_S3_SECRETS_BUCKET="my-s3-secrets-bucket"

  source /buildkite/s3_secrets/hooks/environment
fi
```

### `${BUILDKITE_ROOT}/hooks/pre-exit`

```bash
if [[ "${SECRETS_PLUGIN_ENABLED:-1}" == "1" ]] ; then
  export BUILDKITE_PLUGIN_S3_SECRETS_BUCKET="my-s3-secrets-bucket"

  source /buildkite/s3_secrets/hooks/pre-exit
fi
```

## Usage

When run via the agent environment and pre-exit hook, your builds will check in the s3 secrets bucket you created for secrets files in the following formats:

- `s3://{bucket_name}/{pipeline}/private_ssh_key`
- `s3://{bucket_name}/{pipeline}/environment` or `s3://{bucket_name}/{pipeline}/env`
- `s3://{bucket_name}/{pipeline}/git-credentials`
- `s3://{bucket_name}/{pipeline}/secret-files/`
- `s3://{bucket_name}/private_ssh_key`
- `s3://{bucket_name}/environment` or `s3://{bucket_name}/env`
- `s3://{bucket_name}/git-credentials`
- `s3://{bucket_name}/secret-files/`


The private key is exposed to both the checkout and the command as an ssh-agent instance.
The secrets in the env file are exposed as environment variables, as are individual secret files.
The locations of git-credentials are passed via `GIT_CONFIG_PARAMETERS` environment to git.

## Secret Redaction

When using Buildkite Agent v3.67.0 or later, secrets are automatically redacted from build logs to prevent accidental exposure. The plugin will detect the agent version and use the built-in redactor feature when available.

For agents running older versions, a warning will be displayed recommending an upgrade for enhanced security.

## Uploading Secrets

### SSH Keys

This example uploads an ssh key and an environment file to the root of the bucket, which means it matches all pipelines that use it. You use per-pipeline overrides by adding a path prefix of `/my-pipeline/`.

```bash
# generate a deploy key for your project
ssh-keygen -t rsa -b 4096 -f id_rsa_buildkite
pbcopy < id_rsa_buildkite.pub # paste this into your github deploy key

export secrets_bucket=my-buildkite-secrets
aws s3 cp --acl private --sse aws:kms id_rsa_buildkite "s3://${secrets_bucket}/private_ssh_key"
```

Note the `-sse aws:kms`, as without this your secrets will fail to download.

### Git credentials

For git over https, you can use a `git-credentials` file with credential urls in the format of:

```bash
https://user:password@host/path/to/repo
```

```bash
aws s3 cp --acl private --sse aws:kms <(echo "https://user:password@host/path/to/repo") "s3://${secrets_bucket}/git-credentials"
```

These are then exposed via a [gitcredential helper](https://git-scm.com/docs/gitcredentials) which will download the
credentials as needed.

### Environment variables

Key values pairs can also be uploaded.

```bash
aws s3 cp --acl private --sse aws:kms <(echo "MY_SECRET=blah") "s3://${secrets_bucket}/environment"
```

### Individual Secrets

Individual secrets with a suffix of `_SECRET`, `_SECRET_KEY`, `_PASSWORD`, `_TOKEN`, or `_ACCESS_KEY` can be uploaded to the same location as the rest of your configuration, under an additional prefix of `/secret-files/`.

The file contents should be the secret value, and the object key becomes the environment variable name. For example:

```bash
aws s3 cp --acl private --sse aws:kms <(echo "<SECRET_VALUE>") "s3://${secrets_bucket}/secret-files/SPECIAL_SECRET"
```

## Options

There are a few environment variables you can configure for the s3secrets helper. You can set these options in an environment hook. 

#### `BUILDKITE_PLUGIN_S3_SECRETS_BUCKET`

An s3 bucket to look for secrets in. This can be configured via the BUILDKITE_PLUGIN_S3_SECRETS_BUCKET env var. 

#### `BUILDKITE_PLUGIN_S3_SECRETS_REGION`

The s3 bucket region to use when it cannot derive from both the configured bucket and the local AWS config.

#### `BUILDKITE_PLUGIN_S3_SECRETS_DUMP_ENV`

Enable debugging of the configured environment variables by dumping these variables in the job logs when true. False by default.

#### `BUILDKITE_PLUGIN_S3_SECRETS_SKIP_SSH_KEY_NOT_FOUND_WARNING`

Suppress log warnings when the repository SSH keys are not configured in the specified s3 secrets bucket when true. This can be useful when SSH Keys are configured outside the s3 secrets bucket. False by default.


## License

MIT (see [LICENSE](LICENSE))
