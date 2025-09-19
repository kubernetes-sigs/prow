#!/usr/bin/env bash
# Copyright 2024 The Kubernetes Authors.
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

# usage: source this script to ensure kind is setup

# ensure kind is available for setup-kind-cluster.sh etc
# TODO: consider factoring out the kind version used for CI or
# else ensure all CI jobs use this script
# TODO: consider enforcing a particular version locally as well to avoid confusion
if ! command -v kind; then
    local repo_gobin="${REPO_ROOT}/_bin"
    GOBIN="${repo_gobin}" go install sigs.k8s.io/kind@v0.30.0
    export PATH="${repo_gobin}:${PATH}"
fi
