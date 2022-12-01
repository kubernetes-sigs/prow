---
title: "Components"
weight: 30
---

## Prow Images

This directory includes a sub directory for every Prow component and is where all binary and container images are built. You can find the `main` packages here. For details about building the binaries and images see ["Building, Testing, and Updating Prow"](/docs/build-test-update/).

## Cluster Components

Prow has a microservice architecture implemented as a collection of container images that run as Kubernetes deployments. A brief description of each service component is provided here.

#### Core Components

* `crier` ([doc](/docs/components/core/crier/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/crier)) reports on ProwJob status changes. Can be configured to report to gerrit, github, pubsub, slack, etc.
* `deck` ([doc](/docs/components/core/deck/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/deck)) presents a nice view of [recent jobs](https://prow.k8s.io/), [command](https://prow.k8s.io/command-help) and [plugin](https://prow.k8s.io/plugins) help information, the [current status](https://prow.k8s.io/tide) and [history](https://prow.k8s.io/tide-history) of merge automation, and a [dashboard for PR authors](https://prow.k8s.io/pr).
* `hook` ([doc](/docs/components/core/hook/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/hook)) is the most important piece. It is a stateless server that listens for GitHub webhooks and dispatches them to the appropriate plugins. Hook's plugins are used to trigger jobs, implement 'slash' commands, post to Slack, and more. See the plugins [doc](/docs/components/plugins/) and [code directory](https://github.com/kubernetes/test-infra/tree/master/prow/plugins) for more information on plugins.
* `horologium` ([doc](/docs/components/core/horologium/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/horologium)) triggers periodic jobs when necessary.
* `prow-controller-manager` ([doc](/docs/components/core/prow-controller-manager/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/prow-controller-manager)) manages the job execution and lifecycle for jobs that run in k8s pods. It currently acts as a replacement for [`plank`](/docs/components/deprecated/plank/)
* `sinker` ([doc](/docs/components/core/sinker/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/sinker)) cleans up old jobs and pods.

#### Merge Automation

* `tide` ([doc](/docs/components/core/tide/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/tide)) manages retesting and merging PRs once they meet the configured merge criteria. See [its README](/docs/components/core/tide/) for more information.

#### Optional Components

* `branchprotector` ([doc](/docs/components/optional/branchprotector/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/branchprotector)) configures [github branch protection](https://help.github.com/articles/about-protected-branches/) according to a specified policy
* `exporter` ([doc](/docs/components/optional/exporter/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/exporter)) exposes metrics about ProwJobs not directly related to a specific Prow component
* `gcsupload` ([doc](/docs/components/optional/gcsupload/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/gcsupload))
* `gerrit` ([doc](/docs/components/optional/gerrit/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/gerrit)) is a Prow-gerrit adapter for handling CI on [gerrit](https://www.gerritcodereview.com/) workflows
* `hmac` ([doc](/docs/components/optional/hmac/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/hmac)) updates HMAC tokens, GitHub webhooks and HMAC secrets for the orgs/repos specified in the Prow config file
* `jenkins-operator` ([doc](/docs/components/optional/jenkins-operator/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/jenkins-operator)) is the controller that manages jobs that run on Jenkins. We moved away from using this component in favor of running all jobs on Kubernetes.
* `tot` ([doc](/docs/components/optional/tot/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/tot)) vends sequential build numbers. Tot is only necessary for integration with automation that expects sequential build numbers. If Tot is not used, Prow automatically generates build numbers that are monotonically increasing, but not sequential.
* `status-reconciler` ([doc](/docs/components/optional/status-reconciler/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/status-reconciler)) ensures changes to blocking presubmits in Prow configuration does not cause in-flight GitHub PRs to get stuck
* `sub` ([doc](/docs/components/optional/sub/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/sub)) listen to Cloud Pub/Sub notification to trigger Prow Jobs.

## CLI Tools

* `checkconfig` ([doc](/docs/components/cli-tools/checkconfig/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/checkconfig)) loads and verifies the configuration, useful as a pre-submit.
* `config-bootstrapper` ([doc](/docs/components/cli-tools/config-bootstrapper/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/config-bootstrapper)) bootstraps a configuration that would be incrementally updated by the [`updateconfig` Prow plugin](/docs/components/plugins/updateconfig/)
* `generic-autobumper` ([doc](/docs/components/cli-tools/generic-autobumper/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/generic-autobumper)) automates image version upgrades (e.g. for a Prow deployment) by opening a PR with images changed to their latest version according to a config file.
* `invitations-accepter` ([doc](/docs/components/cli-tools/invitations-accepter/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/invitations-accepter)) approves all pending GitHub repository invitations
* `mkpj` ([doc](/docs/components/cli-tools/mkpj/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/mkpj)) creates `ProwJobs` using Prow configuration.
* `mkpod` ([doc](/docs/components/cli-tools/mkpod/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/mkpod)) creates `Pods` from `ProwJobs`.
* `peribolos` ([doc](/docs/components/cli-tools/peribolos/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/peribolos)) manages GitHub org, team and membership settings according to a config file. Used by [kubernetes/org](https://github.com/kubernetes/org)
* `phaino` ([doc](/docs/components/cli-tools/phaino/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/phaino)) runs an approximation of a ProwJob on your local workstation
* `phony` ([doc](/docs/components/cli-tools/phony/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/phony)) sends fake webhooks for testing hook and plugins.

## Pod Utilities

These are small tools that are automatically added to ProwJob pods for jobs that request pod decoration. They are used to transparently provide source code cloning and upload of metadata, logs, and job artifacts to persistent storage. See [their README](/docs/components/pod-utilities/) for more information.

* `clonerefs` ([doc](/docs/components/pod-utilities/clonerefs/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/clonerefs))
* `initupload` ([doc](/docs/components/pod-utilities/initupload/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/initupload))
* `entrypoint` ([doc](/docs/components/pod-utilities/entrypoint/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/entrypoint))
* `sidecar` ([doc](/docs/components/pod-utilities/sidecar/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/sidecar))

## Base Images

The container images in [`images`](https://github.com/kubernetes/test-infra/tree/master/images) are used as base images for Prow components.

## TODO: undocumented

* `admission` ([doc](/docs/components/undocumented/admission/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/admission))
* `grandmatriarch` ([doc](/docs/components/undocumented/grandmatriarch/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/grandmatriarch))
* `pipeline` ([doc](/docs/components/undocumented/pipeline/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/pipeline))
* `tackle` ([doc](/docs/components/undocumented/tackle/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/tackle))

## Deprecated

* `cm2kc` ([doc](/docs/components/deprecated/cm2kc/), [code](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/cm2kc)) is a CLI tool used to convert a [clustermap file](/docs/getting-started-deploy/#run-test-pods-in-different-clusters) to a [kubeconfig file](https://kubernetes.io/docs/tasks/access-application-cluster/configure-access-multiple-clusters/). Deprecated because we have moved away from clustermaps; you should use [`gencred`](https://github.com/kubernetes/test-infra/tree/master/gencred) to generate a [kubeconfig file](https://kubernetes.io/docs/tasks/access-application-cluster/configure-access-multiple-clusters/) directly.
