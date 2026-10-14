---
title: "Prow Configuration"
weight: 130
description: >
  
---

Core Prow component configuration is managed by the `config` package and stored in the [`Config` struct](https://godoc.org/sigs.k8s.io/prow/pkg/config#Config). If a configuration guide is available for a component it can be found in the ["Components"](/docs/components/) directory. See [`jobs.md`](/docs/jobs/) for a guide to configuring ProwJobs.
Configuration for plugins is handled and stored separately. See the [`plugins`](/docs/components/plugins/) package for details.

You can find a sample config with all possible options and a documentation of them [here](https://github.com/kubernetes-sigs/prow/blob/main/pkg/config/prow-config-documented.yaml).

### Supplemental Prow configuration

Components that load Prow configuration, including Tide and `checkconfig`, can
load shards with `--supplemental-prow-config-dir=config/shards`. The flag can be
repeated for multiple directories, and each directory is searched recursively.
Only filenames ending in `--supplemental-prow-configs-filename-suffix` are loaded;
the default suffix is `_prowconfig.yaml` (for example, `tide_prowconfig.yaml`).
The deprecated `--supplemental-prow-configs-filename` flag also sets this suffix;
prefer `--supplemental-prow-configs-filename-suffix` for new configurations.

Supplemental files support `branch-protection`, `slack_reporter_configs`,
`tide.merge_method`, `tide.queries`, and `tide.context_options`. They use the
same YAML structure as the main config, including the top-level `tide:` key.
Other Prow settings belong in the main config.

For `checkconfig`'s `validate-supplemental-prow-config-hierarchy` check, global
shards belong directly in the supplemental directory, org shards in `<org>/`,
and repo shards in `<org>/<repo>/`. The YAML must explicitly name its org and
repo; the path does not supply these keys. See [sharding Tide context
policies](/docs/components/core/tide/config/#sharding-context-policies) for
examples and merge behavior.
