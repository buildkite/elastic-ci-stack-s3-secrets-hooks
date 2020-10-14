#!/bin/bash

# imds_env is used to store instance role credentials fetched from the instance
# metadata service. They should not be exported, because (a) we don't want to
# overwrite existing AWS_ACCESS_KEY_ID etc, and (b) we don't want them to leak
# beyond this hook. Instead they're explicitly passed to aws CLI calls.
imds_env=("_=") # placeholder to avoid empty array issues

# global var cache for IMDSv2 token
imds_token=""

# curl options used by imds functions
imds_curlopt=(--silent --show-error --fail --connect-timeout 1 --retry 2)

s3_exists() {
  local bucket="$1"
  local key="$2"
  local aws_s3_args=("--region=$AWS_DEFAULT_REGION")

  # capture just stderr
  output=$(aws_cli s3api head-object "${aws_s3_args[@]}" --bucket "$bucket" --key "$key" 2>&1 >/dev/null)
  exitcode=$?

  # If we didn't get a Not Found or Forbidden then show the error and return exit code 2
  if [[ $exitcode -ne 0 && ! $output =~ (Not Found|Forbidden)$ ]] ; then
    echo "$output" >&2
    return 2
  elif [[ $exitcode -ne 0 ]] ; then
    return 1
  fi
}

s3_bucket_exists() {
  local bucket="$1"
  if ! aws_cli s3api head-bucket --bucket "$bucket" &>/dev/null ; then
    return 1
  fi
}

s3_download() {
  local bucket="$1"
  local key="$2"
  local aws_s3_args=("--only-show-errors" "--region=$AWS_DEFAULT_REGION")

  if [[ "${BUILDKITE_USE_KMS:-true}" =~ ^(true|1)$ ]] ; then
    aws_s3_args+=("--sse" "aws:kms")
  fi

  if ! aws_cli s3 cp "${aws_s3_args[@]}" "s3://${bucket}/${key}" - ; then
    return 1
  fi
}

add_ssh_private_key_to_agent() {
  local ssh_key="$1"

  if [[ -z "${SSH_AGENT_PID:-}" ]] ; then
    echo "Starting an ephemeral ssh-agent" >&2;
    eval "$(ssh-agent -s)"
  fi

  echo "Loading ssh-key into ssh-agent (pid ${SSH_AGENT_PID:-})" >&2;
  echo "$ssh_key" | env SSH_ASKPASS="/bin/false" ssh-add -
}

aws_cli() {
  env "${imds_env[@]}" aws "$@"
}

imds_get_token() {
  echo >&2 "ec2-metadata: acquiring IMDSv2 token"
  imds_token="$(curl "${imds_curlopt[@]}" \
    -H "X-aws-ec2-metadata-token-ttl-seconds: 300" \
    -X PUT "http://169.254.169.254/latest/api/token")"
  if [[ -z $imds_token ]]; then
    echo >&2 "ec2-metadata: failed to acquire IMDSv2 token"
    return 1
  fi
}

imds_get() {
  local path="/latest/meta-data/$1"
  echo >&2 "ec2-metadata: getting $path"
  curl "${imds_curlopt[@]}" -H "X-aws-ec2-metadata-token: $imds_token" "http://169.254.169.254$path"
}

imds_credentials_to_env() {
  jq --raw-output '
    "AWS_ACCESS_KEY_ID=\(.AccessKeyId) "+
    "AWS_SECRET_ACCESS_KEY=\(.SecretAccessKey) "+
    "AWS_SESSION_TOKEN=\(.Token)"
  '
}

imds_load_credentials() {
  if ! imds_get_token; then return 1; fi
  local role; role=$(imds_get iam/security-credentials | head -n1)
  if [[ -z $role ]]; then
    echo >&2 "Instance role not found"
    return 1
  fi
  local env; env=$(imds_get "iam/security-credentials/$role" | imds_credentials_to_env)
  if [[ -z $env ]]; then
    echo >&2 "Failed to process response from Instance Metadata Service"
    return 1
  fi
  read -r -a imds_env <<< "$env"
}
