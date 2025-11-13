---
title: "tackle"
weight: 10
description: >
  
---

Prow's `tackle` utility walks you through deploying a new instance of prow
in a couple of minutes, try it out!

### Installing tackle

Tackle at this point in time needs to be built from source. The following
steps will walk you through the process:

1. Clone the `test-infra` repository:

```shell
git clone git@github.com:kubernetes/test-infra.git
```

2. Build `tackle` (This requires a working go installation on your system)

```shell
cd test-infra/prow/cmd/tackle && go build -o tackle
```

3. Optionally move `tackle` to your `$PATH`

```shell
sudo mv tackle /usr/sbin/tackle
```

### Deploying prow

**Note**: Creating a cluster using the `tackle` utility assumes you
have the `gcloud` application in your `$PATH` and are logged in. If you are
doing this on another cloud skip to the **Manual deployment** below.

Installing Prow using `tackle` will help you through the following steps:

* Choosing a kubectl context (or creating a cluster on GCP / getting its credentials if necessary)
* Deploying prow into that cluster
* Configuring GitHub to send prow webhooks for your repos. This is where you'll provide the absolute `/path/to/github/token`

To install prow run the following and follow the on-screen instructions:

1. Run `tackle`:

```sh
tackle
```

2. Once your cluster is created, you'll get a prompt to apply a `starter.yaml`. Before you do that open another terminal and apply the prow CRDs using:

```sh
kubectl apply --server-side=true -f https://raw.githubusercontent.com/kubernetes/test-infra/main/config/prow/cluster/prowjob-crd/prowjob_customresourcedefinition.yaml
```

3. After that specify the `starter.yaml` you want to use (please make sure to replace the values mentioned [here](/docs/getting-started-deploy/#update-the-sample-manifest)). Once that is done some pods still won't be in the `Running` state because we haven't created the secret containing the credentials needed for our GCS bucket. To do that follow the steps in [Configure a GCS bucket](/docs/getting-started-deploy/#configure-a-gcs-bucket).

4. Once that is done, `tackle` should show you the URL where you can access the prow dashboard. To use it with your repositories head over to the settings of the GitHub app you created and there under webhook secret, supply the HMAC token you specified in the [`starter.yaml`](https://github.com/kubernetes-sigs/prow/blob/main/config/prow/cluster/starter/starter-gcs.yaml#L51).

5. Once that is done, install the GitHub app on the repositories you want (this is only needed if you ran `tackle` with the `--skip-github` flag) and you should now be able to use Prow :)

See the [Next Steps](/docs/getting-started-deploy/#next-steps) section after running this utility.
