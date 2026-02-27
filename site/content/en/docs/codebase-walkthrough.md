---
title: "Codebase Walkthrough"
weight: 100
description: >
  Detailed explanation of the repository structure and key components
---

# Codebase Walkthrough

## Repository Structure

The Prow repository follows a standard Go project layout with clear separation of concerns:

```
prow/
├── cmd/                    # Command-line tools and applications
├── pkg/                    # Shared libraries and packages
├── config/                 # Configuration examples
├── site/                   # Documentation site
├── test/                   # Test files and test data
├── hack/                   # Build scripts and utilities
├── Makefile               # Build automation
├── go.mod                  # Go module definition
├── go.sum                  # Go module checksums
├── README.md              # Main README
├── CONTRIBUTING.md        # Contribution guidelines
└── OWNERS                 # Code owners file
```

## Directory Details

### `/cmd` - Command-Line Tools

This directory contains all executable Prow components. Each subdirectory represents a standalone service or tool.

#### Core Components

**Webhook and Event Handling:**
- `hook/` - Main webhook server that processes GitHub/Gerrit events
- `webhook-server/` - Webhook server with admission control
- `ghproxy/` - GitHub API proxy with caching
- `hmac/` - HMAC signature validation

**Job Management:**
- `prow-controller-manager/` - Main controller manager
- `plank/` - Creates and manages Pods for jobs
- `scheduler/` - Distributes jobs across clusters
- `sinker/` - Cleans up old ProwJobs and Pods
- `horologium/` - Triggers periodic jobs

**Status and Reporting:**
- `crier/` - Reports job status to GitHub/Gerrit/Slack
- `status-reconciler/` - Reconciles GitHub commit status
- `deck/` - Web UI for viewing jobs and results
- `exporter/` - Exports metrics

**Automation:**
- `tide/` - Automated PR merging
- `peribolos/` - GitHub organization management
- `branchprotector/` - Branch protection management

**Job Utilities:**
- `clonerefs/` - Clones git repositories
- `initupload/` - Initializes job artifacts
- `entrypoint/` - Job entrypoint wrapper
- `sidecar/` - Sidecar container for log upload
- `gcsupload/` - Uploads artifacts to GCS
- `tot/` - Test of tests (version management)

**Developer Tools:**
- `mkpj/` - Create ProwJob YAML
- `mkpod/` - Create Pod YAML
- `checkconfig/` - Validate Prow configuration
- `config-bootstrapper/` - Bootstrap Prow config

**External Integrations:**
- `gerrit/` - Gerrit integration
- `jenkins-operator/` - Jenkins operator
- `pipeline/` - Tekton pipeline controller
- `gangway/` - OAuth server
- `moonraker/` - Config server

**External Plugins:**
- `external-plugins/cherrypicker/` - Cherry-pick plugin
- `external-plugins/needs-rebase/` - Rebase checker
- `external-plugins/refresh/` - Refresh plugin

#### Component Structure Pattern

Each component typically follows this structure:

```
cmd/component-name/
├── main.go              # Entry point
├── main_test.go         # Tests
├── OWNERS               # Code owners (if applicable)
└── ...                  # Component-specific files
```

### `/pkg` - Shared Packages

This directory contains reusable Go packages used across multiple components.

#### Key Packages

**Core API:**
- `pkg/apis/prowjobs/` - ProwJob Custom Resource definitions
  - `v1/types.go` - ProwJob types
  - `v1/register.go` - API registration

**Configuration:**
- `pkg/config/` - Configuration loading and management
  - `config.go` - Main config structures
  - `jobs.go` - Job configuration
  - `tide.go` - Tide configuration
  - `plugins.go` - Plugin configuration

**Core Components:**
- `pkg/hook/` - Webhook handling logic
- `pkg/plank/` - Pod creation and management
- `pkg/scheduler/` - Job scheduling logic
- `pkg/tide/` - PR merging logic
- `pkg/crier/` - Status reporting
- `pkg/sinker/` - Cleanup logic

