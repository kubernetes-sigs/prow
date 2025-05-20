---
title: "config-bootstrapper"
weight: 10
description: >
  
---

`config-bootstrapper` is used to bootstrap a configuration that would be incrementally updated by the
config-updater Prow plugin.

When a set of configurations do not exist (for example, on a clean redeployment or in a disaster
recovery situation), the config-updater plugin is not useful as it can only upload incremental
updates. This tool is meant to be used in those situations to set up the config to the correct
base state and hand off ownership to the plugin for updates.

Provide the config-bootstrapper with the latest state of the Prow configuration (plugins.yaml, config.yaml, any job configuration files) to boot-strap with the latest configuration.

Sample usage:

```shell
./config-bootstrapper \
    --dry-run=false \
    --source-path=.  \
    --config-path=prowconfig/config.yaml \
    --plugin-config=prowconfig/plugins.yaml \
    --job-config-path=prowconfig/jobs
```

Periodics usage:

```shell
  - interval: 60m  # New periodic job
    agent: kubernetes
    name: config-bootstrapper-job  # Name for the new job
    decorate: true
    clone_uri: "https://github.com/your-org/infra.git"
    spec:
      containers:
        - image: gcr.io/k8s-prow/config-bootstrapper:latest
          name: config-bootstrapper
          command:
            - /app/config-bootstrapper
          args:
            - --dry-run=false
            - --config-path=/home/prow/go/src/github.com/kubestellar/kubestellar/prow/config.yaml
            - --plugin-config=/home/prow/go/src/github.com/kubestellar/kubestellar/prow/plugins.yaml
            - --label-config=/home/prow/go/src/github.com/kubestellar/kubestellar/prow/labels.yaml
            - --job-config-path=/home/prow/go/src/github.com/kubestellar/kubestellar/prow/jobs          
```
