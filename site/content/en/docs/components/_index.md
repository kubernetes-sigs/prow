---
title: "Components"
weight: 30
---

## Prow Images

This directory includes a sub directory for every Prow component and is where all binary and container images are built. You can find the `main` packages here. For details about building the binaries and images see ["Building, Testing, and Updating Prow"](/docs/build-test-update/).

## Cluster Components

Prow has a microservice architecture implemented as a collection of container images that run as Kubernetes deployments. A brief description of each service component is provided here.

#### Core Components

* [`crier`](/docs/components/core/crier/) reports on ProwJob status changes. Can be configured to report to gerrit, github, pubsub, slack, etc.
* [`deck`](/docs/components/core/deck/) presents a nice view of [recent jobs](https://prow.k8s.io/), [command](https://prow.k8s.io/command-help) and [plugin](https://prow.k8s.io/plugins) help information, the [current status](https://prow.k8s.io/tide) and [history](https://prow.k8s.io/tide-history) of merge automation, and a [dashboard for PR authors](https://prow.k8s.io/pr).
* [`hook`](/docs/components/core/hook/) is the most important piece. It is a stateless server that listens for GitHub webhooks and dispatches them to the appropriate plugins. Hook's plugins are used to trigger jobs, implement 'slash' commands, post to Slack, and more. See the [`prow/plugins`](/docs/plugins/) directory for more information on plugins.
* [`horologium`](/docs/components/core/horologium/) triggers periodic jobs when necessary.
* [`prow-controller-manager`](/docs/components/core/prow-controller-manager/) manages the job execution and lifecycle for jobs that run in k8s pods. It currently acts as a replacement for [`plank`](/docs/components/deprecated/plank/)
* [`sinker`](/docs/components/core/sinker/) cleans up old jobs and pods.

#### Merge Automation

* [`tide`](/docs/components/core/tide/) manages retesting and merging PRs once they meet the configured merge criteria. See [its README](/docs/components/core/tide/) for more information.

#### Optional Components

* [`branchprotector`](/docs/components/optional/branchprotector/) configures [github branch protection](https://help.github.com/articles/about-protected-branches/) according to a specified policy
* [`exporter`](/docs/components/optional/exporter/) exposes metrics about ProwJobs not directly related to a specific Prow component
* [`gcsupload`](/docs/components/optional/gcsupload/)
* [`gerrit`](/docs/components/optional/gerrit/) is a Prow-gerrit adapter for handling CI on [gerrit](https://www.gerritcodereview.com/) workflows
* [`hmac`](/docs/components/optional/hmac/) updates HMAC tokens, GitHub webhooks and HMAC secrets for the orgs/repos specified in the Prow config file
* [`jenkins-operator`](/docs/components/optional/jenkins-operator/) is the controller that manages jobs that run on Jenkins. We moved away from using this component in favor of running all jobs on Kubernetes.
* [`tot`](/docs/components/optional/tot/) vends sequential build numbers. Tot is only necessary for integration with automation that expects sequential build numbers. If Tot is not used, Prow automatically generates build numbers that are monotonically increasing, but not sequential.
* [`status-reconciler`](/docs/components/optional/status-reconciler/) ensures changes to blocking presubmits in Prow configuration does not cause in-flight GitHub PRs to get stuck
* [`sub`](/docs/components/optional/sub/) listen to Cloud Pub/Sub notification to trigger Prow Jobs.

## CLI Tools

* [`checkconfig`](/docs/components/cli-tools/checkconfig/) loads and verifies the configuration, useful as a pre-submit.
* [`config-bootstrapper`](/docs/components/cli-tools/config-bootstrapper/) bootstraps a configuration that would be incrementally updated by the [`updateconfig` Prow plugin](/docs/components/plugins/updateconfig/)
* [`generic-autobumper`](/docs/components/cli-tools/generic-autobumper/) automates image version upgrades (e.g. for a Prow deployment) by opening a PR with images changed to their latest version according to a config file.
* [`invitations-accepter`](/docs/components/cli-tools/invitations-accepter/) approves all pending GitHub repository invitations
* [`mkpj`](/docs/components/cli-tools/mkpj/) creates `ProwJobs` using Prow configuration.
* [`mkpod`](/docs/components/cli-tools/mkpod/) creates `Pods` from `ProwJobs`.
* [`peribolos`](/docs/components/cli-tools/peribolos/) manages GitHub org, team and membership settings according to a config file. Used by [kubernetes/org](https://github.com/kubernetes/org)
* [`phaino`](/docs/components/cli-tools/phaino/) runs an approximation of a ProwJob on your local workstation
* [`phony`](/docs/components/cli-tools/phony/) sends fake webhooks for testing hook and plugins.

## Pod Utilities

These are small tools that are automatically added to ProwJob pods for jobs that request pod decoration. They are used to transparently provide source code cloning and upload of metadata, logs, and job artifacts to persistent storage. See [their README](/docs/components/pod-utilities/) for more information.

* [`clonerefs`](/docs/components/pod-utilities/clonerefs/)
* [`initupload`](/docs/components/pod-utilities/initupload/)
* [`entrypoint`](/docs/components/pod-utilities/entrypoint/)
* [`sidecar`](/docs/components/pod-utilities/sidecar/)

## Base Images

The container images in [`images`](https://github.com/kubernetes/test-infra/tree/master/images) are used as base images for Prow components.

## TODO: undocumented

* [`admission`](/docs/components/undocumented/admission/)
* [`grandmatriarch`](/docs/components/undocumented/grandmatriarch/)
* [`pipeline`](/docs/components/undocumented/pipeline/)
* [`tackle`](/docs/components/undocumented/tackle/)

## Deprecated

* [`cm2kc`](/docs/components/deprecated/cm2kc/) is a CLI tool used to convert a [clustermap file](/docs/getting-started-deploy/#run-test-pods-in-different-clusters) to a [kubeconfig file](https://kubernetes.io/docs/tasks/access-application-cluster/configure-access-multiple-clusters/). Deprecated because we have moved away from clustermaps; you should use [`gencred`](https://github.com/kubernetes/test-infra/tree/master/gencred) to generate a [kubeconfig file](https://kubernetes.io/docs/tasks/access-application-cluster/configure-access-multiple-clusters/) directly.