**Plugin System:**
- `pkg/plugins/` - Plugin implementations
  - Individual plugin packages (approve, lgtm, etc.)
  - Plugin framework

**Git Integration:**
- `pkg/github/` - GitHub API client
- `pkg/gerrit/` - Gerrit integration
- `pkg/git/` - Git utilities
- `pkg/clonerefs/` - Repository cloning

**Job Utilities:**
- `pkg/pod-utils/` - Pod utility functions
- `pkg/entrypoint/` - Entrypoint logic
- `pkg/initupload/` - Init upload logic
- `pkg/sidecar/` - Sidecar logic
- `pkg/gcsupload/` - GCS upload logic

**UI Components:**
- `pkg/deck/` - Deck UI backend
- `pkg/spyglass/` - Spyglass artifact viewer

**Utilities:**
- `pkg/util/` - General utilities
- `pkg/pjutil/` - ProwJob utilities
- `pkg/kube/` - Kubernetes client utilities
- `pkg/flagutil/` - Flag utilities
- `pkg/metrics/` - Metrics collection
- `pkg/logrusutil/` - Logging utilities

**Specialized:**
- `pkg/repoowners/` - OWNERS file parsing
- `pkg/labels/` - Label management
- `pkg/markdown/` - Markdown processing
- `pkg/slack/` - Slack integration
- `pkg/jira/` - Jira integration
- `pkg/bugzilla/` - Bugzilla integration

### `/config` - Configuration Examples

Example Prow configurations:
- `prow/cluster/` - Cluster configurations
- Example job configs
- Plugin configs

### `/site` - Documentation Site

Hugo-based documentation site:
- Static site generation
- Documentation content
- API documentation

### `/test` - Test Files

- `test/integration/` - Integration test suites
- Test data files

### `/hack` - Build Scripts

Utility scripts for development and CI:
- `hack/make-rules/` - Makefile rules
- `hack/scripts/` - Utility scripts
- `hack/tools/` - Development tools

## Key Files and Modules

### Main Entry Points

**Hook (`cmd/hook/main.go`):**
- Main webhook server
- Processes GitHub/Gerrit events
- Executes plugins
- Creates ProwJobs

**Controller Manager (`cmd/prow-controller-manager/main.go`):**
- Runs plank and scheduler controllers
- Manages ProwJob lifecycle
- Coordinates job execution

**Plank (`pkg/plank/`):**
- Creates Pods from ProwJobs
- Manages Pod lifecycle
- Updates ProwJob status

**Tide (`cmd/tide/main.go`):**
- Monitors PRs for merge eligibility
- Automatically merges PRs
- Manages merge pools

### Core Components

**ProwJob API (`pkg/apis/prowjobs/v1/types.go`):**
```go
type ProwJob struct {
    Spec   ProwJobSpec
    Status ProwJobStatus
}

type ProwJobSpec struct {
    Type      ProwJobType  // presubmit, postsubmit, periodic, batch
    Job       string
    Refs      Refs
    PodSpec   *corev1.PodSpec
    // ...
}
```

**Configuration (`pkg/config/config.go`):**
- `Config` - Main configuration structure
- `JobConfig` - Job configurations
- `TideConfig` - Tide configuration
- `PluginConfig` - Plugin configuration

**Plugin Interface (`pkg/plugins/`):**
- Plugins implement specific interfaces
- Hook loads and executes plugins
- Plugins can create comments, labels, etc.

## Component Interactions

### How Hook Works

1. **Webhook Reception** (`cmd/hook/main.go`):
   - Receives webhook from GitHub/Gerrit
   - Validates HMAC signature
   - Parses event payload

2. **Plugin Execution** (`pkg/hook/server.go`):
   - Loads plugin configuration
   - Executes relevant plugins
   - Plugins can create ProwJobs

3. **ProwJob Creation** (`pkg/kube/`):
   - Creates ProwJob CRD
   - Controller picks up ProwJob

### How Controller Manager Works

1. **ProwJob Watching** (`pkg/plank/`):
   - Watches for new ProwJobs
   - Determines if Pod should be created

