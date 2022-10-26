#!/usr/bin/env bash
# Copyright 2022 The Kubernetes Authors.
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

# Mark all existing Markdown files with a deprecation notice and redirect link.

set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "$0")" && pwd)"

for file; do
    if [[ "${file}" =~ /_print/index.html ]]; then
        echo "skipping file ${file}"
        continue
    fi
    echo "checking file ${file}"
    htmltest -c .htmltest.yml "${file}" | sed 's/^/  /'
    echo
done
