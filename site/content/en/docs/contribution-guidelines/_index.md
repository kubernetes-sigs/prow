---
title: "Contribution Guidelines"
weight: 50
description: >
  How to contribute to the docs
---

## Clearing out of Legacy Snapshot

Our docs have been migrated from the [Prow folder inside the
kubernetes/test-infra repository][k/t-i/prow] to the
[kubernetes-sigs/prow][k-s/prow] repository (the page you are reading is
generated from kubernetes-sigs/prow). However, these migrated files have been
placed under the [Legacy Snapshot][l-s] directory because they have not been
vetted by the Prow team as being up-to-date. The original files [have been
frozen][orig-freeze] and can no longer be modified.

**Our current top priority is to review docs under the [Legacy Snapshot][l-s] and
to move them to a more appropriate section. Please contribute!**

## Updating existing docs

If you need to update an existing doc (that is, in
`kubernetes/test-infra/prow/.*\.md`), you must find the corresponding file in
[Legacy Snapshot][l-s] and move it to a more appropriate location.

## Tooling

We use [Hugo](https://gohugo.io/) to format and generate our website, the
[Docsy](https://github.com/google/docsy) theme for styling and site structure, 
and [Netlify](https://www.netlify.com/) to manage the deployment of the site. 
Hugo is an open-source static site generator that provides us with templates, 
content organisation in a standard directory structure, and a website generation 
engine. You write the pages in Markdown (or HTML if you want), and Hugo wraps them up into a website.

## Useful resources

* [Docsy user guide](https://www.docsy.dev/docs/): All about Docsy (the Hugo theme used on this site).

[k/t-i/prow]:https://github.com/kubernetes/test-infra/tree/master/prow
[k-s/prow]:https://github.com/kubernetes-sigs/prow
[l-s]:{{< relref "../legacy-snapshot" >}}
[orig-freeze]:https://github.com/kubernetes/test-infra/pull/26458
