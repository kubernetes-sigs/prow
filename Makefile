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

################################################################################
# ========================== Capture Environment ===============================
# get the repo root and output path
REPO_ROOT:=${CURDIR}
OUT_DIR=$(REPO_ROOT)/_output
# image building and publishing config
REGISTRY ?= gcr.io/k8s-prow
PROW_IMAGE ?=
################################################################################
# ================================= Testing ====================================
# unit tests (hermetic)
unit: go-unit
.PHONY: unit
go-unit:
	hack/make-rules/go-test/unit.sh
.PHONY: go-unit
# integration tests
# integration:
#	hack/make-rules/go-test/integration.sh
# all tests
test: unit
.PHONY: test
################################################################################
# ================================= Cleanup ====================================
# standard cleanup target
clean:
	rm -rf "$(OUT_DIR)/"
################################################################################
# ============================== Auto-Update ===================================
# update generated code, gofmt, etc.
# update:
#	hack/make-rules/update/all.sh
# update generated code
#generate:
#	hack/make-rules/update/generated.sh
# gofmt
#gofmt:
#	hack/make-rules/update/gofmt.sh
.PHONY: update-go-deps
update-go-deps:
	hack/make-rules/update/go-deps.sh
.PHONY: verify-go-deps
verify-go-deps:
	hack/make-rules/verify/go-deps.sh
################################################################################
# ================================== Dependencies ===================================
# python deps
ensure-py-requirements3:
	hack/run-in-python-container.sh pip3 install -r requirements3.txt
.PHONY: ensure-py-requirements3
################################################################################
# ================================== Linting ===================================
# run linters, ensure generated code, etc.
.PHONY: verify
verify:
	hack/make-rules/verify/all.sh
# go linters
.PHONY: go-lint
go-lint:
	hack/make-rules/verify/golangci-lint.sh
.PHONY: update-gofmt
update-gofmt:
	hack/make-rules/update/gofmt.sh
.PHONY: verify-gofmt
verify-gofmt:
	hack/make-rules/verify/gofmt.sh
.PHONY: update-file-perms
update-file-perms:
	hack/make-rules/update/file-perms.sh
.PHONY: verify-file-perms
verify-file-perms:
	hack/make-rules/verify/file-perms.sh
.PHONY: update-spelling
update-spelling:
	hack/make-rules/update/misspell.sh
.PHONY: verify-spelling
verify-spelling:
	hack/make-rules/verify/misspell.sh
.PHONY: update-codegen
update-codegen:
	hack/make-rules/update/codegen.sh
.PHONY: verify-codegen
verify-codegen:
	hack/make-rules/verify/codegen.sh
.PHONY: verify-boilerplate
verify-boilerplate: ensure-py-requirements3
	hack/make-rules/verify/boilerplate.sh
#################################################################################
# Build and push specific variables.
REGISTRY ?= gcr.io/k8s-prow
PROW_IMAGE ?=

.PHONY: push-images
push-images:
	hack/make-rules/go-run/arbitrary.sh run ./hack/prowimagebuilder --prow-images-file=./.prow-images.yaml --ko-docker-repo="${REGISTRY}" --push=true

.PHONY: build-images
build-images:
	hack/make-rules/go-run/arbitrary.sh run ./hack/prowimagebuilder --prow-images-file=./.prow-images.yaml --ko-docker-repo="ko.local" --push=false

.PHONY: push-single-image
push-single-image:
	hack/make-rules/go-run/arbitrary.sh run ./hack/prowimagebuilder --prow-images-file=./.prow-images.yaml --ko-docker-repo="${REGISTRY}" --push=true --image=${PROW_IMAGE}

.PHONY: build-single-image
build-single-image:
	hack/make-rules/go-run/arbitrary.sh run ./hack/prowimagebuilder --prow-images-file=./.prow-images.yaml --ko-docker-repo="ko.local" --push=false --image=${PROW_IMAGE}

.PHONY: build-tarball
build-tarball:
# use --ko-docker-repo="something.not.exist" as ko skips writing `.tar` file if
# it's `ko.local.
	hack/make-rules/go-run/arbitrary.sh run ./hack/prowimagebuilder --prow-images-file=./.prow-images.yaml --ko-docker-repo="something.not.exist" --push=false --image=${PROW_IMAGE}
