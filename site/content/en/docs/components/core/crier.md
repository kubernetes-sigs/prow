---
title: "Crier"
weight: 10
description: >
  
---

Crier reports your prowjobs on their status changes.

## Optional scheduling evidence

`--kubernetes-scheduling-evidence` is disabled by default and requires positive
`--kubernetes-blob-storage-workers`. It enables an independent collector in Crier,
not in the job's containers. A parent pod need not have started any container.
It writes `scheduling-evidence.json` beside `podinfo.json`, using the same job
destination, storage client, and visibility. It respects Kubernetes report sampling
and dry-run. No deployment permissions are changed automatically.

While the parent is unassigned, the reporter requests another reconciliation after
one minute. Additional job events cannot cause more than one attempt per minute
per job in an observer process. The throttle holds at most 1024 jobs, expires idle
entries after an hour, and skips optional collection at capacity. There are at most
32 observations per job, plus a terminal event refresh replacing the last observation
when necessary. Sampling stops after assignment or the sample limit. Each optional
attempt has a five-second deadline within the existing reporter deadline. It makes
at most one bounded list each for workers (200), all-namespace occupants (2000), and
parent-UID events (1000), without following continuation tokens. Partial pages are
explicitly marked `truncated`, never interpreted as a complete inventory.

### Public data contract

The projection contains timestamps, coverage states, and sorted anonymous worker
occupancy rows: Ready condition, the inverse of `spec.unschedulable`, and counts
of nonterminal assigned pods whose `pod-scaler.openshift.io/measured` label is
`true`, `false`, or neither. Rows relate occupancy categories to individual workers
within an observation, but deliberately cannot correlate worker identities across
observations. These are occupancy observations, not an eligibility simulation:
taints, tolerations, resource fit, and arbitrary affinity are not evaluated.

No node, pod, namespace, or customer identities, addresses, provider IDs, images,
environment, annotations, arbitrary labels, raw event messages, or API error text
are copied into this new artifact. Its random observer identifier is unrelated to
cluster identities. Existing `podinfo.json` behavior and its data policy are unchanged.

Only `Pod` events matching the parent's nonempty UID and creation-to-observation
(or completion) time window are counted. Recognized scheduler events are
`FailedScheduling` and `Scheduled` from `default-scheduler`. Recognized autoscaler
events are `TriggeredScaleUp`, `NotTriggerScaleUp`, and `FailedToScaleUp` from
`cluster-autoscaler`. Output uses fixed reason categories, not messages or raw
producer strings. Counts describe matching event objects, not cumulative event
occurrences; repeated snapshots are not additive. Aggregated events use the last
observation timestamp and do not establish when every occurrence happened.

**Events do not provide full autoscaler controller reasoning.** The manifest always
sets `controller_logs` to `unavailable_not_collected`. Restricted controller-log
capture is unimplemented. Implementing it would require a separate privacy and
retention design, a restricted destination, and namespace-scoped log permissions;
operators must not upload raw controller logs to the public job destination.

### Coverage and failures

The artifact is updated after each observation and finalized on job completion.
After assignment or completion, event decisions can be refreshed but current
occupancy is not reported as historical run-window evidence.
It is also visible for jobs that eventually succeed. It records that history before
the first observation is unavailable, observer restarts, gaps exceeding two minutes,
assignment and sample-limit stops, parent access failures, and per-source
`available`, `forbidden`, `cancelled`, `error`, or `truncated` states. Snapshots are
not atomic across sources. Event retention and observation intervals can hide
decisions; empty results are not proof that no scaling action occurred.

The prior manifest is read back on reconciliation, retaining observations across
process restarts. A changed observer identifier explicitly marks a restart. Run
only one active Crier observer per job destination; concurrent writers are not
coordinated and can overwrite observations. Failed storage reads do not overwrite
history. Prior manifests with unsupported versions, different run start times, out-of-bound
collections, or non-allowlisted coverage/decision strings are rejected rather than
republished. This validation does not repair already stored objects.
Failed uploads leave the prior manifest; later observations may record an interval
gap. If storage stays unavailable, a
final manifest cannot be guaranteed: a missing or nonfinal artifact is incomplete
coverage. Optional failures produce a fixed warning without raw API errors.
Baseline pod information is uploaded and its finalizer released before optional
terminal capture. Optional failure cannot block that baseline operation or retry
completed jobs indefinitely. The collector does not alter affinity, placement,
scheduling gates, job deadlines, rescheduling, or retries.

