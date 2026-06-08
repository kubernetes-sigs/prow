---
title: "Managing GitHub API Access"
weight: 104
description: >
  
---

Prow components communicate with GitHub to handle webhooks, manage pull requests, update commit statuses, and more. This guide covers operational aspects of authenticating to GitHub, configuring components that access it, and managing its usage through the ghproxy caching proxy.

## GitHub Authentication

Prow supports two authentication methods: personal access tokens and GitHub Apps. Both methods provide API credentials for Prow components to interact with GitHub.

### GitHub Token Authentication

The first authentication method is generating a [personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens) for a GitHub account. This method is suitable for personal and ad-hoc testing deployments, but not recommended for production use.

Components accept the `--github-token-path` flag pointing to a file containing the token. In a cluster deployment, the token would typically be stored in a Kubernetes Secret and mounted as a volume to make the file available to the component.

### GitHub App Authentication

The second authentication method uses [GitHub Apps](https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/about-creating-github-apps). See the [Deploying Prow](/docs/getting-started-deploy/#github-app) guide for how to set up a GitHub App for Prow.

GitHub Apps require two secrets: the App ID and a private key. Components accept the `--github-app-id` flag (typically passed as an environment variable) and the `--github-app-private-key-path` flag pointing to a file containing the private key. In a cluster deployment, both would typically be stored in a Kubernetes Secret and made available to the component.

GitHub Apps are the recommended approach for production deployments because they provide:

- **Higher rate limits:** GitHub Apps receive higher API rate limits (5,000 requests/hour per installation vs 5,000/hour for personal tokens)
- **Granular permissions:** More fine-grained control over what Prow can access
- **Audit trail:** Actions appear as the App rather than a user account
- **Organization-wide:** Easier to manage access across multiple repositories

## Managing API Rate Limits

GitHub enforces [rate limits](https://docs.github.com/en/rest/overview/resources-in-the-rest-api#rate-limiting) on API requests. Each authentication method receives an hourly token budget that is consumed by API calls. GitHub has separate rate limits for different API endpoints:

- **REST API (v3):** 5,000 requests/hour for authenticated requests (higher for GitHub Apps per installation)
- **GraphQL API (v4):** Uses a point-based system with a budget of 5,000 points/hour
- **Search API:** 30 requests/minute (shared across v3 and v4)

When the budget is exhausted, GitHub returns HTTP 403 responses until the hourly window resets. The GitHub client library tracks remaining tokens from response headers and can throttle requests to avoid hitting the limit.

Prow provides two mechanisms to manage rate limits:

### Client-side Throttling

Components accept flags to configure how aggressively they consume the API token budget:

- `--github-hourly-tokens`: Maximum GitHub tokens to consume per hour (default: matches GitHub's limit)
- `--github-allowed-burst`: Maximum burst of tokens that can be consumed at once (default: 100)

These flags allow operators to reserve part of the token budget for other tools or configure more conservative throttling. The client library automatically throttles requests when approaching the configured limit.

### ghproxy Caching Proxy

[ghproxy](/docs/ghproxy/) is a reverse proxy HTTP cache designed for the GitHub API. It caches responses and reduces redundant requests, helping Prow stay within rate limits even under heavy load.

**Benefits:**

- Reduces API token usage by serving cached responses
- Shares a single cache across all Prow components
- Optimized client throttling that doesn't count cached responses against the budget
- Automatic handling of conditional requests (ETags) for efficient cache revalidation

### Deploying ghproxy

ghproxy is deployed as a service in the Prow cluster. It requires a persistent volume for the cache and accepts flags to configure cache size and location. See the [ghproxy documentation](/docs/ghproxy/) for deployment details and configuration options.

### Configuring Components to Use ghproxy

Components that access the GitHub API accept multiple endpoint flags for different API types. Each flag can be specified multiple times to provide fallback endpoints:

- `--github-endpoint`: REST API (v3) endpoint
- `--github-graphql-endpoint`: GraphQL API (v4) endpoint  
- `--github-search-endpoint`: Search API endpoint (often the same as REST)

When multiple endpoints are provided for the same API type, components try them in order and fall back to the next if one is unavailable. The typical pattern is to specify ghproxy first, followed by the direct GitHub API as a fallback:

```
--github-endpoint=http://ghproxy
--github-endpoint=https://api.github.com
--github-graphql-endpoint=http://ghproxy/graphql
--github-graphql-endpoint=https://api.github.com/graphql
```

The GitHub client library automatically tracks which requests were served from cache (ghproxy) versus direct API calls, and optimizes throttling accordingly—cached responses don't count against the token budget.

> **Note:** GitHub Enterprise deployments use different API endpoint URLs. See [Deploying with GitHub Enterprise](/docs/getting-started-deploy/#deploying-with-github-enterprise) for complete configuration details.

## See Also

- [Deploying Prow](/docs/getting-started-deploy/) - Complete deployment guide including GitHub App setup
- [ghproxy](/docs/ghproxy/) - Internal architecture and advanced configuration
- [GitHub API Library](/docs/github/) - Developer documentation for the Go client library
