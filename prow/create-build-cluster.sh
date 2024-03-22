#!/usr/bin/env bash
# Copyright 2020 The Kubernetes Authors.
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

# This script is used to create a new build cluster for use with prow. The cluster will have a 
# single pd-ssd nodepool that will have autoupgrade and autorepair enabled.
#
# Usage: populate the parameters by setting them below or specifying environment variables then run
# the script and follow the prompts. You'll be prompted to share some credentials and commands
# with the current oncall.
#

set -o errexit
set -o nounset
set -o pipefail

# Specific to Prow instance. Use "k8s-prow" if onboarding prow.k8s.io
PROW_INSTANCE_NAME="${PROW_INSTANCE_NAME:-}"
# Crier and deck needs to access the GCS bucket. Use
# "control-plane@k8s-prow.iam.gserviceaccount.com" if onboarding prow.k8s.io
CONTROL_PLANE_SA="${CONTROL_PLANE_SA:-}"

PROW_SERVICE_PROJECT="${PROW_SERVICE_PROJECT:-k8s-prow}"
PROW_SECRET_ACCESSOR_SA="${PROW_SECRET_ACCESSOR_SA:-gencred-refresher@k8s-prow.iam.gserviceaccount.com}"

PROW_DEPLOYMENT_DIR="${PROW_DEPLOYMENT_DIR:-./config/prow/cluster}"
# URI for cloning your fork locally, for example "git@github.com:chaodaiG/test-infra.git"
GITHUB_FORK_URI="${GITHUB_FORK_URI:-}"
GITHUB_CLONE_URI="${GITHUB_CLONE_URI:-git@github.com:kubernetes/test-infra}"

# Specific to the build cluster
TEAM="${TEAM:-}"
PROJECT="${PROJECT:-${PROW_INSTANCE_NAME}-build-${TEAM}}"
ZONE="${ZONE:-us-west1-b}"
CLUSTER="${CLUSTER:-${PROJECT}}"
GCS_BUCKET="${GCS_BUCKET:-gs://${PROJECT}}"

# Only needed for creating cluster
MACHINE="${MACHINE:-n1-standard-8}"
NODECOUNT="${NODECOUNT:-5}"
DISKSIZE="${DISKSIZE:-100GB}"

# Only needed for creating project
FOLDER_ID="${FOLDER_ID:-0123}"
BILLING_ACCOUNT_ID="${BILLING_ACCOUNT_ID:-0123}"  # Find the billing account ID in the cloud console.
ADMIN_IAM_MEMBER="${ADMIN_IAM_MEMBER:-group:mdb.cloud-kubernetes-engprod-oncall@google.com}"

# Overriding output
OUT_FILE="${OUT_FILE:-build-cluster-kubeconfig.yaml}"


# Require bash version >= 4.4
if ((${BASH_VERSINFO[0]}<4)) || ( ((${BASH_VERSINFO[0]}==4)) && ((${BASH_VERSINFO[1]}<4)) ); then
  echo "ERROR: This script requires a minimum bash version of 4.4, but got version of ${BASH_VERSINFO[0]}.${BASH_VERSINFO[1]}"
  if [ "$(uname)" = 'Darwin' ]; then
    echo "On macOS with homebrew 'brew install bash' is sufficient."
  fi
  exit 1
fi

# Macos specific settings
SED="sed"
if command -v gsed &>/dev/null; then
  SED="gsed"
fi
if ! (${SED} --version 2>&1 | grep -q GNU); then
  # darwin is great (not)
  echo "!!! GNU sed is required.  If on OS X, use 'brew install gnu-sed'." >&2
  return 1
fi

# Create temp dir to work in and clone k/t-i

origdir="$( pwd -P )"
tempdir="$( mktemp -d )"
echo
echo "Temporary files produced are stored at: ${tempdir}"
echo
cd "${tempdir}"
git clone https://github.com/kubernetes/test-infra --depth=1
cd "${origdir}"

ROOT_DIR="${tempdir}/test-infra"

