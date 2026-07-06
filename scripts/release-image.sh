#!/usr/bin/env bash
# Build and push the release image; invoked by semantic-release with the
# freshly computed version. Requires a prior docker login to the registry.
set -euo pipefail

VERSION="$1"
IMAGE="harbor.floret.dev/whisker/whisker-server"

docker build -t "$IMAGE:$VERSION" -t "$IMAGE:latest" .
docker push "$IMAGE:$VERSION"
docker push "$IMAGE:latest"
