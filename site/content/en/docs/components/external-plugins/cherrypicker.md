---
title: "cherrypicker"
weight: 10
description: >
  
---

Cherrypicker is an external prow plugin that can also run as a standalone bot.
It automates cherry-picking merged PRs into different branches. Cherrypicks are
triggered from the PR body, issue comments, submitted review bodies, or labels in
GitHub PRs that need to be cherrypicked. Submitted approved, changes-requested,
and commented reviews trigger requests. On merge, the current bodies of all
submitted reviews, including dismissed reviews, are processed; pending reviews
are skipped. Review edits and dismissals do not trigger new requests. Removing
a command from a review before merge removes that request; a later empty review
does not cancel commands in an earlier review. Inline review comments are not
supported.

For PR bodies, issue comments, and review bodies:

```
/cherrypick release-1.10
```

The above comment will result in opening a new PR against the `release-1.10` branch
once the PR where the comment was made gets merged or is already merged.

After deploying the updated cherrypicker, its existing `pull_request`
subscription enables review replay when a PR merges. To also acknowledge review
requests on open PRs and process them immediately on merged PRs, subscribe the
external plugin to `pull_request_review` in `plugins.yaml` alongside its
existing events. For example:

```yaml
external_plugins:
  org/repo:
  - name: cherrypick
    endpoint: http://cherrypick
    events:
    - issue_comment
    - pull_request
    - pull_request_review
```

To use label, you need to apply labels that contain the name of the branch in the form:

```
cherrypick/XXX
```

where XXX is the name of the branch.

The bot uses its own fork to push patches that need to be cherry-picked and opens
PRs out of those patches. The fork is created automatically by the bot so there is
no need to set it up manually.

Required scopes for the oauth token that need to be used are `read:org` and `repo`.
