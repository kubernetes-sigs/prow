---
title: "Exporter"
weight: 10
description: >
  
---

The prow-exporter exposes metrics about prow jobs while the
metrics are not directly related to a specific prow-component.

## Metrics

| Metric name          | Metric type | Labels/tags                                                                                                                                                                                           |
|----------------------|-------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| prow_job_labels      | Gauge       | `job_name`=&lt;prow_job-name&gt; <br> `job_namespace`=&lt;prow_job-namespace&gt; <br> `job_agent`=&lt;prow_job-agent&gt; <br> `label_PROW_JOB_LABEL_KEY`=&lt;PROW_JOB_LABEL_VALUE&gt;                 |
| prow_job_annotations | Gauge       | `job_name`=&lt;prow_job-name&gt; <br> `job_namespace`=&lt;prow_job-namespace&gt; <br> `job_agent`=&lt;prow_job-agent&gt; <br> `annotation_PROW_JOB_ANNOTATION_KEY`=&lt;PROW_JOB_ANNOTATION_VALUE&gt;  |
| prow_job_runtime_seconds     | Histogram     | `job_name`=&lt;prow_job-name&gt; <br> `job_namespace`=&lt;prow_job-namespace&gt; <br> `type`=&lt;prow_job-type&gt; <br> `last_state`=&lt;last-state&gt; <br> `state`=&lt;state&gt; <br> `org`=&lt;org&gt; <br> `repo`=&lt;repo&gt; <br> `base_ref`=&lt;base_ref&gt; <br>  |

For example, the metric `prow_job_labels` is similar to `kube_pod_labels` defined
in [kubernetes/kube-state-metrics](https://github.com/kubernetes/kube-state-metrics/blob/master/docs/pod-metrics.md).
A typical usage of `prow_job_labels` is to [join](https://github.com/kubernetes/kube-state-metrics/tree/master/docs#join-metrics)
it with other metrics using a [Prometheus matching operator](https://prometheus.io/docs/prometheus/latest/querying/operators/#vector-matching).

Note that `job_name` is [`.spec.job`](https://github.com/kubernetes-sigs/prow/blob/7013691e3f35afd02f300c04ccd06ebed66a785f/prow/apis/prowjobs/v1/types.go#L158)
instead of `.metadata.name` as taken in `kube_pod_labels`.
The gauge value is always `1` because we have another metric [`prowjobs`](/docs/metrics/)
for the number jobs by name. The metric here shows only the existence of such a job with the label set in the cluster.