### Operator opt-in permissions

The existing Kubernetes reporter uses core `pods` `get` and `patch` (finalizer)
and core `events` `list` in the parent namespace, plus core `nodes` `get` for baseline
pod information. In addition, the Crier build-cluster identity needs a ClusterRole
allowing only `list` on core `nodes` and `pods`, bound to that identity. All-namespace
pod lists are necessary for cross-namespace occupancy. Event lists remain scoped
to the parent namespace and UID. No new `watch` permission is needed. RBAC cannot
restrict returned fields: the collector
enforces the public projection in code. Review the broader read visibility before
opting in. No secrets, exec, host, cloud, or `pods/log` access is required.

Roll out the Crier image containing this feature first, review the read-only RBAC,
then enable the flag on a small deployment with existing worker limits. Check
coverage states and API/storage load before widening rollout. Disable the flag to
restore baseline behavior. This change supplies no cluster manifest or live rollout.

## Usage / How to enable existing available reporters

For any reporter you want to use, you need to mount your prow configs and specify `--config-path` and `job-config-path`
flag as most of other prow controllers do.

### [Gerrit reporter](https://github.com/kubernetes-sigs/prow/tree/main/pkg/crier/reporters/gerrit)

You can enable gerrit reporter in crier by specifying `--gerrit-workers=n` flag.

Similar to the [gerrit adapter](/docs/components/optional/gerrit/), you'll need to specify `--gerrit-projects` for
your gerrit projects, and also `--cookiefile` for the gerrit auth token (leave it unset for anonymous).

Gerrit reporter will send an aggregated summary message, when all [gerrit adapter](/docs/components/optional/gerrit/)
scheduled prowjobs with the same report label finish on a revision.
It will also attach a report url so people can find logs of the job.

The reporter will also cast a +1/-1 vote on the `prow.k8s.io/gerrit-report-label` label of your prowjob,
or by default it will vote on `CodeReview` label. Where `+1` means all jobs on the patshset pass and `-1`
means one or more jobs failed on the patchset.

### [Pubsub reporter](https://github.com/kubernetes-sigs/prow/tree/main/pkg/crier/reporters/pubsub)

You can enable pubsub reporter in crier by specifying `--pubsub-workers=n` flag.

You need to specify following labels in order for pubsub reporter to report your prowjob:

