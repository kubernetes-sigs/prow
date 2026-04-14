#!/usr/bin/env bash
# Copyright The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# hack/phony.sh — send a fake GitHub webhook to the local dev hook instance.
# Usage: hack/phony.sh --event=<event> --payload=<file> [--address=<url>]
#
# Reads the HMAC token from the cluster and passes all arguments through to
# go run ./cmd/phony. The --address flag defaults to the local dev hook endpoint.

set -euo pipefail

HMAC=$(kubectl --context=kind-kind-prow-integration \
    get secret hmac-token -o jsonpath='{.data.hmac}' | base64 -d)

exec go run ./cmd/phony \
    --address="http://localhost:${DEV_HTTP_PORT:-8080}/hook" \
    --hmac="${HMAC}" \
    "$@"
