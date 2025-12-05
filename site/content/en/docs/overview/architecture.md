---
title: "Architecture"
weight: 10
description: >
  Prow is made up of a collection of microservices (aka "Prow components") that work together in a **service cluster**, leveraging one or more **build clusters** to schedule Prow Jobs (or just "jobs").
---

## Prow in a Nutshell

Prow creates jobs based on various types of events, such as:

- GitHub events (e.g., a new PR is created, or is merged, or a person comments
"/retest" on a PR),

- Pub/Sub messages,

- time (these are created by **Horologium** and are called **periodic** jobs),
and

- retesting (triggered by **Tide**).

Jobs are created inside the Service Cluster as Kubernetes Custom Resources.  The
**Prow Controller Manager** takes triggered jobs and schedules them into a build
cluster, where they run as Kubernetes pods. **Crier** then reports the results
back to GitHub.

```mermaid
flowchart TD

    classDef yellow fill:#ff0
    classDef cyan fill:#0ff
    classDef pink fill:#f99

    subgraph Service Cluster["<span style='font-size: 40px;'><b>Service Cluster</b></span>"]
        Deck:::cyan
        Prowjobs:::yellow
        Crier:::cyan
        Tide:::cyan
        Horologium:::cyan
        Sinker:::cyan
        PCM[Prow Controller Manager]:::cyan
        Hook:::cyan
            subgraph Hook
                WebhookHandler["Webhook Handler"]
                PluginCat(["'cat' plugin"])
                PluginTrigger(["'trigger' plugin"])
            end
    end

    subgraph Build Cluster[<b>Build Cluster</b>]
        Pods[(Pods)]:::yellow
    end

    style Legend fill:#fff,stroke:#000,stroke-width:4px
    subgraph Legend["<span style='font-size: 20px;'><b>LEGEND</b></span>"]
        direction LR
        k8sResource[Kubernetes Resource]:::yellow
        prowComponent[Prow Component]:::cyan
        hookPlugin([Hook Plugin])
        Other
    end

    Prowjobs <-.-> Deck <-----> |Serve| prow.k8s.io
    GitHub ==> |Webhooks| WebhookHandler
    WebhookHandler --> |/meow| PluginCat
    WebhookHandler --> |/retest| PluginTrigger
    Prowjobs <-.-> Tide --> |Retest and Merge| GitHub
    Horologium ---> |Create| Prowjobs
    PluginCat --> |Comment| GitHub
    PluginTrigger --> |Create| Prowjobs
    Sinker ---> |Garbage collect| Prowjobs
    Sinker --> |Garbage collect| Pods
    PCM -.-> |List and update| Prowjobs
    PCM ---> |Report| Prowjobs
    PCM ==> |Create and Query| Pods
    Prowjobs <-.-> |Inform| Crier --> |Report| GitHub
```

## System Architecture

Prow is a Kubernetes-native CI/CD system that uses Kubernetes Custom Resources (ProwJobs) to manage CI/CD workflows. The architecture follows a microservices pattern where each component has a specific responsibility.

### High-Level Architecture Diagram

```mermaid
graph TB
    subgraph "External Systems"
        GitHub[GitHub/Gerrit]
        Slack[Slack]
        PubSub[Pub/Sub]
    end
    
    subgraph "Prow Components"
        Hook[hook<br/>Webhook Server]
        Controller[prow-controller-manager<br/>Main Controller]
        Plank[plank<br/>Pod Manager]
        Scheduler[scheduler<br/>Job Scheduler]
        Tide[tide<br/>Auto Merge]
        Deck[deck<br/>Web UI]
        Crier[crier<br/>Status Reporter]
        Sinker[sinker<br/>Cleanup]
        Horologium[horologium<br/>Periodic Trigger]
    end
    
    subgraph "Kubernetes Cluster"
        ProwJobs[ProwJob CRDs]
        Pods[Job Pods]
        ConfigMaps[ConfigMaps]
        Secrets[Secrets]
    end
    
    subgraph "Storage"
        GCS[Google Cloud Storage]
        Artifacts[Artifacts & Logs]
    end
    
    GitHub --> Hook
    Hook --> Controller
    Controller --> Plank
    Plank --> Scheduler
    Scheduler --> Pods
    Pods --> GCS
    GCS --> Artifacts
    
    Controller --> ProwJobs
    ProwJobs --> Plank
    
    Tide --> GitHub
    Crier --> GitHub
    Crier --> Slack
    Crier --> PubSub
    
    Deck --> GCS
    Deck --> ProwJobs
    
    Sinker --> ProwJobs
    Sinker --> Pods
    
    Horologium --> Controller
    
    style Hook fill:#e1f5ff
    style Controller fill:#fff4e1
    style Plank fill:#e8f5e9
    style Tide fill:#fce4ec
```

