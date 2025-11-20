---
title: "Summaries"
weight: 110
description: >
  Summaries at different technical levels (non-technical, intermediate, advanced)
---

# Summaries

This document provides summaries at different technical levels for different audiences.

## Non-Technical Summary

**What is Prow?**

Prow is a software system that helps developers automatically test their code and manage software projects. Think of it as an automated assistant for software development teams.

**What does it do?**

When developers write code and submit it for review, Prow automatically:
- Runs tests to make sure the code works correctly
- Checks if the code follows project rules
- Helps manage the review process
- Automatically merges code when it's ready

**Why is it important?**

Large software projects like Kubernetes have thousands of developers submitting code every day. Without Prow, managing all the tests and reviews would be extremely time-consuming and error-prone. Prow automates these tasks, making the development process faster, more reliable, and consistent.

**Who uses it?**

Primarily the Kubernetes project and other large open-source projects. It runs automatically in the background, handling thousands of code reviews and tests every day.

**Key Benefits:**
- Saves time by automating repetitive tasks
- Reduces errors through consistent processes
- Enables faster software releases
- Provides visibility into code quality and test status

## Intermediate Summary

**What is Prow?**

Prow is a Kubernetes-based Continuous Integration (CI) and Continuous Deployment (CD) system. It provides automated testing, code review automation, and project management features for large-scale software projects.

**Core Components:**

1. **Hook**: Webhook server that processes GitHub/Gerrit events and executes plugins based on those events. It validates webhook signatures and creates ProwJobs for CI tasks.

2. **Controller Manager**: Runs controllers (plank, scheduler) that manage ProwJobs. Plank creates Kubernetes Pods from ProwJobs, while scheduler distributes jobs across multiple clusters.

3. **Tide**: Automated PR merging system that monitors pull requests and merges them when all requirements are met (tests pass, approvals received, etc.).

4. **Deck**: Web UI that provides visibility into job status, history, and results. It integrates with Spyglass for viewing artifacts.

5. **Crier**: Status reporting component that reports job results back to GitHub, Gerrit, Slack, and other systems.

6. **Plugins**: Extensible plugin system that automates various tasks like labeling PRs, managing approvals, triggering tests, etc.

**How it Works:**

1. Developer opens a pull request on GitHub
2. GitHub sends webhook to Prow's hook component
3. Hook validates webhook and executes relevant plugins
4. Plugins create ProwJobs for CI tasks
5. Controller manager reconciles ProwJobs
6. Plank creates Kubernetes Pods to run jobs
7. Jobs execute and upload artifacts
8. Crier reports results back to GitHub
9. Tide merges PR if all requirements are met

**Key Features:**

- **Kubernetes Native**: Uses Kubernetes Custom Resources (ProwJobs) and Pods
- **Plugin Architecture**: Extensible plugin system for automation
- **Multi-Cluster**: Can distribute jobs across multiple Kubernetes clusters
- **Scalability**: Handles thousands of repositories and millions of test runs
- **Integration**: Deep integration with GitHub, Gerrit, Slack, etc.

**Technology Stack:**

- Go for backend components
- Kubernetes for orchestration
- TypeScript/React for Deck UI
- YAML for configuration

**Use Cases:**

- Automated testing on pull requests
- Code review automation
- Project management (labeling, milestones)
- Automated PR merging
- Status reporting and notifications

## Advanced Summary

**What is Prow?**

Prow is a comprehensive Kubernetes-native CI/CD platform implementing a declarative, event-driven architecture for automated testing, code review, and project management at scale. It uses Kubernetes Custom Resources (ProwJobs) to represent CI jobs and executes them as Pods.

**Architecture:**

The system follows a microservices architecture where independent components communicate through:
- **Kubernetes API**: ProwJobs, Pods, ConfigMaps, Secrets
- **GitHub/Gerrit API**: Webhooks, status updates, PR management
- **Event-Driven**: Responds to webhooks, cron schedules, and resource changes

**Core Design Patterns:**

