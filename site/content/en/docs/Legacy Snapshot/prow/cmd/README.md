---
title: "prow/cmd/README.md"
---

# Prow Images

This directory includes a sub directory for every Prow component and is where all binary and container images are built. You can find the `main` packages here. For details about building the binaries and images see [`build_test_update.md`](https://github.com/kubernetes/test-infra/tree/master/prow/build_test_update.md).

## Cluster Components

Prow has a microservice architecture implemented as a collection of container images that run as Kubernetes deployments. A brief description of each service component is provided here.

#### Core Components

* [`crier`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/crier) reports on ProwJob status changes. Can be configured to report to gerrit, github, pubsub, slack, etc.
* [`deck`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/deck) presents a nice view of [recent jobs](https://prow.k8s.io/), [command](https://prow.k8s.io/command-help) and [plugin](https://prow.k8s.io/plugins) help information, the [current status](https://prow.k8s.io/tide) and [history](https://prow.k8s.io/tide-history) of merge automation, and a [dashboard for PR authors](https://prow.k8s.io/pr).
* [`hook`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/hook) is the most important piece. It is a stateless server that listens for GitHub webhooks and dispatches them to the appropriate plugins. Hook's plugins are used to trigger jobs, implement 'slash' commands, post to Slack, and more. See the [`prow/plugins`](https://github.com/kubernetes/test-infra/tree/master/prow/plugins/) directory for more information on plugins.
* [`horologium`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/horologium) triggers periodic jobs when necessary.
* [`prow-controller-manager`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/prow-controller-manager) manages the job execution and lifecycle for jobs that run in k8s pods. It currently acts as a replacement for [`plank`](https://github.com/kubernetes/test-infra/tree/master/prow/plank)
* [`sinker`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/sinker) cleans up old jobs and pods.

#### Merge Automation

* [`tide`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/tide) manages retesting and merging PRs once they meet the configured merge criteria. See [its README](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/tide/README.md) for more information.

#### Optional Components

* [`branchprotector`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/branchprotector) configures [github branch protection] according to a specified policy
* [`exporter`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/exporter) exposes metrics about ProwJobs not directly related to a specific Prow component
* [`gerrit`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/gerrit) is a Prow-gerrit adapter for handling CI on [gerrit] workflows
* [`hmac`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/hmac) updates HMAC tokens, GitHub webhooks and HMAC secrets for the orgs/repos specified in the Prow config file
* [`jenkins-operator`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/jenkins-operator) is the controller that manages jobs that run on Jenkins. We moved away from using this component in favor of running all jobs on Kubernetes.
* [`tot`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/tot) vends sequential build numbers. Tot is only necessary for integration with automation that expects sequential build numbers. If Tot is not used, Prow automatically generates build numbers that are monotonically increasing, but not sequential.
* [`status-reconciler`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/status-reconciler) ensures changes to blocking presubmits in Prow configuration does not cause in-flight GitHub PRs to get stuck
* [`sub`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/sub) listen to Cloud Pub/Sub notification to trigger Prow Jobs.

## CLI Tools

* [`checkconfig`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/checkconfig) loads and verifies the configuration, useful as a pre-submit.
* [`config-bootstrapper`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/config-bootstrapper) bootstraps a configuration that would be incrementally updated by the [`updateconfig` Prow plugin]
* [`generic-autobumper`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/generic-autobumper) automates image version upgrades (e.g. for a Prow deployment) by opening a PR with images changed to their latest version according to a config file.
* [`invitations-accepter`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/invitations-accepter) approves all pending GitHub repository invitations
* [`mkpj`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/mkpj) creates `ProwJobs` using Prow configuration.
* [`mkpod`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/mkpod) creates `Pods` from `ProwJobs`.
* [`peribolos`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/peribolos) manages GitHub org, team and membership settings according to a config file. Used by [kubernetes/org]
* [`phaino`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/phaino) runs an approximation of a ProwJob on your local workstation
* [`phony`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/phony) sends fake webhooks for testing hook and plugins.

## Pod Utilities

These are small tools that are automatically added to ProwJob pods for jobs that request pod decoration. They are used to transparently provide source code cloning and upload of metadata, logs, and job artifacts to persistent storage. See [their README](https://github.com/kubernetes/test-infra/tree/master/prow/pod-utilities.md) for more information.

* [`clonerefs`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/clonerefs)
* [`initupload`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/initupload)
* [`entrypoint`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/entrypoint)
* [`sidecar`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/sidecar)

## Base Images

The container images in [`images`](https://github.com/kubernetes/test-infra/tree/master/images) are used as base images for Prow components.

## TODO: undocumented

* [`admission`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/admission)
* [`gcsupload`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/gcsupload)
* [`grandmatriarch`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/grandmatriarch)
* [`pipeline`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/pipeline)
* [`tackle`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/tackle)

## Deprecated

* [`cm2kc`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/cm2kc) is a CLI tool used to convert a [clustermap file][clustermap docs] to a [kubeconfig file][kubeconfig docs]. Deprecated because we have moved away from clustermaps; you should use [`gencred`] to generate a [kubeconfig file] directly.

<!-- links -->

[github branch protection]: https://help.github.com/articles/about-protected-branches/
[clustermap docs]: https://github.com/kubernetes/test-infra/blob/1c7d9a4ae0f2ae1e0c11d8357f47163d18521b84/prow/getting_started_deploy.md#run-test-pods-in-different-clusters
[kubeconfig docs]: https://kubernetes.io/docs/tasks/access-application-cluster/configure-access-multiple-clusters/
[`gencred`]: https://github.com/kubernetes/test-infra/tree/master/gencred
[gerrit]: https://www.gerritcodereview.com/
[`updateconfig` Prow plugin]: https://github.com/kubernetes/test-infra/tree/master/prow/plugins/updateconfig
[kubernetes/org]: https://github.com/kubernetes/org
