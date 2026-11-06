---
title: "Command rationale enforcement"
weight: 20
description: >
  Configure per-repository rationale requirements for /retest, /retest-required, and /override.
---

Prow can require a free-text rationale when users run `/retest`,
`/retest-required`, or `/override`. The policy is opt-in and configured per
repository and command in `plugins.yaml`. Repositories and commands without a
policy keep their existing behavior. This setting does not enable the `trigger`
or `override` plugins; enable those plugins separately if they are not already
enabled.

## Configuration

Add a `require_rationale` entry for the exact `org/repo` and command names:

```yaml
require_rationale:
  my-org/my-repo:
    retest:
      min_length: 20
    retest-required:
      min_length: 20
    override:
      min_length: 30
```

Each command is configured independently, so a repository can protect any
combination of them. `min_length` is optional and defaults to 20. It must not
be negative.

This policy does not inherit from an organization-level key: list each
repository that should be protected. If your deployment uses supplemental
per-repository `_pluginconfig.yaml` files, the policy can be configured there
as well.

Only `retest`, `retest-required`, and `override` are supported policy keys.
This policy does not protect `/test <job>`, `/test all`, `/verify-owners`,
`/override-sticky`, `/override-cancel`, or `/verified`.

## Writing the rationale

Put the rationale after the command on the same line:

```text
/retest Retrying after the CI setup network timeout was resolved.
```

For `/override`, the context names come first, so separate them from the
rationale with ` -- `:

```text
/override pull-e2e-aws -- Overriding after the e2e infrastructure outage was confirmed.
```

Alternatively, put the command on its own line and the rationale on the
following lines:

```text
/override pull-e2e-aws
Overriding after the e2e infrastructure outage was confirmed.
```

A rationale on the following lines runs until the next line that starts with
`/` or a code fence, and blank lines are ignored. If a command has a rationale
on the same line, the following lines are not used. A rationale must not start
with `/`, so another command cannot count as one. Prow counts non-whitespace
Unicode code points, so spaces do not count toward the minimum.

The same-line forms are only accepted in repositories with a policy for that
command. Elsewhere, `/retest <text>` still gets the existing reply that
`/retest` does not accept targets, and ` -- ` after `/override` is treated as
context names.

In a comment with multiple commands, each protected command needs its own
rationale. This includes repeated commands: if a comment contains `/override`
twice, both lines need a rationale, or neither override is applied.

A `/retest` inside a code block does not trigger tests on its own. However, if
the same comment contains another trigger command such as `/test <job>`, the
`trigger` plugin still sees the `/retest` line, so it needs a rationale too.

## Who must provide a rationale

Rationale is only checked for users who are already allowed to run the
command:

- For `/retest` and `/retest-required`, untrusted users on untrusted pull
  requests get the existing untrusted-user response.
- For `/override`, users without override permission get the existing
  unauthorized response.

Comments from Prow's own bot account never need a rationale. Other bots and
GitHub Apps are treated like any other user. Automation that comments
`/retest-required` followed by a status line, such as a retest controller,
satisfies the policy when that line meets the minimum length.

## Rejection behavior

If rationale is missing, too short, or cannot be validated, Prow posts an error
comment and blocks actions in that command's native handler. `/retest` and
`/retest-required` will not create ProwJobs; `/override` will not update
statuses or check runs. For `/override`, failing to fetch the pull request
also blocks the command.

The rest of that plugin's handling of the comment is skipped as well:

- `trigger`: `/ok-to-test` and `/test <job>` in the same comment are not
  processed.
- `override`: `/override-cancel` and `/override-sticky` in the same comment are
  not processed.

Other plugins handle the comment independently, so this policy does not block
unrelated commands such as `/lgtm` or `/hold` in the same comment.
