#!/bin/bash

set -eu -o pipefail -o functrace

# Verifies that each published image is a manifest list containing exactly the expected platforms.

if [ $# -lt 3 ]; then
  echo "Usage: $0 IS_SNAPSHOT PLATFORMS IMAGE [IMAGE...]"
  exit 1
fi

IS_SNAPSHOT="$1"
EXPECTED="$(echo "$2" | tr ',' '\n' | sort)"
shift 2

# snapshot images are built one platform at a time and never pushed, so there's no manifest list to inspect
if [ "$IS_SNAPSHOT" == "true" ]; then
  echo "Snapshot build, skipping image verification"
  exit 0
fi

# variants (ex. arm64's "v8") are intentionally omitted; the registry may or may not record them
FORMAT='{{ range .Manifest.Manifests }}{{ .Platform.OS }}/{{ .Platform.Architecture }}{{ "\n" }}{{ end }}'

for image in "$@"; do
  if ! actual="$(docker buildx imagetools inspect "$image" --format "$FORMAT" 2>&1)"; then
    echo "Unable to read the platforms of $image, it may not be a manifest list:"
    echo "$actual"
    exit 1
  fi

  # drop the blank lines the template emits between entries
  actual="$(echo "$actual" | grep . | sort)"

  if [ "$actual" != "$EXPECTED" ]; then
    echo "$image was not published for the expected platforms"
    echo "Expected: $(echo "$EXPECTED" | tr '\n' ' ')"
    echo "Actual:   $(echo "$actual" | tr '\n' ' ')"
    exit 1
  fi

  echo "$image: $(echo "$actual" | tr '\n' ' ')"
done
