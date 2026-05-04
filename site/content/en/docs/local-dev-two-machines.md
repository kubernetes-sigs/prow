---
title: "Local Development with a Remote KinD Cluster"
weight: 76
description: >
  How to deploy the integration Prow KinD cluster to a remote server machine connected to a dev machine for auto-updating with tilt.
---

> **NOTE**
>
> Keep in mind that while this doc comes with commands it is more so meant as a guide than a script. The headings and descriptions show what needs to done and the commands provided are some ways of doing them but ways are available and possibly required based on your systems.

This guide assumes the server machine is a Linux machine that is Debian based but other operating systems should be fine as long as you swap a few commands. The client instruction are very minimal due to the nature of the work required there.

This guides use Tailscale for a trusted machine to machine tunnel. A different VPN should be fine as well.

> **WARNING**
>
> This guide sets the KinD APIServerAddress to one that can be reached remotely and likewise sets the Docker Daemon to listen on an address that can be reached remotely. Ensure the address you bind this behavior to is only available from your dev machine.

## 1 - Install Dependencies
### Client
- `kubectl`
- `docker`
- `tilt`
### Server
Install go with a high enough version. You can install any Go ≥ 1.21 as a bootstrap, then let the module system handle the rest:
```bash
curl -OL https://go.dev/dl/go1.22.12.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.22.12.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
go version
```

Install Docker.
https://docs.docker.com/engine/install/
```bash
# On the server (Ubuntu/Debian)
sudo apt-get update
sudo apt-get install -y docker.io
sudo usermod -aG docker $USER
```

Install KinD.
```bash
curl -Lo ./kind https://kind.sigs.k8s.io/dl/latest/kind-linux-amd64
chmod +x ./kind && sudo mv ./kind /usr/local/bin/kind
```

Install tilt.
```bash
curl -fsSL https://raw.githubusercontent.com/tilt-dev/tilt/master/scripts/install.sh | bash
```

## 2 - Set Environment Variables
### Client
Ensure the following environment variable are set in your shell.
```bash
export DOCKER_HOST="tcp://100.x.y.z:2375"
export KUBECONFIG=$PWD/kind-prow-integration-kubeconfig.yaml
```

### Server
```bash
export KIND_CONFIG=~/kind_config.yaml
```

## 3 - Networking Foundation
### Tailscale
Make sure your Tailscale ACLs allow the following traffic from your dev machine to your server machine.
- `tcp:22` - for copying files over SSH
- `tcp:2375` - for the Docker Daemon
- `tcp:6443` - for kubectl
- `tcp:80` - for the Deck interface for Prow

### Server
Setup `ufw` rules to ensure incoming traffic must come from the Tailscale interface.
https://tailscale.com/docs/how-to/secure-ubuntu-server-with-ufw
```bash
sudo ufw enable
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow in on tailscale0
```

## 4 - Expose the Docker Daemon
### Server
Edit the Docker daemon config and systemd service to listen on a remote IP address.
https://docs.docker.com/engine/daemon/remote-access/

```bash
sudo nano /etc/docker/daemon.json
```
```json
{
  "hosts": ["unix:///var/run/docker.sock", "tcp://100.x.y.z:2375"]
}
```

```bash
sudo systemctl edit docker.service
```
```ini
[Service]
ExecStart=
ExecStart=/usr/bin/dockerd
```

```bash
sudo systemctl daemon-reload
sudo systemctl restart docker
```

## 5 - Create the Cluster
### Server
First create the KIND_CONFIG for the cluster.
```bash
nano ~/kind_config.yaml
```

You may want to change the host var values.
```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  # WARNING: BE CAREFUL EXPOSE THE API SERVER ADDRESS
  # This case specifically goes through Tailscale which
  # I control and the server firewall (ufw) is set to only
  # allow incoming traffic over the Tailscale interface.
  apiServerAddress: "100.x.y.z" # server's Tailscale IP
  apiServerPort: 6443
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
  - |
    kind: ClusterConfiguration
    apiServer:
      certSANs:
        - "100.x.y.z"
        - "debian12-dev.pleco-koi.ts.net"
        - "localhost"
        - "127.0.0.1"
  extraPortMappings:
  - containerPort: 80
    hostPort: 80 # flexible based on your machine
    protocol: TCP
  - containerPort: 443
    hostPort: 443 # flexible based on your machine
    protocol: TCP
  - containerPort: 32000
    hostPort: 32000 # flexible based on your machine
    protocol: TCP
  - containerPort: 30303 # flexible based on your machine
    hostPort: 30303 # flexible based on your machine
    protocol: TCP
```

Then clone the Prow repository and use the make command you want, either `make dev` for just the core component or `make dev-full` for core + optional components for a more production like setup.

```
git clone https://github.com/kubernetes-sigs/prow.git
make dev
```

## 6 - Get the KUBECONFIG
### Server
Get the KUBECONFIG from your created KinD cluster.
```bash
kind get kubeconfig --name kind-prow-integration > ~/kind-prow-integration-kubeconfig.yaml
```

### Client
Copy the retrieved config to your dev machine.
```bash
scp root@100.x.y.z:~/kind-prow-integration-kubeconfig.yaml kind-prow-integration-kubeconfig.yaml
```

## 7 - Test it all works.
### Client
```bash
docker ps
kubectl get nodes
```

Open the Prow UI in the browser.
```
http://100.x.y.z:80
```
