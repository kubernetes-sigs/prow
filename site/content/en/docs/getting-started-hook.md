---
title: "Setting up a local Hook development environment"
weight: 90
description: >
  Step-by-step guide to run Hook locally with smee, a sandbox repo, and a GitHub App so you can develop and test plugins against real GitHub events.
---

Newcomers and future contributors, welcome!

This guide will walk you through setting up a local development environment for Hook and its plugins. You’ll run Hook on your machine, use a webhook proxy to receive webhooks from GitHub, and create a GitHub App to authenticate and interact with GitHub.

By the end, you’ll be able to trigger events from your GitHub account, like commenting `/lgtm` on an issue, and see your plugin respond in real time.

# Prerequisites

Before starting, make sure you have the following:

- A personal GitHub account
- Go installed
- The Prow repository cloned
- `npm` installed

## Set up webhook forwarding

In production, Hook is publicly accessible and receives GitHub webhooks directly. In this setup, for security and simplicity reasons, we keep Hook exposure private.  
We'll use [smee](https://smee.io/), a webhook delivery service, to forwards webhooks from `smee.io` to your machine.

1. Create a smee channel, and install the `smee-client` following the [GitHub Documentation](https://docs.github.com/en/webhooks/using-webhooks/handling-webhook-deliveries)
2. Note the URL. We'll call it `$WEBHOOK_PROXY_URL`

## Create a sandbox repository

Create a repo from where you’ll trigger events (PRs, comments, etc.).

1. [Create a repository](https://docs.github.com/en/repositories/creating-and-managing-repositories/creating-a-new-repository) named `prow-sandbox` in your GitHub account.
2. [Add a repository webhook](https://docs.github.com/en/webhooks/using-webhooks/creating-webhooks). Configure it with:

- Subscribe to all events
- Payload URL: `$WEBHOOK_PROXY_URL`
- Secret: `abcde12345`
- Content type: `application/json`

## Register and install a GitHub App

Hook uses a GitHub App to authenticate with GitHub and make API calls.

1. [Register the App](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app)

- App name: Any name will do. We suggest `prow-sandbox`
- Homepage URL: Any valid URL will do. We suggest `http://irrelevant`
- Permissions: Issues & Pull Requests (read and write)
- (Optional) For maximum immersion, set the image to [k8s-ci-robot's](https://avatars.githubusercontent.com/u/20407524?v=4)

2. [Install the App](https://docs.github.com/en/apps/using-github-apps/installing-your-own-github-app)

- Repository access: `Only selected repositories` > `prow-sandbox`
- Note the App ID. You'll need it later.

## Update local configuration files

The `config/local/hook` directory holds template config files to adapt for your setup.  
You can store sensitive credentials there. It is listed in `.gitignore`.

1. `config/local/hook/prow-sandbox.private-key.pem`

- Generate a private key for your GitHub App following [GitHub's guide](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/managing-private-keys-for-github-apps#generating-private-keys) and store it there

2. `config/local/hook/plugins.yaml`

- Replace `<GITHUB_USER>` with your GitHub username

## Run hook and smee

The configuration part is done. We're now going to

1. Form the root of the git repository, start Hook (replace `$GITHUB_APP_ID` with your saved App ID).

```sh
# Set dry-run flag to false so Hook makes mutating calls to the GitHub API
go run ./cmd/hook --dry-run=false                                  \
                  --config-path=config/local/hook/config.yaml      \
                  --plugin-config=config/local/hook/plugins.yaml   \
                  --hmac-secret-file=config/local/hook/hmac-secret \
                 --github-app-private-key-path=config/local/hook/prow-sandbox.private-key.pem
                 --github-app-id=$GITHUB_APP_ID
```

2. In a separate terminal, start the smee client to forward webhooks to the port Hook is listening on (default `8888`). Replace `$WEBHOOK_PROXY_URL` with your saved URL

```
smee --url $WEBHOOK_PROXY_URL --path /hook --port 8888
```

## Test the setup

Now the fun part. Let's make sure everything is wired up correctly by playing with the `dog` plugin. This plugin listen for `/woof` comment on issues and PRs, and responds with a dog photo

1. Create a new issue in `prow-sandbox`
2. Comment `/woof` on the issue
3. Hook, more precisely the `dog` plugin, should respond by posting a dog photo

## Understanding the workflow

1. Event occurs on GitHub (eg. `/woof` is commented on an Issue)
2. GitHub detects the event, and triggers a webhook to `$WEBHOOK_PROXY_URL`
3. The local `smee-client` receive the webhook and forwards it to the port Hook is listening on
4. Hook receives the webhook, parse the event, and dispatches it to the relevant plugins
5. Plugin reacts to the event (eg. by adding a comment the issue)

You now have a full local setup for Hook and can see plugins react to real GitHub events in real time.
From here, you’re free to experiment: tweak existing plugins, build your own, or tinker hook's configuration. Happy hacking!
