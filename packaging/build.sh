#!/usr/bin/env bash
# Build the relocatable bundle into ./dist
set -euo pipefail
cd "$(dirname "$0")/.."
git submodule update --init
podman build -f packaging/Containerfile --output type=local,dest=dist .
ls -la dist/