1. **Kubernetes Controller Pattern**: Components like plank and scheduler implement the controller pattern:
   - Watch ProwJobs via informers
   - Reconcile desired state
   - Handle errors with exponential backoff
   - Update resource status

2. **Plugin Architecture**: Extensible plugin system:
   - Plugins are Go packages in `pkg/plugins/`
   - Hook loads plugins dynamically
   - Plugins implement specific interfaces
   - Plugins can create ProwJobs, modify GitHub state, etc.

3. **Custom Resources**: ProwJobs are Kubernetes CRDs:
   ```go
   type ProwJob struct {
       Spec   ProwJobSpec   // Job definition
       Status ProwJobStatus // Execution status
   }
   ```
   - Jobs execute as Pods
   - Status tracks execution state
   - Controllers manage lifecycle

4. **Webhook Processing**: Hook component:
   - Validates HMAC signatures
   - Parses event payloads
   - Executes plugins based on event type
   - Creates ProwJobs for CI tasks

5. **Multi-Cluster Scheduling**: Scheduler distributes jobs:
   - Selects cluster based on job requirements
   - Load balances across clusters
   - Handles cluster-specific configurations

**Key Technical Components:**

1. **Hook** (`cmd/hook/`, `pkg/hook/`):
   - HTTP server processing webhooks
   - Plugin execution engine
   - ProwJob creation logic
   - HMAC validation

2. **Controller Manager** (`cmd/prow-controller-manager/`):
   - Runs multiple controllers (plank, scheduler)
   - Uses controller-runtime framework
   - Manages ProwJob lifecycle

3. **Plank** (`pkg/plank/`):
   - Creates Pods from ProwJob specs
   - Manages Pod lifecycle
   - Updates ProwJob status
   - Handles job completion

4. **Scheduler** (`pkg/scheduler/`):
   - Selects target cluster for jobs
   - Distributes load across clusters
   - Handles cluster-specific configs

5. **Tide** (`cmd/tide/`, `pkg/tide/`):
   - Queries GitHub for PRs
   - Checks merge eligibility
   - Manages merge pools
   - Automatically merges PRs

6. **Plugin System** (`pkg/plugins/`):
   - Plugin framework
   - Individual plugin implementations
   - Plugin configuration management

**Advanced Features:**

1. **In-Repo Configuration**: Support for configuration in repository
2. **Gerrit Integration**: Full support for Gerrit code review
3. **Tekton Integration**: Support for Tekton pipelines
4. **Spyglass**: Unified artifact viewer
5. **Gangway**: OAuth server for Prow access

**Scalability Considerations:**

- **Horizontal Scaling**: Components scale horizontally
- **Multi-Cluster**: Jobs distributed across clusters
- **Caching**: ghproxy caches GitHub API responses
- **Resource Management**: Sinker cleans up old resources
- **Efficient API Usage**: Informers and watches for Kubernetes API

**Integration Points:**

- **GitHub API**: Webhooks, status updates, PR management
- **Kubernetes API**: Resource management, event watching
- **Gerrit API**: Code review integration
- **Slack API**: Notifications
- **GCS**: Artifact storage
- **Pub/Sub**: Event streaming

**Extension Mechanisms:**

1. **Custom Plugins**: Implement plugin interface in `pkg/plugins/`
2. **Custom Controllers**: Add to controller manager
3. **Custom Reporters**: Add to `pkg/crier/reporters/`
4. **Custom Job Types**: Extend ProwJob spec

**Performance Optimizations:**

- Informer-based resource watching
- Efficient GitHub API usage with caching
- Parallel job execution
- Resource cleanup via sinker
- Multi-cluster load distribution

**Security Model:**

- HMAC signature validation for webhooks
- RBAC for Kubernetes resource access
- OWNERS files for GitHub permissions
- Service account-based authentication
- Secret management via Kubernetes Secrets

**Monitoring and Observability:**

- Prometheus metrics for all components
- Structured logging with logrus
- OpenTelemetry tracing (where applicable)
- Deck UI for job visibility
- Custom dashboards for key metrics

This architecture enables Prow to handle the scale and complexity of large projects like Kubernetes while remaining maintainable and extensible.

