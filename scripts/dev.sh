#!/usr/bin/env bash
# Runs `go` inside golang:1.26 as the calling user, with caches under the
# repo so produced files stay owned by deploy. No host Go is used.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p .gopath/cache
exec docker run --rm --network host \
  -e HTTP_PROXY -e HTTPS_PROXY -e NO_PROXY \
  -e GOSUMDB=off -e GOFLAGS=-mod=mod \
  -e GOPATH=/src/.gopath -e GOCACHE=/src/.gopath/cache \
  -v "$PWD":/src -w /src \
  --user "$(id -u):$(id -g)" \
  golang:1.26 go "$@"