2. **Pod Creation** (`pkg/plank/`):
   - Creates Pod from ProwJob spec
   - Manages Pod lifecycle
   - Updates ProwJob status

3. **Scheduling** (`pkg/scheduler/`):
   - Selects cluster for job
   - Distributes load across clusters

### How Tide Works

1. **PR Monitoring** (`pkg/tide/`):
   - Queries GitHub for PRs
   - Checks merge eligibility

2. **Merge Eligibility** (`pkg/tide/`):
   - All tests pass
   - Required approvals present
   - No merge conflicts
   - Branch protection satisfied

3. **Merging** (`pkg/tide/`):
   - Merges PR when eligible
   - Updates status

### How Plugins Work

1. **Plugin Loading** (`pkg/hook/`):
   - Loads plugin configuration
   - Initializes plugins

2. **Plugin Execution** (`pkg/plugins/`):
   - Hook calls plugin handlers
   - Plugins process events
   - Plugins can modify state

3. **Plugin Actions**:
   - Create comments
   - Add/remove labels
   - Create ProwJobs
   - Update status

## Key Classes and Functions

### Hook Core

**Main Function** (`cmd/hook/main.go:main`):
- Entry point for hook
- Sets up HTTP server
- Registers webhook handler

**Server** (`pkg/hook/server.go`):
- `ServeHTTP()` - Handles webhook requests
- `handleWebhook()` - Processes webhook
- `handlePluginEvent()` - Executes plugins

### Controller Core

**Plank Controller** (`pkg/plank/`):
- `syncProwJob()` - Syncs ProwJob state
- `createPod()` - Creates Pod for job
- `updateProwJob()` - Updates ProwJob status

**Scheduler Controller** (`pkg/scheduler/`):
- `syncProwJob()` - Schedules job to cluster
- `selectCluster()` - Selects target cluster

### Tide Core

**Tide Controller** (`pkg/tide/`):
- `sync()` - Syncs PR state
- `isMergeable()` - Checks merge eligibility
- `mergePRs()` - Merges eligible PRs

### Plugin Framework

**Plugin Interface**:
```go
type PluginClient interface {
    // Plugin-specific methods
}
```

**Common Plugins**:
- `pkg/plugins/approve/` - Approval plugin
- `pkg/plugins/lgtm/` - LGTM plugin
- `pkg/plugins/trigger/` - Job trigger plugin

## API Surface

### ProwJob API

**ProwJob Resource**:
```go
type ProwJob struct {
    metav1.TypeMeta
    metav1.ObjectMeta
    Spec   ProwJobSpec
    Status ProwJobStatus
}
```

**Job Types**:
- `Presubmit` - Run on PRs
- `Postsubmit` - Run after merge
- `Periodic` - Run on schedule
- `Batch` - Run multiple jobs

### Configuration API

**Config Structure**:
```go
type Config struct {
    ProwConfig  ProwConfig
    JobConfig   JobConfig
    Tide        TideConfig
    Plank       PlankConfig
    // ...
}
```

## Data Flow

1. **Webhook Flow:**
   - GitHub sends webhook
   - Hook validates and processes
   - Plugins execute
   - ProwJob created

2. **Job Execution Flow:**
   - Controller reconciles ProwJob
   - Plank creates Pod
   - Pod executes job
   - Artifacts uploaded
   - Status updated

3. **Status Reporting Flow:**
   - Job completes
   - Crier reads status
   - Reports to GitHub/Slack
   - Updates commit status

## Testing Structure

- **Unit Tests**: `*_test.go` files alongside source
- **Integration Tests**: `test/integration/` directory
- **Test Data**: `testdata/` directories in packages

## Build System

- **Makefile**: Main build automation
- **Go Modules**: Dependency management
- **Ko**: Container image builds
- **CI/CD**: Automated testing and deployment

## Extension Points

1. **Custom Plugins**: Add to `pkg/plugins/`
2. **Custom Controllers**: Add to controller manager
3. **Custom Reporters**: Add to `pkg/crier/reporters/`
4. **Custom Job Types**: Extend ProwJob spec