| Label                          | Description                                                                                                                               |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------|
| `"prow.k8s.io/pubsub.project"` | Your gcp project where pubsub channel lives                                                                                               |
| `"prow.k8s.io/pubsub.topic"`   | The [topic](https://cloud.google.com/pubsub/docs/publisher) of your pubsub message                                                        |
| `"prow.k8s.io/pubsub.runID"`   | A user assigned job id. It's tied to the prowjob, serves as a name tag and help user to differentiate results in multiple pubsub messages |

The service account used by crier will need to have `pubsub.topics.publish` permission in the project where pubsub channel lives, e.g. by assigning the `roles/pubsub.publisher` IAM role

Pubsub reporter will report whenever prowjob has a state transition.

You can check the reported result by [list the pubsub topic](https://cloud.google.com/sdk/gcloud/reference/pubsub/topics/list).

### [GitHub reporter](https://github.com/kubernetes-sigs/prow/tree/main/pkg/crier/reporters/github)

You can enable github reporter in crier by specifying `--github-workers=N` flag (N>0).

The GitHub reporter uses the GitHub API to report job results as commit statuses and check runs. It requires GitHub authentication credentials and should be configured with ghproxy to manage rate limits. See [Managing GitHub API Access](/docs/github-api-access/) for details on authentication methods, endpoint configuration, and rate limit management.

The actual report logic is in the [github report library](https://github.com/kubernetes-sigs/prow/tree/main/pkg/github/report) for your reference.

### [Slack reporter](https://github.com/kubernetes-sigs/prow/tree/main/pkg/crier/reporters/slack)

> **NOTE:** if enabling the slack reporter for the *first* time, Crier will message to the Slack channel for **all** ProwJobs matching the configured filtering criteria.

You can enable the Slack reporter in crier by specifying the `--slack-workers=n` and `--slack-token-file=path-to-tokenfile` flags.

The `--slack-token-file` flag takes a path to a file containing a Slack [**OAuth Access Token**](https://api.slack.com/docs/oauth).

The **OAuth Access Token** can be obtained as follows:

1. Navigate to: <https://api.slack.com/apps>.
1. Click **Create New App**.
1. Provide an **App Name** (e.g. Prow Slack Reporter) and **Development Slack Workspace** (e.g. Kubernetes).
1. Click **Permissions**.
1. Add the `chat:write.public` scope using the **Scopes / Bot Token Scopes** dropdown and **Save Changes**.
1. Click **Install App to Workspace**
1. Click **Allow** to authorize the Oauth scopes.
1. Copy the **OAuth Access Token**.

Once the *access token* is obtained, you can create a `secret` in the cluster using that value:

```shell
kubectl create secret generic slack-token --from-literal=token=< access token >
```

Furthermore, to make this token available to **Crier**, mount the *slack-token* `secret` using a `volume` and set the `--slack-token-file` flag in the deployment spec.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: crier
  labels:
    app: crier
spec:
  selector:
    matchLabels:
      app: crier
  template:
    metadata:
      labels:
        app: crier
    spec:
      containers:
      - name: crier
        image: gcr.io/k8s-prow/crier:v20200205-656133e91
        args:
        - --slack-workers=1
        - --slack-token-file=/etc/slack/token
        - --config-path=/etc/config/config.yaml
        - --dry-run=false
        volumeMounts:
        - mountPath: /etc/config
          name: config
          readOnly: true
        - name: slack
          mountPath: /etc/slack
          readOnly: true
      volumes:
      - name: slack
        secret:
          secretName: slack-token
      - name: config
        configMap:
          name: config
```

Additionally, in order for it to work with Prow you must add the following to your `config.yaml`:

> **NOTE:** `slack_reporter_configs` is a map of `org`, `org/repo`, or `*` (i.e. catch-all wildcard) to a set of slack reporter configs.

```yaml
slack_reporter_configs:

  # Wildcard (i.e. catch-all) slack config
  "*":
    # default: None
    job_types_to_report:
      - presubmit
      - postsubmit
    # default: None
    job_states_to_report:
      - failure
      - error
    # required
    channel: my-slack-channel
    # The template shown below is the default
    report_template: "Job {{.Spec.Job}} of type {{.Spec.Type}} ended with state {{.Status.State}}. <{{.Status.URL}}|View logs>"

  # "org/repo" slack config
  istio/proxy:
    job_types_to_report:
      - presubmit
    job_states_to_report:
      - error
    channel: istio-proxy-channel

  # "org" slack config
  istio:
    job_types_to_report:
      - periodic
    job_states_to_report:
      - failure
    channel: istio-channel
```

The `channel`, `job_states_to_report` and `report_template` can be overridden at the ProwJob level via the `reporter_config.slack` field:

```yaml
postsubmits:
  some-org/some-repo:
    - name: example-job
      decorate: true
      reporter_config:
        slack:
          channel: 'override-channel-name'
          job_states_to_report:
            - success
          report_template: "Overridden template for job {{.Spec.Job}}"
      spec:
        containers:
          - image: alpine
            command:
              - echo
```

To silence notifications at the ProwJob level you can pass an empty slice to `reporter_config.slack.job_states_to_report`:

```yaml
postsubmits:
  some-org/some-repo:
    - name: example-job
      decorate: true
      reporter_config:
        slack:
          job_states_to_report: []
      spec:
        containers:
          - image: alpine
            command:
              - echo
```

## Implementation details

Crier supports multiple reporters, each reporter will become a crier controller. Controllers
will get prowjob change notifications from a [shared informer](https://github.com/kubernetes/client-go/blob/master/tools/cache/shared_informer.go), and you can specify `--num-workers` to change parallelism.

If you are interested in how client-go works under the hood, the details are explained
[in this doc](https://github.com/kubernetes/sample-controller/blob/master/docs/controller-client-go.md)

## Adding a new reporter

Each crier controller takes in a reporter.

Each reporter will implement the following interface:

```go
type reportClient interface {
 Report(pj *v1.ProwJob) error
 GetName() string
 ShouldReport(pj *v1.ProwJob) bool
}
```

`GetName` will return the name of your reporter, the name will be used as a key when we store previous
reported state for each prowjob.

`ShouldReport` will return if a prowjob should be handled by current reporter.

`Report` is the actual report logic happens. Return `nil` means report is successful, and the reported
state will be saved in the prowjob. Return an actual error if report fails, crier will re-add the prowjob
key to the shared cache and retry up to 5 times.

You can add a reporter that implements the above interface, and add a flag to turn it on/off in crier.

## Migration from plank for github report

Both plank and crier will call into the [github report lib](https://github.com/kubernetes/test-infra/tree/de3775a7480fe0a724baacf24a87cbf058cd9fd5/prow/github/report) when a prowjob needs to be reported,
so as a user you only want to make one of them to report :-)

To disable GitHub reporting in Plank, add the `--skip-report=true` flag to the Plank [deployment](https://github.com/kubernetes/test-infra/blob/de3775a7480fe0a724baacf24a87cbf058cd9fd5/prow/cluster/plank_deployment.yaml#L45).

Before migrating, be sure plank is setting the [PrevReportStates field](https://github.com/kubernetes-sigs/prow/blob/db89760fea406dd2813e331c3d52b53b5bcbd140/pkg/apis/prowjobs/v1/types.go#L1078)
by describing a finished presubmit prowjob. Plank started to set this field after commit [2118178](https://github.com/kubernetes/test-infra/pull/10975/commits/211817826fc3c4f3315a02e46f3d6aa35573d22f), if not, you want to upgrade your plank to a version includes this commit before moving forward.

you can check this entry by:

```sh
$ kubectl get prowjobs -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.prev_report_states.github-reporter}{"\n"}'
...
fafec9e1-3af2-11e9-ad1a-0a580a6c0d12	failure
fb027a97-3af2-11e9-ad1a-0a580a6c0d12	success
fb0499d3-3af2-11e9-ad1a-0a580a6c0d12	failure
fb05935f-3b2b-11e9-ad1a-0a580a6c0d12	success
fb05e1f1-3af2-11e9-ad1a-0a580a6c0d12	error
fb06c55c-3af2-11e9-ad1a-0a580a6c0d12	success
fb09e7d8-3abb-11e9-816a-0a580a6c0f7f	success


```

You want to add a crier deployment, similar to ours [config/prow/cluster/crier_deployment.yaml](https://github.com/kubernetes/test-infra/blob/de3775a7480fe0a724baacf24a87cbf058cd9fd5/prow/cluster/crier_deployment.yaml),
flags need to be specified:

- point `config-path` and `--job-config-path` to your prow config and job configs accordingly.
- Set `--github-worker` to be number of parallel github reporting threads you need
- Point `--github-endpoint` to ghproxy, if you have set that for plank
- Bind github oauth token as a secret and set `--github-token-path` if you've have that set for plank.

In your plank deployment, you can

- Remove the `--github-endpoint` flags
- Remove the github oauth secret, and `--github-token-path` flag if set
- Flip on `--skip-report`, so plank will skip the reporting logic

Both change should be deployed at the same time, if have an order preference, deploy crier first since report twice should just be a no-op.

We will send out an announcement when we cleaning up the report dependency from plank in later 2019.