## Component Architecture

### Webhook Flow

```mermaid
sequenceDiagram
    participant Dev as Developer
    participant GH as GitHub
    participant Hook as hook
    participant Controller as Controller
    participant Plank as plank
    participant Pod as Kubernetes Pod
    
    Dev->>GH: Opens/Updates PR
    GH->>Hook: Webhook Event
    Hook->>Hook: Validate Signature
    Hook->>Hook: Execute Plugins
    Hook->>Controller: Create ProwJob
    Controller->>Plank: Reconcile ProwJob
    Plank->>Pod: Create Pod
    Pod->>Pod: Execute Job
    Pod->>GCS: Upload Artifacts
    Pod->>Controller: Update Status
    Controller->>Crier: Job Complete
    Crier->>GH: Update Status
```

### Job Execution Flow

```mermaid
graph TD
    A[Webhook/Periodic Trigger] --> B[Create ProwJob]
    B --> C[Controller Reconciles]
    C --> D{Job Type?}
    D -->|Presubmit| E[Run on PR]
    D -->|Postsubmit| F[Run on Merge]
    D -->|Periodic| G[Run on Schedule]
    E --> H[Plank Creates Pod]
    F --> H
    G --> H
    H --> I[Pod Executes]
    I --> J[Upload Artifacts]
    J --> K[Update Status]
    K --> L[Crier Reports]
    
    style H fill:#e1f5ff
    style I fill:#fff4e1
    style L fill:#e8f5e9
```

### Plugin System Architecture

```mermaid
graph LR
    subgraph "hook Component"
        Webhook[Webhook Handler]
        PluginEngine[Plugin Engine]
        Plugins[Plugins]
    end
    
    subgraph "Plugin Types"
        Trigger[Trigger Plugins]
        Review[Review Plugins]
        Label[Label Plugins]
        Milestone[Milestone Plugins]
        Custom[Custom Plugins]
    end
    
    Webhook --> PluginEngine
    PluginEngine --> Plugins
    Plugins --> Trigger
    Plugins --> Review
    Plugins --> Label
    Plugins --> Milestone
    Plugins --> Custom
    
    style PluginEngine fill:#e1f5ff
    style Plugins fill:#fff4e1
```

### Data Flow Diagram

```mermaid
flowchart TD
    subgraph "Input Sources"
        GHWebhooks[GitHub Webhooks]
        GerritEvents[Gerrit Events]
        CronSchedule[Cron Schedule]
    end
    
    subgraph "Processing"
        Hook[hook]
        Controller[Controller Manager]
        Plank[plank]
        Scheduler[scheduler]
    end
    
    subgraph "Storage"
        K8sAPI[Kubernetes API]
        GCS[GCS Artifacts]
        ConfigRepo[Config Repository]
    end
    
    subgraph "Output"
        GitHubStatus[GitHub Status]
        SlackNotif[Slack Notifications]
        DeckUI[Deck UI]
        Spyglass[Spyglass]
    end
    
    GHWebhooks --> Hook
    GerritEvents --> Hook
    CronSchedule --> Horologium
    
    Hook --> Controller
    Horologium --> Controller
    Controller --> Plank
    Plank --> Scheduler
    Scheduler --> K8sAPI
    
    K8sAPI --> GCS
    ConfigRepo --> Hook
    ConfigRepo --> Controller
    
    GCS --> DeckUI
    GCS --> Spyglass
    K8sAPI --> Crier
    Crier --> GitHubStatus
    Crier --> SlackNotif
    
    style Hook fill:#e1f5ff
    style Controller fill:#fff4e1
```

## Deployment Architecture

### Production Deployment

```mermaid
graph TB
    subgraph "Prow Cluster"
        subgraph "Prow Namespace"
            HookPod[hook Pod]
            ControllerPod[Controller Pod]
            TidePod[tide Pod]
            DeckPod[deck Pod]
            CrierPod[crier Pod]
        end
    end
    
    subgraph "Build Clusters"
        Build01[build01]
        Build02[build02]
        BuildN[buildN...]
    end
    
    subgraph "External Services"
        GitHub[GitHub]
        GCS[Google Cloud Storage]
        Slack[Slack]
    end
    
    HookPod --> ControllerPod
    ControllerPod --> Build01
    ControllerPod --> Build02
    ControllerPod --> BuildN
    
    GitHub --> HookPod
    Build01 --> GCS
    Build02 --> GCS
    BuildN --> GCS
    
    CrierPod --> GitHub
    CrierPod --> Slack
    
    DeckPod --> GCS
    
    style ControllerPod fill:#e1f5ff
    style Build01 fill:#fff4e1
```

