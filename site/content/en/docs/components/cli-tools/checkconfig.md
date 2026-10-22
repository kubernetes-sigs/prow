---
title: "checkconfig"
weight: 10
description: >
  
---

`checkconfig` loads the Prow configuration given with `--config-path`,
`--job-config-path` and `--plugin-config` in order to validate it. If the job
configuration is loaded from several paths, pass `--job-config-path` once for
each of them to validate them together.
Use `checkconfig` as a pre-submit for any repository holding Prow
configuration to ensure that check-ins do not break anything.
