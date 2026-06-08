---
title: "Hook"
weight: 30
description: >
  Receives GitHub webhooks and dispatches them to plugins.
---

Hook is the Prow component that listens for [GitHub webhooks](https://docs.github.com/en/developers/webhooks-and-events/webhooks/about-webhooks) and dispatches them
to the appropriate [plugins](/docs/components/plugins/). It validates incoming webhooks using HMAC credentials and routes events to both internal and [external plugins](/docs/components/external-plugins/) based on configuration.

## How Hook Works

Hook receives webhook events from GitHub and dispatches them to plugins:

1. **Receives webhook**: GitHub sends events to Hook's `/hook` endpoint
2. **Validates HMAC**: Verifies the webhook signature matches the configured secret
3. **Parses event**: Unmarshals the JSON payload based on event type
4. **Checks repository**: Verifies the repository is enabled for processing
5. **Dispatches to internal plugins**: Routes events to registered plugin handlers
6. **Forwards to external plugins**: Sends events to external plugin services if configured

## Supported Event Types

Hook handles the following GitHub webhook events with internal plugins:

- `issues` - Issue opened, closed, edited, etc.
- `issue_comment` - Comments on issues and pull requests
- `pull_request` - Pull request opened, closed, synchronized, etc.
- `pull_request_review` - Pull request reviews submitted
- `pull_request_review_comment` - Comments on pull request diffs
- `push` - Commits pushed to a repository
- `status` - Commit status changes (e.g., CI/CD check results)

All event types (including the above) can be forwarded to external plugins if configured.

## Configuration

### GitHub Webhook Setup

Configure GitHub to send webhooks to Hook:

1. In your GitHub repository or organization settings, go to **Settings** → **Webhooks**
2. Click **Add webhook**
3. Set **Payload URL** to `https://your-prow-instance.com/hook`
4. Set **Content type** to `application/json`
5. Set **Secret** to match your HMAC secret
6. Select which events to send:
   - Choose **Let me select individual events** and select the events your plugins need
   - Or choose **Send me everything** to receive all event types
7. Ensure webhook is **Active**

**Note:** Hook will only process events that GitHub is configured to send. If plugins aren't responding to certain events, verify those events are enabled in the webhook configuration.

### HMAC Secret

The HMAC secret validates webhooks are from GitHub. Store it as a
secret and mount it to Hook:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: hmac-token
type: Opaque
stringData:
  hmac: <your-secret-here>
```

Mount the secret in Hook's deployment:

```yaml
spec:
  containers:
  - name: hook
    args:
    - --hmac-secret-file=/etc/webhook/hmac
    volumeMounts:
    - name: hmac
      mountPath: /etc/webhook
      readOnly: true
  volumes:
  - name: hmac
    secret:
      secretName: hmac-token
```

### Plugin Configuration

Plugins are configured in `plugins.yaml`. Enable plugins per repository or organization:

```yaml
plugins:
  org/repo:
    plugins:
    - assign
    - lgtm
    - approve
  org:
    plugins:
    - size
    - welcome
```

You can also exclude specific repositories from organization-level plugins:

```yaml
plugins:
  org:
    plugins:
    - size
    - welcome
    excluded_repos:
    - repo-to-exclude
```

### External Plugins

External plugins are separate HTTP services that receive webhook events:

```yaml
external_plugins:
  org/repo:
    - name: my-plugin
      endpoint: http://my-plugin-service:8080
      events:
        - pull_request
        - issue_comment
```

## CLI Flags

Common flags for Hook:

- `--config-path`: Path to [Prow config file](/docs/config/)
- `--plugin-config`: Path to plugin config (default: `/etc/plugins/plugins.yaml`)
- `--hmac-secret-file`: Path to HMAC secret file (default: `/etc/webhook/hmac`)
- `--webhook-path`: Path for webhook events (default: `/hook`)
- `--dry-run`: Dry run mode for testing (default: `true`)
- `--port`: Port to listen on (default: `8888`)
- `--grace-period`: Duration to handle events on shutdown (default: `180s`)
- `--slack-token-file`: Path to Slack token file (optional)

Production deployments must set `--dry-run=false`.

## GitHub API Access

Hook uses the GitHub API to interact with repositories on behalf of plugins — for example, adding labels, posting comments, or updating commit statuses. It requires GitHub authentication credentials and should be configured with ghproxy to manage rate limits. See [Managing GitHub API Access](/docs/github-api-access/) for details on authentication methods, endpoint configuration, and rate limit management.

## Endpoints

Hook exposes these HTTP endpoints:

- `/hook` - Webhook receiver endpoint (configurable via `--webhook-path`)
- `/plugin-help` - Returns help information about enabled plugins
- `/` - Health check endpoint (returns 200 OK)

## Troubleshooting

### Webhooks Not Received

- Verify webhook configuration in GitHub
- Check HMAC secret matches between GitHub and Hook
- Review Hook logs for validation errors
- Ensure Hook endpoint is publicly accessible

### Events Not Processed

- Verify the plugin is enabled for the repository in `plugins.yaml`
- Check that the event type is enabled in the GitHub webhook configuration
- Review Hook logs for plugin execution errors
- Ensure required credentials (GitHub token/App credentials, etc.) are properly configured

## See Also

- [Plugins Documentation](/docs/components/plugins/)
