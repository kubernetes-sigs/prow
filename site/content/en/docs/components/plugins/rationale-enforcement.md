---
title: "Command rationale enforcement"
weight: 20
description: >
  Configure per-repository rationale requirements for /retest and /override.
---

Prow can require a free-text rationale when users run `/retest` or `/override`.
The policy is opt-in and configured per repository and command in `plugins.yaml`.
Repositories and commands without a policy keep their existing behavior. This
setting does not enable the `trigger` or `override` plugins; enable those
plugins separately if they are not already enabled.

## Configuration

Add a `require_rationale` entry for the exact `org/repo` and command names:

```yaml
require_rationale:
  openshift/some-repo:
    retest:
      min_length: 20
      emergency_bypass:
        allowed_github_teams:
          - openshift/ci-admins
    override:
      min_length: 20
      emergency_bypass:
        allowed_github_teams:
          - openshift/ci-admins
```

`min_length` is optional and defaults to 20. It must not be negative. The
`emergency_bypass` section is optional; without it, `Emergency: true` cannot be
used to bypass the length requirement. Each team must be written as
`organization/team-slug`.

This policy does not inherit from an organization-level key: list each
repository that should be protected. If your deployment uses supplemental
per-repository `_pluginconfig.yaml` files, the policy can be configured there
as well.

Only `retest` and `override` are supported policy keys. This policy does not
directly protect `/test <job>`, `/retest-required`, `/verify-owners`,
`/override-sticky`, `/override-cancel`, or `/verified`.

## Writing the rationale

Put a one-line `Reason:` on a separate line in the block following the protected
command. The text after the colon is the rationale:

```text
/retest
Reason: Retrying after the CI setup network timeout was resolved; failure is unrelated to PR changes.
```

For `/override`, keep the context arguments on the command line and the
rationale on its own line:

```text
/override "ci/prow/e2e-aws"
Reason: Overriding after the e2e infrastructure outage was confirmed.
```

The `Reason:` key is case-insensitive, but the colon is required. Prow trims
surrounding whitespace and counts non-whitespace Unicode code points. Spaces
inside the reason do not count toward the minimum.

In a comment with multiple commands, each protected command needs its own
rationale before the next slash-command line. Blank lines are allowed, and the
`Reason:` and `Emergency:` lines may appear in either order:

```text
/retest
Reason: Retrying after the CI setup network timeout was resolved; failure is unrelated to PR changes.

/override "ci/prow/e2e-aws"
Reason: Overriding after the e2e infrastructure outage was confirmed.
```

Use only one occurrence of each protected command in a comment; when a command
is repeated, rationale validation uses its first matching invocation.

## Emergency bypass

An authorized member of one of the configured teams can bypass the minimum
length by adding `Emergency: true` on a separate line. A non-empty `Reason:` is
still required:

```text
/retest
Emergency: true
Reason: CI outage; retrying affected jobs.
```

`Emergency:` and the value `true` are case-insensitive. `Emergency: false`
uses normal length validation; any other value is invalid and blocks the
command. If the user is not a member of an allowed team, the bypass is denied
even when the reason otherwise meets the minimum. A team authorized for the
bypass does not gain permission to run `/override`; Prow still applies the
existing `/override` authorization rules. The bypass also does not change the
existing trust checks for `/retest`.

An emergency bypass is useful when an authorized CI administrator needs to
record a short incident explanation. Merely writing “emergency override” as a
`Reason:` does not request a bypass and is too short for the default minimum.

Prow checks configured team membership through GitHub. A team-lookup or
rationale-validation error fails closed. Emergency bypasses are recorded in
structured logs with the actor, repository, pull request, command, reason, and
matched team; the reason is not used as a metric label.

## Rejection behavior

If rationale is missing, empty, too short, malformed, or cannot be validated,
Prow posts an error comment and blocks actions in that command's native
handler. `/retest` will not create ProwJobs; `/override` will not update
statuses or check runs. If the same comment contains other commands handled by
that same plugin, they are not processed after the rationale failure. Prow
dispatches other generic-comment plugins independently, so this policy does
not provide a global comment-level transaction across unrelated plugins.
