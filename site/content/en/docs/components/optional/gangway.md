---
title: "Gangway (Prow API)"
description: >
  Gangway is an optional component which allows you to interact with Prow in a programmatic way (through an API).
weight: 40
---

## Architecture

See the [design doc][design-doc].

Gangway uses gRPC to serve several endpoints. These can be seen in the
[`gangway.proto`][gangway.proto] file, which describes the gRPC endpoints. The
proto describes the interface at a high level, and is converted into low-level
Golang types into [`gangway.pb.go`][gangway.pb.go] and
[`gangway_grpc.pb.go`][gangway_grpc.pb.go]. These low-level Golang types are
then used in the  [`gangway.go`][gangway.go] file to implement the high-level
intent of the proto file.

As Gangway only understands gRPC natively, if you want to use a REST client
against it you must deploy Gangway. For example, on GKE you can use Cloud
Endpoints and deploy Gangway behind a reverse proxy called "ESPv2". This ESPv2
container will forward HTTP requests made to it to the equivalent gRPC endpoint
in Gangway and back again.

## Configuration setup

### Server-side configuration

Gangway has its own security check to see whether the client is allowed to, for
example, trigger the job that it wants to trigger (we don't want to let any
random client trigger any Prow Job that Prow knows about). In the central Prow
config under the `gangway` section, prospective Gangway users can list
themselves in there. For an example, see the section filled out for Gangway's
own [integration tests][integration-test-config] and search for
`allowed_jobs_filters`.

### Client-side configuration

The table below lists the supported endpoints.

| Endpoint           | Description                              |
|:-------------------|:-----------------------------------------|
| CreateJobExecution | Triggers a new Prow Job.                 |
| GetJobExecution    | Get the status of a Prow Job.            |
| ListJobExecutions  | List all Prow Jobs that match the query. |

See [`gangway.proto`][gangway.proto] and the [Gangway Google
client][gangway-client-google].

## Tutorial

See the [example][example].

[example]:https://github.com/kubernetes/test-infra/blob/master/prow/examples/gangway/main.go 
[gangway.proto]:https://github.com/kubernetes/test-infra/blob/master/prow/gangway/gangway.proto
[gangway.pb.go]:https://github.com/kubernetes/test-infra/blob/master/prow/gangway/gangway.pb.go
[gangway_grpc.pb.go]:https://github.com/kubernetes/test-infra/blob/master/prow/gangway/gangway_grpc.pb.go
[gangway.go]:https://github.com/kubernetes/test-infra/blob/master/prow/gangway/gangway.go
[design-doc]:https://docs.google.com/document/d/1v77jp1Nb5C2C2-PdV02SGViO9CyZ9SvNxCPOHyIUQeo/edit?usp=sharing
[integration-test-config]:https://github.com/kubernetes/test-infra/blob/f3e439df9f34818fd35a7cc8f2546070540429e4/prow/test/integration/config/prow/config.yaml#L71
[gangway-client-google]:https://github.com/kubernetes/test-infra/blob/master/prow/gangway/client/google/google.go
