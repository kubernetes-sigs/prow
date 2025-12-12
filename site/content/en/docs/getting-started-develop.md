---
title: "Developing and Contributing to Prow"
weight: 90
description: >
  
---

## Contributing

Thank you for your interest in contributing to Prow! This guide will help you understand how to contribute effectively.

Please consider upstreaming any changes or additions you make! Contributions in any form (issues, pull requests, even constructive comments in discussions) are more than welcome!
You can develop in-tree for more help and review, or out-of-tree if you need to for whatever reason. If you upstream a new feature or a change that impacts the default behavior of Prow, consider adding an [announcement](/docs/announcements/) about it and dropping an email at the [sig-testing](https://groups.google.com/forum/#!forum/kubernetes-sig-testing) mailing list.

**New Contributors** should search for issues in kubernetes-sigs/prow with the `help-wanted` and/or `good first issue` labels. [(Query link)](https://github.com/kubernetes-sigs/prow/issues?utf8=%E2%9C%93&q=is%3Aopen+is%3Aissue+label%3A%22help+wanted%22). Before starting work please ensure that the issue is still active and then provide a short design overview of your planned solution.
Also reach out on the Kubernetes slack in the `sig-testing` channel.

## How to Contribute

### 1. Fork and Clone

```bash
# Fork the repository on GitHub, then clone your fork
git clone https://github.com/YOUR_USERNAME/prow.git
cd prow

# Add upstream remote
git remote add upstream https://github.com/kubernetes-sigs/prow.git
```

### 2. Create a Branch

```bash
# Create a feature branch from main
git checkout -b feature/my-feature

# Or a bugfix branch
git checkout -b fix/bug-description
```

### 3. Make Your Changes

- Write clear, readable code
- Follow Go conventions and style
- Add tests for new functionality
- Update documentation as needed

### 4. Test Your Changes

```bash
# Run unit tests
make test

# Run linters
make verify

# Run integration tests
go test ./test/integration/...
```

### 5. Commit Your Changes

```bash
# Stage changes
git add .

# Commit with descriptive message
git commit -m "Add feature: description of changes"
```

**Commit Message Guidelines:**
- Use imperative mood ("Add feature" not "Added feature")
- Keep first line under 72 characters
- Add detailed description if needed
- Reference issues: "Fix #123: description"

### 6. Push and Create Pull Request

```bash
# Push to your fork
git push origin feature/my-feature
```

Then create a Pull Request on GitHub with:
- Clear title and description
- Reference to related issues
- Screenshots/logs if applicable
- Checklist of what was tested

## Branching Model

### Branch Naming

- `feature/description` - New features
- `fix/description` - Bug fixes
- `docs/description` - Documentation updates
- `refactor/description` - Code refactoring
- `test/description` - Test improvements

### Branch Strategy

- **main** - Production-ready code
- **Feature branches** - Created from main, merged back via PR
- **Release branches** - For release-specific fixes (if needed)

## Coding Standards

### Go Style

Follow [Effective Go](https://golang.org/doc/effective_go) and [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments).

**Formatting:**
```bash
# Use gofmt
make update-gofmt

# Or goimports (handles imports)
goimports -w .
```

**Key Guidelines:**
- Use `gofmt` for formatting
- Run `goimports` to organize imports
- Follow naming conventions
- Keep functions focused and small
- Add comments for exported functions/types

### Code Organization

- **Packages**: Group related functionality
- **Files**: Keep files focused
- **Tests**: `*_test.go` files alongside source
- **Test Data**: Use `testdata/` directories

### Error Handling

```go
// Good: Wrap errors with context
if err != nil {
    return fmt.Errorf("failed to load config: %w", err)
}

// Good: Use errors.Is and errors.As
if errors.Is(err, os.ErrNotExist) {
    // handle
}
```

### Logging

```go
// Use logrus for structured logging
import "github.com/sirupsen/logrus"

logrus.WithFields(logrus.Fields{
    "config": configPath,
    "error": err,
}).Error("Failed to load config")
```

### Testing

**Unit Tests:**
```go
func TestFunction(t *testing.T) {
    // Arrange
    input := "test"
    
    // Act
    result := Function(input)
    
    // Assert
    if result != expected {
        t.Errorf("Expected %v, got %v", expected, result)
    }
}
```

## PR Guidelines

### PR Requirements

1. **Description**: Clear description of what and why
2. **Tests**: All tests pass
3. **Linting**: Code passes linting
4. **Documentation**: Updated if needed
5. **Size**: Keep PRs focused and reasonably sized

### PR Template

```markdown
## Description
Brief description of changes

## Problem
What problem does this solve?

## Solution
How does this solve the problem?

## Testing
- [ ] Unit tests added/updated
- [ ] Integration tests pass
- [ ] Manual testing performed

## Related Issues
Fixes #123
Related to #456
```

### Review Process

1. **Automated Checks**: Must pass (tests, linting, etc.)
2. **Code Review**: At least one approval required
3. **LGTM**: Reviewer says `/lgtm` when satisfied
4. **Approve**: Approver says `/approve` for final approval
5. **Merge**: Automated merge by Tide when all conditions met

## Code Review Guidelines

### For Reviewers

**What to Look For:**
- Code correctness and logic
- Test coverage
- Error handling
- Performance considerations
- Security implications
- Documentation completeness

**Review Checklist:**
- [ ] Code follows style guidelines
- [ ] Tests are adequate
- [ ] Error handling is proper
- [ ] Documentation is updated
- [ ] No security issues
- [ ] Performance is acceptable

### For Authors

**Before Requesting Review:**
- Self-review your code
- Run all tests
- Check linting
- Update documentation
- Write clear PR description

**During Review:**
- Respond promptly to comments
- Be open to suggestions
- Ask for clarification if needed
- Update code based on feedback

## Testing Requirements

### Unit Tests

- Required for new functionality
- Aim for >80% coverage for new code
- Test edge cases and error conditions

### Integration Tests

- Required for components that modify external state
- Use test data in `test/integration/`
- Test with real Kubernetes clusters when possible

## Documentation

### Code Documentation

- Document exported functions/types
- Use Go doc comments
- Include examples for complex APIs

### User Documentation

- Update README.md for component changes
- Add examples for new features
- Update this contributing guide if process changes

## Getting Help

### Communication Channels

- **GitHub Issues**: Bug reports and feature requests
- **Pull Requests**: Code contributions and discussions
- **Slack**: #sig-testing on Kubernetes Slack

### Resources

- [Go Documentation](https://golang.org/doc/)
- [Kubernetes Contributing Guide](https://github.com/kubernetes/community/blob/master/contributors/guide/README.md)
- [Prow Documentation](https://docs.prow.k8s.io/)

## Code of Conduct

- Be respectful and inclusive
- Welcome newcomers
- Focus on constructive feedback
- Follow the [Kubernetes Code of Conduct](https://github.com/kubernetes/community/blob/master/code-of-conduct.md)

Thank you for contributing to Prow! 🎉

## Prow Integration Points

There are a number of ways that you can write code for Prow or integrate existing code with Prow.

#### Plugins

[Prow plugins](/docs/components/plugins/) are sub-components of the [`hook`](/docs/components/core/hook/) binary that register event handlers for various types of GitHub events.
Plugin event handlers are provided a [`PluginClient`](https://godoc.org/sigs.k8s.io/prow/pkg/plugins#PluginClient) that provides access to a suite of clients and agents for configuration, ProwJobs, GitHub, git, OWNERS file, Slack, and more.

##### How to add new plugins

Add a new package under `plugins` with a method satisfying one of the handler
types in `plugins`. In that package's `init` function, call
`plugins.Register*Handler(name, handler)`. Then, in `hook/plugins.go`, add an
empty import so that your plugin is included. If you forget this step then a
unit test will fail when you try to add it to `plugins.yaml`. Don't add a brand
new plugin to the main `kubernetes/kubernetes` repo right away, start with
somewhere smaller and make sure it is well-behaved.

The [`lgtm` plugin](/docs/components/plugins/lgtm/) is a good place to start if you're looking for an example
plugin to mimic.

##### External plugins

For even more flexibility, *anything* that receives GitHub webhooks can be configured to be forwarded webhooks as an [external plugin](/docs/components/plugins/#external-plugins). This allows in-cluster or out of cluster plugins and forwarding to other bots or infrastructure.

#### Cluster Deployments

Additional cluster components can use the informer framework for ProwJobs in order to react to job creation, update, and deletion.
This can be used to implement additional job execution controllers for executing job with different agents. For example, `jenkins-operator` executes jobs on jenkins, `plank` uses kubernetes pods, and `build` uses the build CRD.
The informer framework can also be used to react to job completion or update in order to create an alternative job reporting mechanism.

#### Artifact Viewers

[Spyglass](/docs/spyglass/) artifact viewers allow for custom display of ProwJob artifacts that match a certain file regexp. Existing viewers display logs, metadata, and structured junit results.

#### ProwJobs

[ProwJobs](/docs/jobs/) themselves are often a sufficient integration point if you just need to execute a task on a schedule or in reaction to code changes.

#### Exposed Data

If you just need some data from Prow you may be able to get it from the JSON exposed by Prow's front end `deck`, or from Prometheus metrics.

## Building, Testing, and Deploying

You can build, test, and deploy Prow’s binaries, container images, and cluster resources. See ["Deploying Prow"](/docs/getting-started-deploy/) for initially deploying Prow and ["Building, Testing, and Updating Prow"](/docs/build-test-update/) for iterating on an existing deployment.
