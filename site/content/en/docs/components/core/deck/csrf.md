---
title: "CSRF attacks"
weight: 20
description: >
  
---

In Deck, we make a number of `POST` requests that require user authentication. These requests are susceptible
to [cross site request forgery (CSRF) attacks](https://en.wikipedia.org/wiki/Cross-site_request_forgery), 
in which a malicious actor tricks an already authenticated user into submitting a form to one of these endpoints 
and performing one of these protected actions on their behalf. 

## Protection

CSRF protection is always enabled in Deck using Go's standard library
[`net/http.CrossOriginProtection`](https://pkg.go.dev/net/http#CrossOriginProtection), which validates
the `Sec-Fetch-Site` and `Origin` headers on incoming requests. No configuration is required.

This protection works by ensuring that any `POST` request originates from the same origin as Deck,
rather than from a cross-origin site. Safe methods (`GET`, `HEAD`, `OPTIONS`) are always allowed.
Non-browser requests (those without `Sec-Fetch-Site` or `Origin` headers) are also allowed, so API
clients like `curl` and CI scripts continue to work.

If you are adding a new `POST` endpoint, no additional CSRF handling is needed — the middleware
protects all routes automatically.

CSRF can also be executed by tricking a user into making a state-mutating `GET` request. All 
state-mutating requests must therefore be `POST` requests, as the CSRF middleware does not restrict
`GET` requests.
