#!/usr/bin/env bash
set -euo pipefail

cd -- "$(dirname -- "$0")"
docker buildx build --load --platform linux/amd64 --network host --progress plain \
  --label org.opencontainers.image.title=cilihub \
  --label org.opencontainers.image.source=https://github.com/nathanxiangang-web/cilihub \
  --label "org.opencontainers.image.revision=$(git rev-parse HEAD)" \
  --label org.opencontainers.image.licenses=PolyForm-Noncommercial-1.0.0 \
  -t cilihub:local .
