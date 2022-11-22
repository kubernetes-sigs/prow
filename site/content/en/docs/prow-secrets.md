---
title: "Prow Secrets Management"
weight: 140
description: >
  
---

Secrets in prow service/build clusters are managed with [Kubernetes External
Secrets](https://github.com/external-secrets/kubernetes-external-secrets), which is responsible for one-way syncing secret values from major
secret manager providers such as GCP, Azure, and AWS secret managers into
kubernetes clusters, based on `ExternalSecret` custom resource defined in
cluster (As shown in example below).

_Note: the instructions below are only for GCP secret manager, for
authenticating with other providers please refer to
https://github.com/external-secrets/kubernetes-external-secrets#backends_

## Set Up (Prow maintainers)

This is performed by prow service/build clusters maintainer.

1. In the cluster that the secrets are synced to, enable workload identity by
   following [`workload-identity`](https://github.com/kubernetes/test-infra/tree/master/workload-identity/README.md).
1. Deploy `kubernetes-external-secrets_crd.yaml`,
   `kubernetes-external-secrets_deployment.yaml`,
   `kubernetes-external-secrets_rbac.yaml`,
   and  `kubernetes-external-secrets_service.yaml` under
   [`config/prow/cluster`](https://github.com/kubernetes/test-infra/tree/master/config/prow/cluster). The deployment file assumes
   using the same service account name as used in step #1
2. [Optional but recommended] Create postsubmit deploy job for managing the
   deployment, for example
   [post-test-infra-deploy-prow](https://github.com/kubernetes/test-infra/blob/8716584a87c11b3ac4596d4199a00eaa4ce659a0/config/jobs/kubernetes/test-infra/test-infra-trusted.yaml#L95).

## Usage (Prow clients)

This is performed by prow serving/build cluster clients. Note that the GCP
project mentioned here doesn't have to, and normally is not the same GCP project
where the prow service/build clusters are located.

1. In the GCP project that stores secrets with google secret manager, grant the
   `roles/secretmanager.viewer` and `roles/secretmanager.secretAccessor`
   permission to the GCP service account used above, by running:
   ```
   gcloud beta secrets add-iam-policy-binding <my-gsm-secret-name> --member="serviceAccount:<same-service-account-for-workload-identity>" --role=<role> --project=<my-gsm-secret-project>
   ```
   The above command ensures that the service account used by prow can only
   access the secret name `<my-gsm-secret-name>` in the GCP project owned by
   clients. The service account used for prow.k8s.io (aka `test-infra-trusted`
   build cluster) is defined in
   [`trusted_serviceaccounts.yaml`](https://github.com/kubernetes/test-infra/blob/1b2153ebe2809727a45c5b930647b2a3609dd7e7/config/prow/cluster/trusted_serviceaccounts.yaml#L46),
   and the secrets are defined in
   [`kubernetes_external_secrets.yaml`](https://github.com/kubernetes/test-infra/blob/master/config/prow/cluster/kubernetes_external_secrets.yaml).
   The service account used for `k8s-prow-builds` cluster(aka the default build
   cluster) is defined in
   [`build_serviceaccounts.yaml`](https://github.com/kubernetes/test-infra/blob/422fd7239bd65aba020adca54948df292c60c10a/config/prow/cluster/build_serviceaccounts.yaml#L43),
   and the secrets are defined in
   [`build_kubernetes-external-secrets_customresource.yaml`](https://github.com/kubernetes/test-infra/blob/master/config/prow/cluster/build/build_kubernetes-external-secrets_customresource.yaml).

2. Create secret in google secret manager
3. Create kubernetes external secrets custom resource by:
   ```
   apiVersion: kubernetes-client.io/v1
   kind: ExternalSecret
   metadata:
     name: <my-precious-secret-kes-name>    # name of the k8s external secret and the k8s secret
     namespace:  <ns-where-secret-is-used>
   spec:
     backendType: gcpSecretsManager
     projectId: <my-gsm-secret-project>
     data:
     - key: <my-gsm-secret-name>     # name of the GCP secret
       name: <my-kubernetes-secret-name>   # key name in the k8s secret
       version: latest    # version of the GCP secret
       # Property to extract if secret in backend is a JSON object,
       # remove this line if using the GCP secret value straight
       property: value
   ```

Within 10 seconds (determined by `POLLER_INTERVAL_MILLISECONDS` envvar on deployment), a secret will be created automatically:
```
apiVersion: v1
kind: Secret
metadata:
  name: <my-precious-secret-kes-name>
  namespace:  <ns-where-secret-is-used>
data:
  <my-kubernetes-secret-name>: <value_read_from_gsm>
```

The `Secret` will be updated automatically when the secret value in gsm changed
or the `ExternalSecret` is changed. when `ExternalSecret` CR is deleted from the
cluster, the secret will be also be deleted by kubernetes external secret.
(Note: deleting the `ExternelSecret` CR config from source control doesn't
result in deletion of corresponding `ExternalSecret` CR from the cluster as the
postsubmit action only does `kubectl apply`).
