---
title: "Usage Guide"
weight: 88
description: >
  Practical examples and step-by-step instructions for using Prow
---

# Usage Guide

This guide provides practical examples and step-by-step instructions for using Prow.

## Table of Contents

- [Configuration](#configuration)
- [Running Components](#running-components)
- [Job Management](#job-management)
- [Plugin Usage](#plugin-usage)
- [Tide Configuration](#tide-configuration)
- [Deck UI](#deck-ui)

## Configuration

### Basic Configuration

Prow requires two main configuration files:

1. **Prow Config** (`config.yaml`): Main Prow configuration
2. **Plugin Config** (`plugins.yaml`): Plugin configuration

### Example Prow Config

```yaml
prowjob_namespace: default
pod_namespace: test-pods
periodics:
- interval: 24h
  name: periodic-job
  spec:
    containers:
    - image: alpine:latest
      command: ["echo", "Hello Prow"]
presubmits:
  myorg/myrepo:
  - name: test-job
    always_run: true
    spec:
      containers:
      - image: alpine:latest
        command: ["make", "test"]
postsubmits:
  myorg/myrepo:
  - name: deploy-job
    spec:
      containers:
      - image: alpine:latest
        command: ["make", "deploy"]
```

### Example Plugin Config

```yaml
plugins:
  myorg/myrepo:
  - approve
  - lgtm
  - trigger
  - welcome
```

## Running Components

### Hook Component

```bash
# Basic usage
hook \
  --config-path=config.yaml \
  --plugin-config=plugins.yaml \
  --dry-run

# Production usage
hook \
  --config-path=/etc/config/config.yaml \
  --plugin-config=/etc/plugins/plugins.yaml \
  --github-token-path=/etc/github/token \
  --hmac-secret=/etc/webhook/hmac
```

### Controller Manager

```bash
# Run with plank controller
prow-controller-manager \
  --config-path=config.yaml \
  --kubeconfig=~/.kube/config \
  --enable-controller=plank

# Run with multiple controllers
prow-controller-manager \
  --config-path=config.yaml \
  --enable-controller=plank \
  --enable-controller=scheduler
```

### Tide

```bash
# Run Tide
tide \
  --config-path=config.yaml \
  --github-token-path=/etc/github/token \
  --dry-run
```

### Deck

```bash
# Run Deck UI
deck \
  --config-path=config.yaml \
  --spyglass=true
```

## Job Management

### Creating Jobs

Jobs are defined in the Prow config file. See the [ProwJobs documentation](/docs/jobs/) for detailed job configuration.

### Triggering Jobs

**Via GitHub Comments:**
```
/test job-name
/retest
```

**Via Webhook:**
Jobs are automatically triggered when:
- A PR is opened/updated (presubmit jobs)
- Code is merged (postsubmit jobs)
- Scheduled time arrives (periodic jobs)

### Viewing Job Status

**Via Deck UI:**
Navigate to `http://localhost:8080` (or your Deck URL)

**Via kubectl:**
```bash
# List ProwJobs
kubectl get prowjobs

# Get specific ProwJob
kubectl get prowjob <job-name> -o yaml

# View job logs
kubectl logs <pod-name>
```

## Plugin Usage

### Available Plugins

Common plugins include:
- `approve` - Approval automation
- `lgtm` - Looks Good To Me automation
- `trigger` - Job triggering
- `welcome` - Welcome messages
- `label` - Label management
- `milestone` - Milestone management

See the [Plugins documentation](/docs/components/plugins/) for a complete list.

### Enabling Plugins

Add plugins to `plugins.yaml`:

```yaml
plugins:
  myorg/myrepo:
  - approve
  - lgtm
  - trigger
```

### Plugin Commands

**GitHub Comments:**
```
/approve
/lgtm
/test job-name
/retest
/close
```

## Tide Configuration

### Basic Tide Config

```yaml
tide:
  queries:
  - repos:
    - myorg/myrepo
    labels:
    - lgtm
    - approved
    missingLabels:
    - do-not-merge
```

### Merge Requirements

Tide merges PRs when:
- All required labels are present
- All required tests pass
- No blocking labels
- Branch protection satisfied

See the [Tide documentation](/docs/components/core/tide/) for detailed configuration.

## Deck UI

### Accessing Deck

Deck is typically available at:
- Local: `http://localhost:8080`
- Production: `https://prow.your-domain.com`

### Features

- **Job History**: View past job runs
- **Job Status**: Real-time job status
- **Logs**: View job logs
- **Spyglass**: View artifacts
- **Plugin Help**: View available plugin commands

### Navigation

- **Jobs**: Browse all jobs
- **PR History**: View PR-related jobs
- **Tide**: View merge queue status
- **Spyglass**: View artifacts

## Troubleshooting

### Jobs Not Running

1. Check ProwJob status:
   ```bash
   kubectl get prowjobs
   ```

2. Check controller logs:
   ```bash
   kubectl logs -l app=prow-controller-manager
   ```

3. Verify configuration:
   ```bash
   checkconfig --config-path=config.yaml
   ```

### Webhooks Not Working

1. Check hook logs:
   ```bash
   kubectl logs -l app=hook
   ```

2. Verify HMAC secret:
   ```bash
   # Check secret is configured
   kubectl get secret hmac-token
   ```

3. Test webhook delivery:
   - Check GitHub webhook delivery logs
   - Verify webhook URL is correct

### Configuration Errors

1. Validate config:
   ```bash
   checkconfig --config-path=config.yaml
   ```

2. Check for syntax errors:
   ```bash
   yamllint config.yaml
   ```

## Best Practices

1. **Configuration Management**
   - Use version control for configs
   - Test configs before deploying
   - Use `checkconfig` to validate

2. **Job Design**
   - Keep jobs focused and fast
   - Use appropriate job types
   - Set proper resource limits

3. **Plugin Usage**
   - Enable only needed plugins
   - Configure plugin permissions
   - Test plugins in dry-run mode

4. **Monitoring**
   - Monitor job success rates
   - Track resource usage
   - Set up alerts for failures

## Additional Resources

- [ProwJobs Documentation](/docs/jobs/)
- [Plugins Documentation](/docs/components/plugins/)
- [Tide Documentation](/docs/components/core/tide/)
- [Configuration Documentation](/docs/config/)

