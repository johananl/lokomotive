# Upgrading bootstrap Kubelet

## Contents

- [Introduction](#introduction)
- [Steps](#steps)
  - [Drain the node](#drain-the-node)
  - [Find out the IP and SSH](#find-out-the-ip-and-ssh)
  - [On the node](#on-the-node)
- [Caveats](#caveats)

## Introduction

[Kubelet](https://kubernetes.io/docs/reference/command-line-tools-reference/kubelet/) is a daemon that runs on every node and it is responsible for managing Pods on the node.

Lokomotive cluster runs two different sets of Kubelet processes. Initially, **bootstrap** Kubelet configured on the node as a `rkt` pod joins the cluster, and then Kubelet pod managed using DaemonSet(self-hosted Kubelet) takes over the bootstrap Kubelet. Self-hosted Kubelet allows seamless updates between Kubernetes patch version for nodes and also to tune nodes configuration using tools like `kubectl`.

At the time of writing `lokoctl` cannot update bootstrap Kubelet, so this document explains how to perform this operation manually.

## Steps

Perform the following steps on each node, one node at a time.

### Step 1: Drain the node

> **Caution:** If you are using a local directory as a storage for a workload, it will be disturbed by this operation. To avoid this move the workload to another node and let the application replicate the data. If the application does not support data replication across instances, then expect downtime.

```bash
kubectl drain --ignore-daemonsets <node name>
```

### Step 2: Find out the IP and SSH

Find the IP of the node by visiting the cloud provider dashboard.

```bash
ssh core@<IP Address>
```

### Step 3: Upgrade Kubelet on node

Run the following commands:

> **NOTE**: Before proceeding to other commands, set the `latest_kube` variable to the latest Kubernetes version.

```bash
export latest_kube=<latest kubernetes version e.g. v1.18.0>
sudo sed -i "s|$(grep -i kubelet_image_tag /etc/kubernetes/kubelet.env)|KUBELET_IMAGE_TAG=${latest_kube}|g" /etc/kubernetes/kubelet.env
sudo systemctl restart kubelet
sudo journalctl -fu kubelet
```

Check the logs carefully. If `kubelet` fails to restart and instructs to do something (e.g. deleting some file), follow the instructions and reboot the node:

```bash
sudo reboot
```

### Step 4: Verify

Once the node reboots and Kubelet rejoins the cluster output of following command will show new version across the node name:

```bash
kubectl get nodes
```

## Caveats

- When upgrading Kubelet on nodes which are running Rook Ceph, verify that the Ceph cluster is in the **`HEALTH_OK`** state. If it is in any other state, **do not proceed with the upgrade** as doing so could lead to data loss. When the cluster is in the `HEALTH_OK` state it can tolerate the downtime caused by rebooting nodes.
