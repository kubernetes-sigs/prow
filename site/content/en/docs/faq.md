---
title: "FAQ"
weight: 120
description: >
  Frequently asked questions and answers
---

# FAQ Section

Frequently asked questions from newcomers to the Prow repository.

## General Questions

### What is Prow?

Prow is a Kubernetes-based Continuous Integration and Continuous Deployment (CI/CD) system. It provides automated testing, code review automation, and project management features for Kubernetes and other open-source projects.

### Who maintains Prow?

The Kubernetes SIG Testing team maintains Prow. See the [OWNERS](https://github.com/kubernetes-sigs/prow/blob/main/OWNERS) file for the list of maintainers.

### What programming language is Prow written in?

Primarily Go (94.7%), with some TypeScript (2.5%) for the Deck frontend.

### How do I get started?

1. Read the [Overview](/docs/overview/)
2. Set up your development environment
3. Try running some components
4. Explore the codebase ([Codebase Walkthrough](/docs/codebase-walkthrough/))

## Development Questions

### How do I build the project?

```bash
# Build all components
make build

# Install to $GOPATH/bin
go install ./cmd/...

# Build specific component
go build ./cmd/hook
```

### How do I run tests?

```bash
# Run all unit tests
make test

# Run specific package tests
go test ./pkg/hook/...

# Run integration tests
go test ./test/integration/...
```

### How do I contribute?

See the [Developing and Contributing to Prow](/docs/getting-started-develop/) guide for detailed instructions. In short:
1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test your changes
5. Create a Pull Request

## Component Questions

### What is hook?

Hook is the webhook server that processes GitHub/Gerrit events and executes plugins. See the [Hook documentation](/docs/components/core/hook/).

### What is plank?

Plank is the controller that creates and manages Pods for ProwJobs. Note: Plank is deprecated in favor of prow-controller-manager.

### What is Tide?

Tide automatically merges pull requests when all requirements are met. See the [Tide documentation](/docs/components/core/tide/).

### What is Deck?

Deck is the web UI for viewing job status, history, and results. See the [Deck documentation](/docs/components/core/deck/).

### What is Crier?

Crier reports job status back to GitHub, Gerrit, Slack, etc. See the [Crier documentation](/docs/components/core/crier/).

## Configuration Questions

### How do I configure Prow?

Prow requires two main config files:
- `config.yaml` - Main Prow configuration
- `plugins.yaml` - Plugin configuration

See the [Configuration documentation](/docs/config/) for details.

### How do I add a new job?

Add job definition to `config.yaml`:

```yaml
presubmits:
  myorg/myrepo:
  - name: my-job
    spec:
      containers:
      - image: alpine:latest
        command: ["echo", "test"]
```

### How do I enable a plugin?

Add plugin to `plugins.yaml`:

```yaml
plugins:
  myorg/myrepo:
  - approve
  - lgtm
```

## Job Questions

### What are the different job types?

- **Presubmit**: Run on pull requests
- **Postsubmit**: Run after code is merged
- **Periodic**: Run on a schedule (cron)
- **Batch**: Run multiple jobs in parallel

See the [ProwJobs documentation](/docs/jobs/) for more details.

### How do I trigger a job manually?

Use `/test <job-name>` comment on a PR.

### How do I view job logs?

```bash
# Get Pod name
kubectl get pods

# View logs
kubectl logs <pod-name>
```

## Plugin Questions

### How do plugins work?

Plugins are Go packages that hook processes and executes based on events. See the [Plugins documentation](/docs/components/plugins/).

### How do I create a custom plugin?

Create a new package in `pkg/plugins/` and implement the plugin interface. See the [Developing and Contributing to Prow](/docs/getting-started-develop/) guide.

### What plugins are available?

See `pkg/plugins/` directory for available plugins, or check the [Plugins documentation](/docs/components/plugins/).

## Troubleshooting Questions

### Jobs are not running

1. Check ProwJob status: `kubectl get prowjobs`
2. Check Pod status: `kubectl get pods`
3. Review controller logs
4. Verify configuration

### Webhooks are not working

1. Check hook logs
2. Verify HMAC secret
3. Check GitHub webhook configuration
4. Test webhook delivery

### Tide is not merging

1. Check Tide logs
2. Verify PR eligibility
3. Check required labels
4. Review branch protection

### Deck is not loading

1. Check Deck logs
2. Verify config path
3. Check network connectivity
4. Review browser console

## Architecture Questions

### How does Prow work?

1. GitHub sends webhook
2. Hook processes webhook
3. Plugins execute
4. ProwJob created
5. Controller reconciles ProwJob
6. Plank creates Pod
7. Pod executes job
8. Status reported back

See the [Architecture documentation](/docs/overview/architecture/) for more details.

### How are jobs executed?

Jobs execute as Kubernetes Pods. The controller manager creates Pods from ProwJob specs. See the [Life of a Prow Job](/docs/life-of-a-prow-job/) for details.

### How does Tide work?

Tide monitors PRs and automatically merges them when all requirements are met. See the [Tide documentation](/docs/components/core/tide/) for details.

## Contribution Questions

### How do I find good first issues?

Look for issues labeled:
- `good first issue`
- `help wanted`
- `beginner friendly`

### What makes a good PR?

- Clear description
- Focused changes
- Tests included
- Documentation updated
- All checks passing

### How long does review take?

Typically 1-3 business days, depending on:
- PR size and complexity
- Reviewer availability
- Number of review rounds needed

## Getting Help

### Where can I ask questions?

- **GitHub Issues**: For bugs and feature requests
- **Pull Requests**: For code-related questions
- **Slack**: #sig-testing on Kubernetes Slack
- **Documentation**: Check the docs in this directory

### How do I report a bug?

Create a GitHub issue with:
- Clear description
- Steps to reproduce
- Expected vs actual behavior
- Relevant logs/configs
- Environment information

### How do I request a feature?

Create a GitHub issue with:
- Problem description
- Proposed solution
- Use cases
- Any alternatives considered

## Still Have Questions?

- Check the [documentation index](/docs/)
- Search existing GitHub issues
- Ask in #sig-testing on Kubernetes Slack
- Create a new issue with your question

