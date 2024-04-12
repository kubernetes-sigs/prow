---
title: "Overview"
weight: 20
description: >
  A brief guide about Prow and its ecosystem
---

# ![Prow](/images/logo_horizontal_solid.png)

Prow is a Kubernetes based CI/CD system. Jobs can be triggered by various types of events and report their status to many different services. In addition to job execution, Prow provides GitHub automation in the form of policy enforcement, chat-ops via `/foo` style commands, and automatic PR merging.

See the [GoDoc](https://pkg.go.dev/sigs.k8s.io/prow/pkg) for library docs.
Please note that these libraries are intended for use by prow only, and we do
not make any attempt to preserve backwards compatibility.

For a brief overview of how Prow runs jobs take a look at ["Life of a Prow Job"](/docs/life-of-a-prow-job/).

To see common Prow usage and interactions flow, see the pull request interactions [sequence diagram](/images/pr-interactions-sequence.svg).

#### Functions and Features

* Job execution for testing, batch processing, artifact publishing.
  * GitHub events are used to trigger post-PR-merge (postsubmit) jobs and on-PR-update (presubmit) jobs.
  * Support for multiple execution platforms and source code review sites.
* Pluggable GitHub bot automation that implements `/foo` style commands and enforces configured policies/processes.
* GitHub merge automation with batch testing logic.
* Front end for viewing jobs, merge queue status, dynamically generated help information, and more.
* Automatic deployment of source control based config.
* Automatic GitHub org/repo administration configured in source control.
* Designed for multi-org scale with dozens of repositories. (The Kubernetes Prow instance uses only 1 GitHub bot token!)
* High availability as benefit of running on Kubernetes. (replication, load balancing, rolling updates...)
* JSON structured logs.
* Prometheus metrics.

## Documentation

### Getting started

* With your own Prow deployment: ["Deploying Prow"](/docs/getting-started-deploy/)
* With developing for Prow: ["Developing and Contributing to Prow"](/docs/getting-started-develop/)
* As a job author: [ProwJobs](/docs/jobs/)

### More details

* [Components](/docs/components/)

* [Plugins](/docs/components/plugins/)
* [ProwJobs](/docs/jobs/)
* [Building, Testing, and Updating](/docs/build-test-update/)
* [General Configuration](/docs/config/)
* [Pod Utilities](/docs/components/pod-utilities/)
* [Scaling Prow](/docs/scaling/)
* [Tide](/docs/components/core/tide/)
* [Metrics](/docs/metrics/)
* ["Life of a Prow Job"](/docs/life-of-a-prow-job/)
* [Getting more out of Prow](/docs/more-prow/)

### Tests

The stability of prow is heavily relying on unit tests and integration tests.

* Unit tests are co-located with prow source code
* [Integration tests](/docs/test/integration/) utilizes [kind](https://kind.sigs.k8s.io/) with hermetic integration tests. See [instructions for adding new integration tests](/docs/test/integration/#adding-new-integration-tests) for more details

## Useful Talks

### KubeCon 2020 EU virtual

* [Going Beyond CI/CD with Prow](https://youtu.be/qQvoImxHydk)

### KubeCon 2018 EU

* [Automation and the Kubernetes Contributor Experience](https://www.youtube.com/watch?v=BsIC7gPkH5M)
* [SIG Testing Deep Dive](https://www.youtube.com/watch?v=M32NIHRKaOI)

### KubeCon 2018 China

* [SIG Testing Intro](https://youtu.be/WFvC_VdkDFk)

### KubeCon 2018 Seattle

* [Behind you PR: K8s with K8s on K8s](https://www.youtube.com/watch?v=pz0lpl6h-Gc)
* [Using Prow for Testing Outside of K8s](https://www.youtube.com/watch?v=DBrkSC6nS8A)
* [Jenkins X (featuring Tide)](https://www.youtube.com/watch?v=IDEa8seAzVc)
* [SIG Testing Intro](https://www.youtube.com/watch?v=7-_O41W3FRU)
* [SIG Testing Deep Dive](https://www.youtube.com/watch?v=1rwiKDTJILY)

### Misc

* [Deploy Your Own Kubernetes Prow](https://www.youtube.com/watch?v=eMNwB96A1Qc)

## Prow in the wild

Prow is used by the following organizations and projects:

* [Kubernetes](https://prow.k8s.io)
  * This includes [kubernetes](https://github.com/kubernetes), [kubernetes-client](https://github.com/kubernetes-client), [kubernetes-csi](https://github.com/kubernetes-csi), and [kubernetes-sigs](https://github.com/kubernetes-sigs).
* [OpenShift](https://prow.ci.openshift.org/)
  * This includes [openshift](https://github.com/openshift), [openshift-s2i](https://github.com/openshift-s2i), [operator-framework](https://github.com/operator-framework), and some repos in [containers](https://github.com/containers) and [heketi](https://github.com/heketi).
* [Istio](https://prow.istio.io/)
* [Knative](https://prow.knative.dev/)
* [Jetstack](https://prow.build-infra.jetstack.net/)
* [Kyma](https://status.build.kyma-project.io/)
* [Metal³](https://prow.apps.test.metal3.io/)
* [Prometheus](http://prombench.prometheus.io/)
* [Caicloud](https://github.com/caicloud)
* [Kubeflow](https://github.com/kubeflow)
* [Azure AKS Engine](https://github.com/Azure/aks-engine/tree/master/.prowci)
* [tensorflow/minigo](https://github.com/tensorflow/minigo#automated-tests)
* [Daisy (Google Compute Image Tools)](https://github.com/GoogleCloudPlatform/compute-image-tools/tree/master/test-infra#prow-and-gubenator)
* [KubeEdge (Kubernetes Native Edge Computing Framework)](https://github.com/kubeedge/kubeedge)
* [Volcano (Kubernetes Native Batch System)](https://github.com/volcano-sh/volcano)
* [Loodse](https://public-prow.loodse.com/)
* [Feast](https://github.com/gojek/feast)
* [Falco](http://prow.falco.org)
* [TiDB](https://prow.tidb.net)
* [Amazon EKS Distro and Amazon EKS Anywhere](https://prow.eks.amazonaws.com/)
* [KubeSphere](https://prow.kubesphere.io)
* [OpenYurt](https://github.com/openyurtio/openyurt)
* [KubeVirt](https://prow.ci.kubevirt.io/)
* [AWS Controllers for Kubernetes](https://prow.ack.aws.dev/)
* [Gardener](https://prow.gardener.cloud/)

[Jenkins X](https://jenkins-x.io/) uses [Prow as part of Serverless Jenkins](https://medium.com/@jdrawlings/serverless-jenkins-with-jenkins-x-9134cbfe6870).

## Contact us

If you need to contact the maintainers of Prow you have a few options:

1. Open an issue in the [kubernetes/test-infra](https://github.com/kubernetes/test-infra) repo.
1. Reach out to the `#prow` channel of the [Kubernetes Slack](https://github.com/kubernetes/community/tree/master/communication#social-media).
1. Contact one of the code owners in [prow/OWNERS](https://github.com/kubernetes-sigs/prow/blob/main/prow/OWNERS) or in a more specifically scoped OWNERS file.

### Bots home

[@k8s-ci-robot](https://github.com/k8s-ci-robot) lives here and is the face of the Kubernetes Prow instance. Here is a [command list](https://go.k8s.io/bot-commands) for interacting with @k8s-ci-robot and other Prow bots.
