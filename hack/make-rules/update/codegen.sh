#!/usr/bin/env bash
# Copyright 2021 The Kubernetes Authors.
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

set -o errexit
set -o nounset
set -o pipefail

# darwin is great
SED=sed
if which gsed &>/dev/null; then
  SED=gsed
fi
if ! (${SED} --version 2>&1 | grep -q GNU); then
  echo "!!! GNU sed is required.  If on OS X, use 'brew install gnu-sed'." >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
cd $REPO_ROOT

echo "Ensuring go version."
source ./hack/build/setup-go.sh

wrap-go-tool() {
  local tool="$1"
  local wrapper_name="${tool}-XXXX"
  wrapper="$(mktemp --tmpdir $wrapper_name)"
  printf '#!/usr/bin/bash\n\ngo tool %s $@\n' "$tool" >>"$wrapper"
  chmod +x "$wrapper"
  echo "$wrapper"
}
PROTOC_GEN_GO_WRAPPER="$(wrap-go-tool protoc-gen-go)"
PROTOC_GEN_GO_GRPC_WRAPPER="$(wrap-go-tool protoc-gen-go-grpc)"

ensure-protoc-deps() {
  # Install protoc
  if [[ ! -f "_bin/protoc/bin/protoc" ]]; then
    mkdir -p _bin/protoc
    OS="linux"
    if [[ $(uname -s) == "Darwin" ]]; then
          OS="osx"
    fi
    ARCH="x86_64"
    if [[ $(uname -m) == "arm64" ]]; then
      ARCH="aarch_64"
    fi
    # See https://developers.google.com/protocol-buffers/docs/news/2022-05-06 for
    # a note on the versioning scheme change.
    PROTOC_VERSION=25.2
    PROTOC_ZIP="protoc-${PROTOC_VERSION}-${OS}-${ARCH}.zip"
    curl -OL "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/${PROTOC_ZIP}"
    unzip -o $PROTOC_ZIP -d _bin/protoc bin/protoc
    unzip -o $PROTOC_ZIP -d _bin/protoc 'include/*'
    rm -f $PROTOC_ZIP
  fi

  # Clone proto dependencies.
  if ! [[ -f "${REPO_ROOT}"/_bin/protoc/include/googleapis/google/api/annotations.proto ]]; then
    # This SHA was retrieved on 2024-02-09.
    GOOGLEAPIS_VERSION="e183baf16610b925fc99e628fc539e118bc3348a"
    >/dev/null pushd "${REPO_ROOT}"/_bin/protoc/include
    curl -OL "https://github.com/googleapis/googleapis/archive/${GOOGLEAPIS_VERSION}.zip"
    >/dev/null unzip -o ${GOOGLEAPIS_VERSION}.zip
    mv googleapis-${GOOGLEAPIS_VERSION} googleapis
    rm -f ${GOOGLEAPIS_VERSION}.zip
    >/dev/null popd
  fi
}
ensure-protoc-deps

echo "Finished installations."

gen-prow-config-documented() {
  go run ./hack/gen-prow-documented
}

gen-deepcopy() {
  echo "Generating DeepCopy() methods..." >&2
  go tool deepcopy-gen ./... \
    --go-header-file hack/boilerplate/boilerplate.generated.go.txt \
    --output-file zz_generated.deepcopy.go \
    --bounding-dirs sigs.k8s.io/prow/pkg/apis,sigs.k8s.io/prow/pkg/config
}

gen-client() {
  echo "Generating client..." >&2
  go tool client-gen \
    --go-header-file hack/boilerplate/boilerplate.generated.go.txt \
    --clientset-name versioned \
    --input-base "" \
    --input sigs.k8s.io/prow/pkg/apis/prowjobs/v1 \
    --output-dir pkg/client/clientset \
    --output-pkg sigs.k8s.io/prow/pkg/client/clientset

  echo "Generating client for pipeline..." >&2
  go tool client-gen \
    --go-header-file hack/boilerplate/boilerplate.generated.go.txt \
    --clientset-name versioned \
    --input-base "" \
    --input github.com/tektoncd/pipeline/pkg/apis/pipeline/v1 \
    --output-dir pkg/pipeline/clientset \
    --output-pkg sigs.k8s.io/prow/pkg/pipeline/clientset
}

gen-lister() {
  echo "Generating lister..." >&2
  go tool lister-gen sigs.k8s.io/prow/pkg/apis/prowjobs/v1 \
    --go-header-file hack/boilerplate/boilerplate.generated.go.txt \
    --output-dir pkg/client/listers \
    --output-pkg sigs.k8s.io/prow/pkg/client/listers

  echo "Generating lister for pipeline..." >&2
  go tool lister-gen github.com/tektoncd/pipeline/pkg/apis/pipeline/v1 \
    --go-header-file hack/boilerplate/boilerplate.generated.go.txt \
    --output-dir pkg/pipeline/listers \
    --output-pkg sigs.k8s.io/prow/pkg/pipeline/listers
}

