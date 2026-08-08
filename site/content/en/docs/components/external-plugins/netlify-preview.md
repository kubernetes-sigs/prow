---
title: "netlify-preview"
weight: 20
description: >
  
---

netlify-preview is an external Prow plugin that retries the latest Netlify
deploy preview for a pull request in response to a chat command. It is intended
for repositories whose pull request previews are built by Netlify (for example
`kubernetes/website` and `kubernetes/contributor-site`).

## Commands

```
/retest
```

Retries the latest Netlify deploy preview for the PR **only when that preview
is in `error` state**. If the preview is `ready`, the plugin posts a comment
explaining that `/retest` will not retry passing previews and points the user
to `/rebuild-preview`.

```
/rebuild-preview
```

Forces a retry of the latest Netlify deploy preview regardless of its current
state, with one exception: if a build is already running (`building` or
`enqueued`), the plugin declines and reports the in-progress preview rather
than triggering a redundant build.

Both commands require the comment author to be trusted under the same rules
that Prow's `trigger` plugin applies: org members, configured trusted apps and
orgs, or PR authors who have received `/ok-to-test`.

## Configuration

The plugin reads a small YAML configuration file that maps each repository to
a Netlify site. Repositories that are not listed are ignored.

```yaml
repos:
  kubernetes/website:
    site_id: <netlify-site-id>
  kubernetes/contributor-site:
    site_id: <netlify-site-id>
```

Repositories that are listed under `external_plugins:` in the Prow
configuration but missing from this file will receive a comment explaining
that no Netlify preview site is configured.

## Required credentials

A Netlify personal access token (or a scoped equivalent) with permission to
list site deploys and call the deploy retry endpoint. The plugin reads it from
a file specified at startup. The token never reaches core Prow components.

## Webhook events

The plugin only subscribes to `issue_comment` events.
