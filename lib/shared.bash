#!/bin/bash

declare -a s3_cache_found
declare -a s3_cache_not_found

s3_exists_preload() {
  local bucket="$1"; shift
  local keys=("$@")

  echo >&2 "Searching $bucket"
  declare -a pid_to_cache_key
  declare -a pid_to_key
  for key in "${keys[@]}"; do
    cache_key="$bucket/$key"
    s3_exists "$bucket" "$key" &
    pid=$!
    pid_to_cache_key[$pid]="$cache_key"
    pid_to_key[$pid]="$key"
  done
  declare -a keys_found
  declare -a keys_not_found
  for pid in "${!pid_to_cache_key[@]}"; do
    cache_key=${pid_to_cache_key[$pid]}
    key=${pid_to_key[$pid]}
    if wait "$pid"; then
      s3_cache_found+=("$cache_key")
      keys_found+=("$key")
    else
      s3_cache_not_found+=("$cache_key")
      keys_not_found+=("$key")
    fi
  done
  echo >&2 "Found:"
  for key in "${keys_found[@]}"; do
    echo >&2 "- $key"
  done
  echo >&2 "Not found:"
  for key in "${keys_not_found[@]}"; do
    echo >&2 "- $key"
  done
}

s3_exists() {
  local bucket="$1"
  local key="$2"
  local aws_s3_args=("--region=$AWS_DEFAULT_REGION")

  cache_key="$bucket/$key"
  for found in "${s3_cache_found[@]-}"; do
    if [[ $found == "$cache_key" ]]; then
      return 0
    fi
  done
  for not_found in "${s3_cache_not_found[@]-}"; do
    if [[ $not_found == "$cache_key" ]]; then
      return 1
    fi
  done

  # capture just stderr
  output=$(aws s3api head-object "${aws_s3_args[@]}" --bucket "$bucket" --key "$key" 2>&1 >/dev/null)
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
  if ! aws s3api head-bucket --bucket "$bucket" &>/dev/null ; then
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

  if ! aws s3 cp "${aws_s3_args[@]}" "s3://${bucket}/${key}" - ; then
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
