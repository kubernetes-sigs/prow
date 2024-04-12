---
title: "Gerrit"
weight: 150
description: >
  
---

[Gerrit](https://www.gerritcodereview.com/) is a free, web-based team code collaboration tool.

## Related Deployments

- Prow-gerrit adapter ([doc](/docs/components/optional/gerrit/), [code](https://github.com/kubernetes-sigs/prow/tree/main/prow/cmd/gerrit))
- Crier (the reporter) ([doc](/docs/components/core/crier/), [code](https://github.com/kubernetes-sigs/prow/tree/main/prow/cmd/crier))

## Related packages

#### Client

We have a [gerrit-client package](https://github.com/kubernetes-sigs/prow/tree/main/prow/gerrit/client) that provides a thin wrapper around  
[andygrunwald/go-gerrit](https://github.com/andygrunwald/go-gerrit), which is a go client library
for accessing the [Gerrit Code Review REST API](https://gerrit-review.googlesource.com/Documentation/rest-api.html)

You can create a client instance by pass in a map of instance-name:project-ids, and pass in an oauth token path to
start the client, like:

```go
projects := map[string][]string{
 "foo.googlesource.com": {
  "project-bar",
  "project-baz",
 },
}

c, err := gerrit.NewClient(projects)
if err != nil {
 // handle error
}
c.Start(cookiefilePath)
```

The client will try to refetch token from the path every 10 minutes.

You should also utilize [`grandmatriarch`](/docs/components/undocumented/grandmatriarch/) to generate a token from a
passed-in service account credential.

If you need extra features, feel free to introduce new gerrit API functions to the client package.

#### Adapter

The adapter package implements a controller that is periodically polling gerrit, and triggering
presubmit and postsubmit jobs based on your prow config.

#### Gerrit Labels

Prow adds the following [Labels](https://github.com/kubernetes-sigs/prow/blob/main/prow/gerrit/client/client.go) to Gerrit Presubmits that can be accessed in the container by leveraging the [Downward API](https://kubernetes.io/docs/tasks/inject-data-application/environment-variable-expose-pod-information/).

- "prow.k8s.io/gerrit-revision": SHA of current patchset from a gerrit change
- "prow.k8s.io/gerrit-patchset": Numeric ID of the current patchset
- "prow.k8s.io/gerrit-report-label": Gerrit label prow will cast vote on, fallback to CodeReview label if unset

```yaml
    - name: PATHCSET_NUMBER
      valueFrom:
        fieldRef:
          fieldPath: metadata.labels['prow.k8s.io/gerrit-patchset']
```

## Caveat

The gerrit adapter currently does not support [gerrit hooks](https://gerrit-review.googlesource.com/Documentation/config-hooks.html),
If you need them, please send us a PR to support them :-)
