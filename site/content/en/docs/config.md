---
title: "Prow Configuration"
weight: 130
description: >
  
---

Core Prow component configuration is managed by the `config` package and stored in the [`Config` struct](https://godoc.org/sigs.k8s.io/prow/pkg/config#Config). If a configuration guide is available for a component it can be found in the ["Components"](/docs/components/) directory. See [`jobs.md`](/docs/jobs/) for a guide to configuring ProwJobs.
Configuration for plugins is handled and stored separately. See the [`plugins`](/docs/components/plugins/) package for details.

You can find a sample config with all possible options and a documentation of them [here](https://github.com/kubernetes-sigs/prow/blob/main/prow/config/prow-config-documented.yaml).