gen-informer() {
  echo "Generating informer..." >&2
  go tool informer-gen sigs.k8s.io/prow/pkg/apis/prowjobs/v1 \
    --go-header-file hack/boilerplate/boilerplate.generated.go.txt \
    --versioned-clientset-package sigs.k8s.io/prow/pkg/client/clientset/versioned \
    --listers-package sigs.k8s.io/prow/pkg/client/listers \
    --output-dir pkg/client/informers \
    --output-pkg sigs.k8s.io/prow/pkg/client/informers

  echo "Generating informer for pipeline..." >&2
  go tool informer-gen github.com/tektoncd/pipeline/pkg/apis/pipeline/v1 \
    --go-header-file hack/boilerplate/boilerplate.generated.go.txt \
    --versioned-clientset-package sigs.k8s.io/prow/pkg/pipeline/clientset/versioned \
    --listers-package sigs.k8s.io/prow/pkg/pipeline/listers \
    --output-dir pkg/pipeline/informers \
    --output-pkg sigs.k8s.io/prow/pkg/pipeline/informers
}

gen-spyglass-bindata() {
  cd pkg/spyglass/lenses/common/
  echo "Generating spyglass bindata..." >&2
  go tool go-bindata -pkg=common static/
  gofmt -s -w ./
  cd - >/dev/null
}

gen-prowjob-crd() {
  echo "Generating prowjob crd..." >&2
  go tool controller-gen crd:crdVersions=v1 paths=./pkg/apis/prowjobs/v1 output:stdout \
    | $SED '/^$/d' \
    | $SED '/^spec:.*/a  \  preserveUnknownFields: false' \
    | $SED '/^  annotations.*/a  \    api-approved.kubernetes.io: https://github.com/kubernetes/test-infra/pull/8669' \
    | $SED '/^          status:/r'<(cat<<EOF
            anyOf:
            - not:
                properties:
                  state:
                    enum:
                    - "success"
                    - "failure"
                    - "error"
            - required:
              - completionTime
EOF
    ) > ./config/prow/cluster/prowjob-crd/prowjob_customresourcedefinition.yaml
}

# Generate gRPC stubs for a given protobuf file.
gen-proto-stubs() {
  local dir
  dir="$(dirname "$1")"

  # We need the "paths=source_relative" bits to prevent a nested directory
  # structure (so that the generated files can sit next to the .proto files,
  # instead of under a "k8.io/test-infra/prow/..." subfolder).
  "${REPO_ROOT}/_bin/protoc/bin/protoc" \
    --plugin=protoc-gen-go="$PROTOC_GEN_GO_WRAPPER" \
    --plugin=protoc-gen-go-grpc="$PROTOC_GEN_GO_GRPC_WRAPPER" \
    --proto_path="${REPO_ROOT}/_bin/protoc/include/google/protobuf" \
    --proto_path="${REPO_ROOT}/_bin/protoc/include/googleapis" \
    --proto_path="$dir" \
    --go_out="$dir" \
    --go_opt=paths=source_relative \
    --go-grpc_out="$dir" \
    --go-grpc_opt=paths=source_relative \
    "$1"
}

gen-all-proto-stubs() {
  echo >&2 "Generating proto stubs"

  while IFS= read -r -d '' proto; do
    echo >&2 "  $proto"
    gen-proto-stubs "$proto"
  done < <(find "${REPO_ROOT}" \
    -not '(' -path "${REPO_ROOT}/vendor" -prune ')' \
    -not '(' -path "${REPO_ROOT}/hack/tools/vendor" -prune ')' \
    -not '(' -path "${REPO_ROOT}/node_modules" -prune ')' \
    -not '(' -path "${REPO_ROOT}/_bin" -prune ')' \
    -name '*.proto' \
    -print0 | sort -z)
}

gen-gangway-apidescriptorpb-for-cloud-endpoints() {
  echo >&2 "Generating self-describing proto stub (gangway_api_descriptor.pb) for gangway.proto"

  "${REPO_ROOT}/_bin/protoc/bin/protoc" \
    "--proto_path=${REPO_ROOT}/_bin/protoc/include/google/protobuf" \
    "--proto_path=${REPO_ROOT}/_bin/protoc/include/googleapis" \
    "--proto_path=${REPO_ROOT}/pkg/gangway" \
    --include_imports \
    --include_source_info \
    --descriptor_set_out "${REPO_ROOT}/pkg/gangway/gangway_api_descriptor.pb" \
    gangway.proto
}

gen-prow-config-documented

export GO111MODULE=on
export GOPROXY=https://proxy.golang.org
export GOSUMDB=sum.golang.org

gen-deepcopy
gen-client
gen-lister
gen-informer
gen-spyglass-bindata
gen-prowjob-crd
gen-all-proto-stubs
gen-gangway-apidescriptorpb-for-cloud-endpoints
