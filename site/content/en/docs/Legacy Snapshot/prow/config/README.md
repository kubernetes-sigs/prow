---
title: "prow/config/README.md"
---

# Prow Configuration

Core Prow component configuration is managed by the `config` package and stored in the [`Config` struct](https://godoc.org/k8s.io/test-infra/prow/config#Config). If a configuration guide is available for a component it can be found in the [`/prow/cmd`](https://github.com/kubernetes/test-infra/tree/master/prow/cmd) directory. See [`jobs.md`](https://github.com/kubernetes/test-infra/tree/master/prow/jobs.md) for a guide to configuring ProwJobs.
Configuration for plugins is handled and stored separately. See the [`plugins`](https://github.com/kubernetes/test-infra/tree/master/prow/plugins) package for details.

You can find a sample config with all possible options and a documentation of them [here.](https://github.com/kubernetes/test-infra/tree/master/prow/config/prow-config-documented.yaml)