function main() {
  parseArgs "$@"
  ensureProject
  ensureBucket
  ensureCluster
  ensureUploadSA
  genConfig
  gencreds
  echo "All done!"
}
# Prep and check args.
function parseArgs() {
  for var in TEAM PROJECT ZONE CLUSTER MACHINE NODECOUNT DISKSIZE FOLDER_ID BILLING_ACCOUNT_ID GITHUB_FORK_URI; do
    if [[ -z "${!var}" ]]; then
      echo "Must specify ${var} environment variable (or specify a default in the script)."
      exit 2
    fi
    echo "${var}=${!var}"
  done
  if [[ "${PROW_INSTANCE_NAME}" != "k8s-prow" ]]; then
    if [[ "${PROW_SECRET_ACCESSOR_SA}" == "gencred-refresher@k8s-prow.iam.gserviceaccount.com" ]]; then
      echo "${PROW_SECRET_ACCESSOR_SA} is k8s-prow specific, must pass in the service account used by ${PROW_INSTANCE_NAME}"
      exit 2
    fi
    if [[ "${PROW_DEPLOYMENT_DIR}" == "./config/prow/cluster" ]]; then
      read -r -n1 -p "${PROW_DEPLOYMENT_DIR} is k8s-prow specific, are you sure this is the same for ${PROW_INSTANCE_NAME} ? [y/n] "
      if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 2
      fi
    fi
  fi
}
function prompt() {
  local msg="$1" cmd="$2"
  echo
  read -r -n1 -p "$msg ? [y/n] "
  echo
  if [[ $REPLY =~ ^[Yy]$ ]]; then
    "$cmd"
  else
    echo "Skipping and continuing to next step..."
  fi
}
function pause() {
  read -n 1 -s -r
}

authed=""
function getClusterCreds() {
  if [[ -z "${authed}" ]]; then
    gcloud container clusters get-credentials --project="${PROJECT}" --zone="${ZONE}" "${CLUSTER}"
    authed="true"
  fi
}
function ensureProject() {
  if gcloud projects describe ${PROJECT}; then
    echo "GCP project '${PROJECT}' exists, skip creating."
    return
  fi

  prompt "Failed to describe the project ${PROJECT}, press Y/y to create the project" echo
  # Create project, configure billing, enable GKE, add IAM rule for oncall team.
  echo "Creating project '${PROJECT}' (this may take a few minutes)..."
  gcloud projects create "${PROJECT}" --name="${PROJECT}" --folder="${FOLDER_ID}"
  gcloud beta billing projects link "${PROJECT}" --billing-account="${BILLING_ACCOUNT_ID}"
  gcloud services enable "container.googleapis.com" --project="${PROJECT}"
  gcloud projects add-iam-policy-binding "${PROJECT}" --member="${ADMIN_IAM_MEMBER}" --role="roles/owner"
}
function ensureCluster() {
  if gcloud container clusters describe "${CLUSTER}" --project="${PROJECT}" --zone="${ZONE}" >/dev/null 2>&1; then
    echo "Cluster '${CLUSTER}' exists in zone '${ZONE}' in project '${PROJECT}', skip creating."
    return
  fi

  prompt "Pressing Y/y to create the cluster" echo
  echo "Creating cluster '${CLUSTER}' (this may take a few minutes)..."
  echo "If this fails due to insufficient project quota, request more at https://console.cloud.google.com/iam-admin/quotas?project=${PROJECT}"
  echo
  gcloud container clusters create "${CLUSTER}" --project="${PROJECT}" --zone="${ZONE}" --machine-type="${MACHINE}" --num-nodes="${NODECOUNT}" --disk-size="${DISKSIZE}" --disk-type="pd-ssd" --enable-autoupgrade --enable-autorepair --workload-pool="${PROJECT}.svc.id.goog"
  getClusterCreds
  kubectl create namespace "test-pods"
}

