#!/usr/bin/env bats

load '/usr/local/lib/bats/load.bash'

setup() {
  # emulate the upcoming bats `setup_file`
  # https://github.com/bats-core/bats-core/issues/39#issuecomment-377015447
  if [[ $BATS_TEST_NUMBER -eq 1 ]]; then
    # output to fd 3, prefixed with hashes, for TAP compliance:
    # https://github.com/bats-core/bats-core/blob/v1.2.0/README.md#printing-to-the-terminal
    apk --no-cache add jq | sed -e 's/^/# /' >&3
  fi
}

# export CURL_STUB_DEBUG=/dev/tty
# export AWS_STUB_DEBUG=/dev/tty
# export SSH_ADD_STUB_DEBUG=/dev/tty
# export SSH_AGENT_STUB_DEBUG=/dev/tty
# export GIT_STUB_DEBUG=/dev/tty

# shell snippet to assert IMDS credentials were passed via environment
imds_assertion='if [[ $AWS_ACCESS_KEY_ID != "AKID" || $AWS_SECRET_ACCESS_KEY != "SAK" || $AWS_SESSION_TOKEN != "ST" ]]; then echo >&2 missing IMDS credentials; exit 1; fi'

@test "Show unexpected s3 errors" {
  export BUILDKITE_PLUGIN_S3_SECRETS_BUCKET=my_secrets_bucket
  export BUILDKITE_PLUGIN_S3_SECRETS_DUMP_ENV=true
  export BUILDKITE_PIPELINE_SLUG=test

  curlopts="--silent --show-error --fail --connect-timeout 1 --retry 2"
  stub curl \
    "$curlopts -H 'X-aws-ec2-metadata-token-ttl-seconds: 300' -X PUT http://169.254.169.254/latest/api/token : echo imdstoken" \
    "$curlopts -H 'X-aws-ec2-metadata-token: imdstoken' http://169.254.169.254/latest/meta-data/iam/security-credentials : echo testrole" \
    "$curlopts -H 'X-aws-ec2-metadata-token: imdstoken' http://169.254.169.254/latest/meta-data/iam/security-credentials/testrole : cat tests/fixtures/imds-creds.json"

  stub aws \
    "s3api head-bucket --bucket my_secrets_bucket : $imds_assertion; echo Forbidden; exit 0" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/private_ssh_key : $imds_assertion; echo Unexpected llamas >&2; exit 1"

  run bash -c "$PWD/hooks/environment && $PWD/hooks/pre-exit"

  assert_failure
  assert_output --partial "Unexpected llamas"

  unstub aws
  unstub curl
}

@test "Load env file from s3 bucket" {
  export BUILDKITE_PLUGIN_S3_SECRETS_BUCKET=my_secrets_bucket
  export BUILDKITE_PLUGIN_S3_SECRETS_DUMP_ENV=true
  export BUILDKITE_PIPELINE_SLUG=test

  stub ssh-agent "-s : echo export SSH_AGENT_PID=93799"

  curlopts="--silent --show-error --fail --connect-timeout 1 --retry 2"
  stub curl \
    "$curlopts -H 'X-aws-ec2-metadata-token-ttl-seconds: 300' -X PUT http://169.254.169.254/latest/api/token : echo imdstoken" \
    "$curlopts -H 'X-aws-ec2-metadata-token: imdstoken' http://169.254.169.254/latest/meta-data/iam/security-credentials : echo testrole" \
    "$curlopts -H 'X-aws-ec2-metadata-token: imdstoken' http://169.254.169.254/latest/meta-data/iam/security-credentials/testrole : cat tests/fixtures/imds-creds.json"

  stub aws \
    "s3api head-bucket --bucket my_secrets_bucket : $imds_assertion; echo Forbidden; exit 0" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/private_ssh_key : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/id_rsa_github : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key private_ssh_key : $imds_assertion; exit 0" \
    "s3 cp --only-show-errors --region=us-east-1 --sse aws:kms s3://my_secrets_bucket/private_ssh_key - : $imds_assertion; echo secret material" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key id_rsa_github : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key env : $imds_assertion; exit 0" \
    "s3 cp --only-show-errors --region=us-east-1 --sse aws:kms s3://my_secrets_bucket/env - : $imds_assertion; echo SECRET=24" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key environment : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/env : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/environment : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key git-credentials : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/git-credentials : $imds_assertion; echo Forbidden >&2; exit 1"

  stub ssh-add \
    "- : echo added ssh key"

  run bash -c "$PWD/hooks/environment && $PWD/hooks/pre-exit"

  assert_success
  assert_output --partial "ssh-agent (pid 93799)"
  assert_output --partial "added ssh key"
  assert_output --partial "SECRET=24"

  unstub ssh-add
  unstub aws
  unstub curl
  unstub ssh-agent
}

