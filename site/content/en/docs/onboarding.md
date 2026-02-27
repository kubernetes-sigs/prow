---
title: "Onboarding Guide for New Contributors"
weight: 95
description: >
  Learning path for new contributors, important concepts, and beginner roadmap
---

# Onboarding Guide for New Contributors

Welcome to Prow! This guide will help you understand the codebase and get started contributing effectively.

## What to Learn Before Contributing

### Essential Knowledge

1. **Go Programming Language**
   - Go basics (variables, functions, structs, interfaces)
   - Go concurrency (goroutines, channels)
   - Go testing (`testing` package)
   - Go modules and dependency management
   - **Resources**: [A Tour of Go](https://tour.golang.org/), [Effective Go](https://golang.org/doc/effective_go)

2. **Kubernetes**
   - Kubernetes API concepts
   - Custom Resources (CRDs)
   - Pods, Services, ConfigMaps, Secrets
   - Controllers and Operators
   - **Resources**: [Kubernetes Documentation](https://kubernetes.io/docs/)

3. **Git and GitHub**
   - Git workflow (branching, merging, rebasing)
   - GitHub API
   - Webhooks
   - **Resources**: [Git Documentation](https://git-scm.com/doc), [GitHub API](https://docs.github.com/en/rest)

4. **CI/CD Concepts**
   - Continuous Integration principles
   - Build pipelines
   - Test automation
   - **Resources**: [CI/CD Best Practices](https://www.redhat.com/en/topics/devops/what-is-ci-cd)

### Recommended Knowledge

1. **Kubernetes Controllers**
   - Controller pattern
   - Reconciliation loops
   - **Resources**: [Kubernetes Controller Pattern](https://kubernetes.io/docs/concepts/architecture/controller/)

2. **Webhooks**
   - Webhook concepts
   - HMAC signatures
   - Event handling
   - **Resources**: [GitHub Webhooks](https://docs.github.com/en/developers/webhooks-and-events/webhooks)

3. **REST APIs**
   - HTTP methods
   - JSON handling
   - Authentication
   - **Resources**: [REST API Tutorial](https://restfulapi.net/)

## Important Concepts the Repo Uses

### 1. ProwJob Custom Resource

Prow uses Kubernetes Custom Resources to represent CI jobs:

```go
// From pkg/apis/prowjobs/v1/types.go
type ProwJob struct {
    Spec   ProwJobSpec
    Status ProwJobStatus
}
```

**Key Concepts:**
- ProwJobs are Kubernetes resources
- Jobs execute as Pods
- Status tracks job execution

**Learn More:**
- Study `pkg/apis/prowjobs/v1/types.go`
- Read about Kubernetes CRDs

### 2. Controller Pattern

Prow components use the Kubernetes controller pattern:

```go
// Controllers watch resources and reconcile state
type Controller interface {
    Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error)
}
```

**Key Concepts:**
- Watch for resource changes
- Reconcile desired state
- Handle errors gracefully

**Learn More:**
- Study `pkg/plank/`
- Study `pkg/scheduler/`
- Read [Kubernetes Controller Pattern](https://kubernetes.io/docs/concepts/architecture/controller/)

### 3. Plugin System

Prow has an extensible plugin architecture:

```go
// Plugins implement specific interfaces
type PluginClient interface {
    // Plugin methods
}
```

**Key Concepts:**
- Plugins are Go packages
- Hook loads and executes plugins
- Plugins can interact with GitHub, Kubernetes, etc.

**Learn More:**
- Study `pkg/plugins/`
- Look at example plugins like `pkg/plugins/approve/`

### 4. Webhook Handling

Hook processes GitHub/Gerrit webhooks:

**Key Concepts:**
- Validates HMAC signatures
- Parses event payloads
- Executes plugins
- Creates ProwJobs

**Learn More:**
- Study `pkg/hook/server.go`
- Read about GitHub webhooks

### 5. Job Execution

Jobs execute as Kubernetes Pods:

**Key Concepts:**
- Plank creates Pods from ProwJobs
- Pods run job containers
- Artifacts uploaded to GCS
- Status reported back

**Learn More:**
- Study `pkg/plank/`
- Study `pkg/pod-utils/`

### 6. Configuration Management

Prow uses YAML configuration files:

**Key Concepts:**
- Config loaded from files
- Job definitions in config
- Plugin configuration
- Tide configuration

**Learn More:**
- Study `pkg/config/config.go`
- Look at example configs

## Beginner Roadmap for Mastering the Project

### Phase 1: Understanding the Basics (Week 1-2)

**Goal**: Understand what Prow does and how it's organized

**Tasks:**
1. Read [Overview](/docs/overview/) and [Architecture](/docs/overview/architecture/)
2. Set up development environment
3. Build and run a simple component locally
4. Read through `cmd/hook/main.go` to understand entry point
5. Study a simple component like `cmd/sinker/`

**Deliverable**: You can build and run components locally

### Phase 2: Understanding Hook (Week 3-4)

**Goal**: Understand how webhooks are processed

**Tasks:**
1. Read hook documentation
2. Study `pkg/hook/server.go` to understand webhook handling
3. Study `pkg/hook/events.go` to understand event types
4. Create a test webhook and process it locally
5. Trace through webhook processing flow

**Deliverable**: You understand how hook works

### Phase 3: Understanding Controllers (Week 5-6)

**Goal**: Understand how controllers manage ProwJobs

**Tasks:**
1. Study `pkg/plank/` to understand Pod creation
2. Study `pkg/scheduler/` to understand job scheduling
3. Study `pkg/sinker/` to understand cleanup
4. Run a controller locally and observe behavior
5. Make a small change to a controller

**Deliverable**: You can modify controllers

### Phase 4: Understanding Plugins (Week 7-8)

**Goal**: Understand the plugin system

**Tasks:**
1. Study `pkg/plugins/` to understand plugin framework
2. Study a simple plugin like `pkg/plugins/welcome/`
3. Study a complex plugin like `pkg/plugins/trigger/`
4. Create a simple custom plugin
5. Test plugin locally

**Deliverable**: You can create custom plugins

### Phase 5: Making Your First Contribution (Week 9-10)

**Goal**: Make your first meaningful contribution

**Tasks:**
1. Find a good first issue (labeled "good first issue")
2. Understand the problem and proposed solution
3. Implement the fix or feature
4. Write tests
5. Create a Pull Request
6. Address review feedback

**Deliverable**: Your first merged PR!

## Learning Resources by Topic

### Go

- **Books**: "The Go Programming Language" by Donovan & Kernighan
- **Online**: [Go by Example](https://gobyexample.com/)
- **Practice**: [Exercism Go Track](https://exercism.org/tracks/go)

### Kubernetes

- **Official Docs**: [Kubernetes Documentation](https://kubernetes.io/docs/)
- **Interactive**: [Kubernetes Playground](https://www.katacoda.com/courses/kubernetes)
- **Books**: "Kubernetes: Up and Running" by Hightower et al.

### CI/CD

- **Concepts**: [Red Hat CI/CD Guide](https://www.redhat.com/en/topics/devops/what-is-ci-cd)
- **Best Practices**: [ThoughtWorks CI/CD Guide](https://www.thoughtworks.com/continuous-integration)

## Common Patterns in the Codebase

### 1. Component Pattern

Most components follow this pattern:

```go
func main() {
    // Parse flags
    // Load configuration
    // Initialize clients
    // Start server/controller
    // Handle shutdown
}
```

### 2. Controller Reconciliation Pattern

```go
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Fetch resource
    // Check if deletion
    // Reconcile desired state
    // Update status
    // Return result
}
```

### 3. Plugin Pattern

```go
func HandleIssueComment(e github.IssueCommentEvent) error {
    // Parse comment
    // Execute action
    // Update state
    return nil
}
```

### 4. Error Handling Pattern

```go
// Wrap errors with context
if err != nil {
    return fmt.Errorf("operation failed: %w", err)
}

// Use errors.Is for error checking
if errors.Is(err, os.ErrNotExist) {
    // handle
}
```

## Tips for Success

1. **Start Small**: Begin with small, focused changes
2. **Read Code**: Spend time reading existing code before writing new code
3. **Ask Questions**: Don't hesitate to ask for help
4. **Write Tests**: Always write tests for new code
5. **Review PRs**: Review other PRs to learn patterns
6. **Be Patient**: Understanding a large codebase takes time

## Getting Help

- **GitHub Issues**: Search existing issues or create new ones
- **Pull Requests**: Ask questions in PR comments
- **Slack**: #sig-testing on Kubernetes Slack
- **Documentation**: Read the docs in this directory

## Next Steps

After completing the roadmap:

1. Find areas of interest
2. Look for issues labeled "good first issue"
3. Start contributing regularly
4. Consider becoming a maintainer (after significant contributions)

Welcome to the team! 🚀