function createBucket() {
  gsutil mb -p "${PROJECT}" -b on "${GCS_BUCKET}"
  for i in ${CONTROL_PLANE_SA//,/ }
  do
    gsutil iam ch "serviceAccount:${i}:roles/storage.objectAdmin" "${GCS_BUCKET}"
  done
}

function ensureBucket() {
  if ! gsutil ls "${GCS_BUCKET}"; then
    prompt "The specified GCS bucket '${GCS_BUCKET}' cannot be located. This is expected if this is a shared default job result bucket. Otherwise press Y/y to create it." createBucket
  else
    echo "Bucket '${GCS_BUCKET}' already exists, skip creation."
  fi
}

function ensureUploadSA() {
  getClusterCreds
  local sa="prowjob-default-sa"
  local saFull="${sa}@${PROJECT}.iam.gserviceaccount.com"
  # Create a GCP service account for uploading to GCS
  if ! gcloud beta iam service-accounts describe "${saFull}" --project="${PROJECT}" >/dev/null 2>&1; then
    gcloud beta iam service-accounts create "${sa}" --project="${PROJECT}" --description="Default SA for ProwJobs to use to upload job results to GCS." --display-name="ProwJob default SA"
  else
    echo "Service account '${sa}' already exists, skip creation."
  fi
  # Ensure workload identity is enabled on the cluster
  if ! gcloud container clusters describe ${CLUSTER} --project=${PROJECT} --zone=${ZONE} | grep "${CLUSTER}.svc.id.goog" >/dev/null 2>&1; then
    "${ROOT_DIR}/workload-identity/enable-workload-identity.sh" "${PROJECT}" "${ZONE}" "${CLUSTER}"
  else
    echo "Workload identity is enabled on cluster '${CLUSTER}', skip enabling."
  fi

  # Create a k8s service account to associate with the GCP service account
  if ! kubectl -n test-pods get ${sa}; then
    kubectl apply -f - <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  annotations:
    iam.gke.io/gcp-service-account: ${saFull}
  name: ${sa}
  namespace: test-pods
EOF
  fi

  echo "Binding GCP service account with k8s service account via workload identity. Propagation and validation may take a few minutes..."
  if ! gcloud iam service-accounts get-iam-policy --project=gob-prow prowjob-default-sa@gob-prow.iam.gserviceaccount.com | grep "${CLUSTER}.svc.id.goog[test-pods/${saFull}]" >/dev/null 2>&1; then
    "${ROOT_DIR}/workload-identity/bind-service-accounts.sh" "${PROJECT}" "${ZONE}" "${CLUSTER}" test-pods "${sa}" "${saFull}"
  fi

  # Try to authorize SA to upload to GCS_BUCKET. If this fails, the bucket if
  # probably a shared result bucket and oncall will need to handle.
  if ! gsutil iam get "${GCS_BUCKET}" | grep "serviceAccount:${saFull}" >/dev/null 2>&1; then
    if ! gsutil iam ch "serviceAccount:${saFull}:roles/storage.objectAdmin" "${GCS_BUCKET}"; then
      echo
      echo "It doesn't look you have permission to authorize access to this bucket. This is expected for the default job result bucket."
      echo "If this is a default job result bucket, please ask the test-infra oncall (https://go.k8s.io/oncall) to run the following:"
      echo "  gsutil iam ch \"serviceAccount:${saFull}:roles/storage.objectAdmin\" \"${GCS_BUCKET}\""
      echo
      echo "Press any key to acknowledge (this doesn't need to be completed to continue this script, but it needs to be done before uploading will work)..."
      pause
    fi
  fi
}

function genConfig() {
  # TODO: Automatically inject this into config.yaml at the same time as kubeconfig credential setup (which auto creates a PR we can include this in).
  echo
  echo "The following changes should be made to the Prow instance's config.yaml file (Probably located at ${PROW_DEPLOYMENT_DIR}/../config.yaml)."
  echo
  echo "Append the following entry to the end of the slice at field 'plank.default_decoration_config_entries': "
  cat <<EOF
  - cluster: $(cluster_alias)
    config:
      gcs_configuration:
        bucket: "${GCS_BUCKET#"gs://"}"
      default_service_account_name: "prowjob-default-sa" # Use workload identity
      gcs_credentials_secret: ""                         # rather than service account key secret
EOF
  echo
  echo "Press any key to acknowledge... This doesn't need to be merged to continue this script, but it needs to be done before configuring jobs for the cluster."
  pause
}

# generate a JWT kubeconfig file that we can merge into k8s-prow's kubeconfig
# secret so that Prow can schedule pods. This operation is now handled by a prow
# job runs gencred pediodically. So the only action from this function is
# authorizing prow service account to access the build cluster.
function gencreds() {
  # The secret can be stored in prow service cluster
  gcloud projects add-iam-policy-binding --member="serviceAccount:${PROW_SECRET_ACCESSOR_SA}" --role="roles/container.admin" "${PROJECT}" --condition=None

  prompt "Create CL for you" create_cl

  echo "ProwJobs that intend to use this cluster should specify 'cluster: $(cluster_alias)'" # TODO: color this
  echo
  echo "Press any key to acknowledge (this doesn't need to be completed to continue this script, but it needs to be done before Prow can schedule jobs to your cluster)..."
  pause
}

cluster_alias() {
  echo "build-${TEAM}"
}
gsm_secret_name() {
  echo "prow_build_cluster_kubeconfig_$(cluster_alias)"
}

create_cl() {
  local cluster_alias
  cluster_alias="$(cluster_alias)"
  local gsm_secret_name
  gsm_secret_name="$(gsm_secret_name)"
  local build_cluster_kubeconfig_mount_path="/etc/${cluster_alias}"
  local build_clster_secret_name_in_cluster="kubeconfig-build-${TEAM}"
  cd "${ROOT_DIR}"
  local fork
  fork="$(echo "${GITHUB_FORK_URI}" | "$SED" -e "s;https://github.com/;;" -e "s;git@github.com:;;" -e "s;.git;;")"
  
  cd "${tempdir}"
  git clone "${GITHUB_CLONE_URI}" forked-test-infra
  cd forked-test-infra
  git fetch

  git checkout -b add-build-cluster-secret-${TEAM}

  cat>>"${PROW_DEPLOYMENT_DIR}/kubernetes_external_secrets.yaml" <<EOF
---
apiVersion: kubernetes-client.io/v1
kind: ExternalSecret
metadata:
  name: ${build_clster_secret_name_in_cluster}
  namespace: default
spec:
  backendType: gcpSecretsManager
  projectId: ${PROW_SERVICE_PROJECT}
  data:
  - key: ${gsm_secret_name}
    name: kubeconfig
    version: latest
EOF

  # Also register this build cluster with gencred, so that the kubeconfig
  # secrets can be rotated.
  local gencred_config_file="${PROW_DEPLOYMENT_DIR}/../gencred-config/gencred-config.yaml"
  "${SED}" -i "s;clusters:;clusters:\\
- gke: projects/${PROJECT}/locations/${ZONE}/clusters/${CLUSTER}\\
  name: ${cluster_alias}\\
  duration: 48h\\
  gsm:\\
    name: ${gsm_secret_name}\\
    project: ${PROW_SERVICE_PROJECT};" "${gencred_config_file}"

  git add "${PROW_DEPLOYMENT_DIR}/kubernetes_external_secrets.yaml"
  git add "${gencred_config_file}"
  git commit -m "Add external secret from build cluster for ${TEAM}"
  git push -f "${GITHUB_FORK_URI}" "HEAD:add-build-cluster-secret-${TEAM}"

  git checkout -b use-build-cluster-${TEAM} master
  
  for app_deployment_file in ${PROW_DEPLOYMENT_DIR}/*.yaml; do
    if ! grep "/etc/kubeconfig/config" "${app_deployment_file}">/dev/null 2>&1; then
      if ! grep "name: KUBECONFIG" "${app_deployment_file}">/dev/null 2>&1; then
        continue
      fi
    fi
    "${SED}" -i "s;volumeMounts:;volumeMounts:\\
        - mountPath: ${build_cluster_kubeconfig_mount_path}\\
          name: ${cluster_alias}\\
          readOnly: true;" "${app_deployment_file}"

    "${SED}" -i "s;volumes:;volumes:\\
      - name: ${cluster_alias}\\
        secret:\\
          defaultMode: 420\\
          secretName: ${build_clster_secret_name_in_cluster};" "${app_deployment_file}"

    # Appends to an existing value doesn't seem to be supported by kustomize, so
    # using sed instead. `&` represents for regex matched part
    "${SED}" -E -i "s;/etc/kubeconfig/config(-[0-9]+)?;&:${build_cluster_kubeconfig_mount_path}/kubeconfig;" "${app_deployment_file}"
    git add "${app_deployment_file}"
  done

  git commit -m "Add build cluster kubeconfig for ${TEAM}

Please submit this change after the previous PR was submitted and postsubmit job succeeded.
Prow oncall: please don't submit this change until the secret is created successfully, which will be indicated by prow alerts in 2 minutes after the postsubmit job.
"

  git push -f "${GITHUB_FORK_URI}" "HEAD:use-build-cluster-${TEAM}"
  echo
  echo "Please open https://github.com/${fork}/pull/new/add-build-cluster-secret-${TEAM} and https://github.com/${fork}/pull/new/use-build-cluster-${TEAM}, creating PRs from both of them and assign to test-infra oncall for approval"
  echo
  pause
}

function cleanup() {
  returnCode="$?"
  rm -f "sa-key.json" || true
  rm -rf "${tempdir}" || true
  exit "${returnCode}"
}
trap cleanup EXIT
main "$@"
cleanup
