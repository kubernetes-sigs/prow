---
title: "Setup Guide"
weight: 85
description: >
  Step-by-step instructions for setting up your development environment
---

# Setup Guide (Beginner Friendly)

This guide will help you set up a development environment for working with Prow.

## Prerequisites

### Required Software

1. **Go** (version 1.24.0 or later)
   ```bash
   # Check your Go version
   go version
   
   # If not installed, download from https://golang.org/dl/
   ```

2. **Git**
   ```bash
   git --version
   # Install via your package manager if needed
   ```

3. **Make**
   ```bash
   make --version
   # Usually pre-installed on Linux/macOS
   ```

4. **Docker** (optional, for building container images)
   ```bash
   docker --version
   ```

5. **kubectl** (for interacting with Kubernetes)
   ```bash
   kubectl version --client
   ```

### Optional but Recommended

- **kind** or **minikube** - For local Kubernetes cluster
- **ko** - For building container images
- **jq** - For JSON processing
- **yq** - For YAML processing

## Installation Steps

### 1. Clone the Repository

```bash
# Clone the repository
git clone https://github.com/kubernetes-sigs/prow.git
cd prow

# If you plan to contribute, fork first and clone your fork
```

### 2. Set Up Go Environment

```bash
# Set Go environment variables (if not already set)
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin

# Verify Go is working
go env
```

### 3. Install Dependencies

```bash
# Download dependencies
go mod download

# Verify dependencies
go mod verify
```

### 4. Build the Components

```bash
# Build all components
make build

# Or install to $GOPATH/bin
go install ./cmd/...

# Build specific component
go build ./cmd/hook
```

### 5. Verify Installation

```bash
# Check that components are installed
which hook
hook --help

# List all available components
ls $GOPATH/bin/ | grep -E "(hook|deck|tide|plank)"
```

## Development Environment Setup

### IDE Setup

**VS Code:**
1. Install Go extension
2. Install Kubernetes extension
3. Configure Go settings

**GoLand:**
1. Import project
2. Configure Go SDK
3. Set up Kubernetes integration

### Local Kubernetes Cluster

**Using kind:**
```bash
# Install kind
go install sigs.k8s.io/kind@latest

# Create cluster
kind create cluster --name prow

# Verify cluster
kubectl cluster-info --context kind-prow
```

**Using minikube:**
```bash
# Install minikube
# See https://minikube.sigs.k8s.io/docs/start/

# Start cluster
minikube start

# Verify cluster
kubectl get nodes
```

## Testing Your Setup

### Run Unit Tests

```bash
# Run all unit tests
make test

# Run specific package tests
go test ./pkg/hook/...

# Run with verbose output
go test -v ./pkg/hook/...
```

### Run Integration Tests

```bash
# Run integration tests
go test ./test/integration/...

# Run specific integration test
go test ./test/integration/... -run TestName
```

### Build Container Images

```bash
# Build images using ko
ko build ./cmd/hook

# Or using Docker
docker build -t prow/hook:latest ./cmd/hook
```

## Common Issues and Troubleshooting

### Go Version Issues

**Problem**: Go version too old
**Solution**: Update Go to 1.24.0 or later

```bash
# Check version
go version

# Update Go (example for Linux)
wget https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
```

### Dependency Issues

**Problem**: `go mod download` fails
**Solution**: Clear module cache and retry

```bash
go clean -modcache
go mod download
```

### Build Issues

**Problem**: Build fails with import errors
**Solution**: Ensure you're in the correct directory and dependencies are installed

```bash
# Verify you're in the prow directory
pwd

# Re-download dependencies
go mod download
go mod tidy
```

### Kubernetes Connection Issues

**Problem**: Cannot connect to Kubernetes cluster
**Solution**: Verify kubeconfig

```bash
# Check kubeconfig
kubectl config view

# Test connection
kubectl get nodes
```

## Next Steps

After setting up your environment:

1. Read the [Usage Guide](/docs/usage/) to learn how to use Prow
2. Explore the [Codebase Walkthrough](/docs/codebase-walkthrough/) to understand the structure
3. Check out the [Contributing Guide](/docs/getting-started-develop/) to start contributing
4. Review the [Onboarding Guide](/docs/onboarding/) for new contributors

## Additional Resources

- [Go Documentation](https://golang.org/doc/)
- [Kubernetes Documentation](https://kubernetes.io/docs/)
- [kind Documentation](https://kind.sigs.k8s.io/)
- [minikube Documentation](https://minikube.sigs.k8s.io/)