### Controller Architecture

The prow-controller-manager runs multiple controllers:

```mermaid
graph LR
    subgraph "prow-controller-manager"
        PlankCtrl[plank Controller]
        SchedulerCtrl[scheduler Controller]
    end
    
    subgraph "Kubernetes API"
        ProwJobs[ProwJobs]
        Pods[Pods]
        ConfigMaps[ConfigMaps]
    end
    
    PlankCtrl --> ProwJobs
    PlankCtrl --> Pods
    SchedulerCtrl --> ProwJobs
    SchedulerCtrl --> ConfigMaps
    
    style PlankCtrl fill:#e1f5ff
    style SchedulerCtrl fill:#fff4e1
```

## Component Interaction Diagram

```mermaid
graph TB
    subgraph "Event Layer"
        Hook[hook]
        Horologium[horologium]
    end
    
    subgraph "Control Layer"
        Controller[Controller Manager]
        Plank[plank]
        Scheduler[scheduler]
    end
    
    subgraph "Management Layer"
        Tide[tide]
        Sinker[sinker]
        StatusReconciler[status-reconciler]
    end
    
    subgraph "Reporting Layer"
        Crier[crier]
        Deck[deck]
    end
    
    Hook --> Controller
    Horologium --> Controller
    Controller --> Plank
    Plank --> Scheduler
    Scheduler --> Tide
    Tide --> Sinker
    Sinker --> StatusReconciler
    StatusReconciler --> Crier
    Crier --> Deck
    
    style Controller fill:#e1f5ff
    style Plank fill:#fff4e1
```

## Key Design Patterns

### 1. Kubernetes Native
Prow uses Kubernetes Custom Resources (ProwJobs) to represent CI jobs:
- ProwJobs are Kubernetes resources
- Jobs execute as Pods
- Standard Kubernetes patterns (watches, informers)

### 2. Controller Pattern
Components follow the Kubernetes controller pattern:
- Watch for resource changes
- Reconcile desired state
- Handle errors and retries gracefully

### 3. Plugin Architecture
Extensible plugin system:
- Plugins are Go packages
- Hook loads and executes plugins
- Plugins can interact with GitHub, Kubernetes, etc.

### 4. Multi-Cluster
Support for distributing jobs across clusters:
- Cluster selection logic
- Per-cluster configurations
- Load balancing

### 5. Event-Driven
System responds to events:
- GitHub webhooks trigger jobs
- Periodic jobs triggered by cron
- Status updates trigger reporting

## Security Architecture

```mermaid
graph TB
    subgraph "Authentication"
        GitHubOAuth[GitHub OAuth]
        HMAC[HMAC Signatures]
        ServiceAccounts[K8s Service Accounts]
    end
    
    subgraph "Authorization"
        OWNERS[OWNERS Files]
        RBAC[Kubernetes RBAC]
        PluginConfig[Plugin Config]
    end
    
    subgraph "Secrets Management"
        K8sSecrets[Kubernetes Secrets]
        Vault[Vault]
        GSM[Google Secret Manager]
    end
    
    GitHubOAuth --> OWNERS
    HMAC --> PluginConfig
    ServiceAccounts --> RBAC
    
    OWNERS --> K8sSecrets
    RBAC --> K8sSecrets
    PluginConfig --> Vault
    
    Vault --> GSM
    GSM --> K8sSecrets
    
    style HMAC fill:#e1f5ff
    style RBAC fill:#fff4e1
```

## Scalability Considerations

1. **Horizontal Scaling**: Components can be scaled horizontally
2. **Multi-Cluster**: Jobs distributed across multiple clusters
3. **Caching**: ghproxy caches GitHub API responses
4. **Resource Management**: Sinker cleans up old resources
5. **Efficient API Usage**: Informers and watches for Kubernetes API

## Monitoring and Observability

```mermaid
graph LR
    subgraph "Metrics Collection"
        Prometheus[Prometheus]
        Metrics[Metrics Endpoints]
    end
    
    subgraph "Logging"
        Stackdriver[Stackdriver Logs]
        LocalLogs[Local Log Files]
    end
    
    subgraph "Tracing"
        OpenTelemetry[OpenTelemetry]
    end
    
    subgraph "Dashboards"
        Grafana[Grafana]
        DeckUI[Deck UI]
    end
    
    Prometheus --> Grafana
    Metrics --> Prometheus
    Stackdriver --> Grafana
    OpenTelemetry --> Grafana
    DeckUI --> Metrics
    
    style Prometheus fill:#e1f5ff
    style Grafana fill:#fff4e1
```

## Notes

Note that Prow can also work with Gerrit, albeit with less features.
Specifically, neither **Tide** nor **Hook** work with Gerrit yet.