@test "Load env file from s3 bucket without IMDS credential success" {
  export BUILDKITE_PLUGIN_S3_SECRETS_BUCKET=my_secrets_bucket
  export BUILDKITE_PLUGIN_S3_SECRETS_DUMP_ENV=true
  export BUILDKITE_PIPELINE_SLUG=test

  stub ssh-agent "-s : echo export SSH_AGENT_PID=93799"

  curlopts="--silent --show-error --fail --connect-timeout 1 --retry 2"
  stub curl \
    "$curlopts -H 'X-aws-ec2-metadata-token-ttl-seconds: 300' -X PUT http://169.254.169.254/latest/api/token : exit 1"

  stub aws \
    "s3api head-bucket --bucket my_secrets_bucket : exit 0" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/private_ssh_key : echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/id_rsa_github : echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key private_ssh_key : exit 0" \
    "s3 cp --only-show-errors --region=us-east-1 --sse aws:kms s3://my_secrets_bucket/private_ssh_key - : echo secret material" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key id_rsa_github : echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key env : exit 0" \
    "s3 cp --only-show-errors --region=us-east-1 --sse aws:kms s3://my_secrets_bucket/env - : echo SECRET=24" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key environment : echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/env : echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/environment : echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key git-credentials : echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/git-credentials : echo Forbidden >&2; exit 1"

  stub ssh-add \
    "- : echo added ssh key"

  run bash -c "$PWD/hooks/environment && $PWD/hooks/pre-exit"

  assert_success
  assert_output --partial "ssh-agent (pid 93799)"
  assert_output --partial "added ssh key"
  assert_output --partial "SECRET=24"

  unstub ssh-add
  unstub aws
  unstub curl
  unstub ssh-agent
}

@test "Load git-credentials from bucket into GIT_CONFIG_PARAMETERS" {
  export BUILDKITE_PLUGIN_S3_SECRETS_BUCKET=my_secrets_bucket
  export BUILDKITE_PLUGIN_S3_SECRETS_DUMP_ENV=true
  export BUILDKITE_PIPELINE_SLUG=test
  unset SSH_AGENT_PID

  curlopts="--silent --show-error --fail --connect-timeout 1 --retry 2"
  stub curl \
    "$curlopts -H 'X-aws-ec2-metadata-token-ttl-seconds: 300' -X PUT http://169.254.169.254/latest/api/token : echo imdstoken" \
    "$curlopts -H 'X-aws-ec2-metadata-token: imdstoken' http://169.254.169.254/latest/meta-data/iam/security-credentials : echo testrole" \
    "$curlopts -H 'X-aws-ec2-metadata-token: imdstoken' http://169.254.169.254/latest/meta-data/iam/security-credentials/testrole : cat tests/fixtures/imds-creds.json"

  stub aws \
    "s3api head-bucket --bucket my_secrets_bucket : $imds_assertion; echo Forbidden; exit 0" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/private_ssh_key : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/id_rsa_github : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key private_ssh_key : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key id_rsa_github : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key env : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key environment : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/env : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/environment : $imds_assertion; echo Forbidden >&2; exit 1" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key git-credentials : $imds_assertion; exit 0" \
    "s3api head-object --region=us-east-1 --bucket my_secrets_bucket --key test/git-credentials : $imds_assertion; exit 0"

  run bash -c "$PWD/hooks/environment && $PWD/hooks/pre-exit"

  assert_success
  assert_output --partial "Adding git-credentials in git-credentials as a credential helper"
  assert_output --partial "Adding git-credentials in test/git-credentials as a credential helper"
  assert_output --partial "GIT_CONFIG_PARAMETERS='credential.helper="

  unstub aws
  unstub curl
}
