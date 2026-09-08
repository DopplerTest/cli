#!/bin/bash

set -euo pipefail

TEST_NAME="analytics"

cleanup() {
  exit_code=$?
  if [ "$exit_code" -ne 0 ]; then
    echo "ERROR: '$TEST_NAME' tests failed during execution"
    afterAll || echo "ERROR: Cleanup failed"
  fi

  exit "$exit_code"
}
trap cleanup EXIT
trap cleanup INT

beforeAll() {
  echo "INFO: Executing '$TEST_NAME' tests"
}

beforeEach() {
  rm -rf ./temp-config
}

afterAll() {
  echo "INFO: Completed '$TEST_NAME' tests"
  beforeEach
}

error() {
  message=$1
  echo "$message"
  exit 1
}

beforeAll

beforeEach

# analytics defaults to disabled
status="$("$DOPPLER_BINARY" analytics status --configuration=./temp-config --json)"
[[ "$status" == '{"enabled":false}' ]] || error "ERROR: analytics not disabled"

beforeEach

# analytics defaults to disabled after clearing config
"$DOPPLER_BINARY" configure reset --configuration=./temp-config --yes
status="$("$DOPPLER_BINARY" analytics status --configuration=./temp-config --json)"
[[ "$status" == '{"enabled":false}' ]] || error "ERROR: analytics not disabled after reset"

beforeEach

# analytics defaults to disabled after enabling and then clearing config
"$DOPPLER_BINARY" analytics enable --configuration=./temp-config >/dev/null 2>&1
"$DOPPLER_BINARY" configure reset --configuration=./temp-config --yes
status="$("$DOPPLER_BINARY" analytics status --configuration=./temp-config --json)"
[[ "$status" == '{"enabled":false}' ]] || error "ERROR: analytics not disabled after enabling and resetting"

beforeEach

# analytics can be enabled
"$DOPPLER_BINARY" analytics enable --configuration=./temp-config >/dev/null 2>&1
status="$("$DOPPLER_BINARY" analytics status --configuration=./temp-config --json)"
[[ "$status" == '{"enabled":true}' ]] || error "ERROR: analytics not enabled"

beforeEach

# analytics can be disabled
"$DOPPLER_BINARY" analytics disable --configuration=./temp-config >/dev/null 2>&1
status="$("$DOPPLER_BINARY" analytics status --configuration=./temp-config --json)"
[[ "$status" == '{"enabled":false}' ]] || error "ERROR: analytics not disabled"

afterAll
