# Tenstorrent device plugin for Kubernetes

## Introduction

This is a [Kubernetes][k8s] [device plugin][dp] implementation that enables the registration of Tenstorrent AI accelerators in a container cluster for compute workloads.

With [tt-kmd][tt-kmd] installed and this plugin deployed, you will be able to request Tenstorrent devices in your pod specs:

| Resource | Cards |
|-|-|
| `tenstorrent.com/blackhole` | p100a, p150a/b/c, p300a/b/c |
| `tenstorrent.com/n150` | N150 |
| `tenstorrent.com/n300` | N300, N300L, N300S |
| `tenstorrent.com/grayskull` | e75, e150 |

## Prerequisites

* [tt-kmd][tt-kmd] kernel module loaded on each node
* `/dev/tenstorrent/N` device nodes present
* 1G hugepages (`/dev/hugepages-1G`) recommended

## Deployment

The device plugin needs to run on all nodes equipped with Tenstorrent devices. Deploy as a [DaemonSet][ds]:

```bash
kubectl apply -f deploy/daemonset.yaml
```

### Helm Chart

```bash
helm install tt-device-plugin helm/tt-device-plugin/ -n kube-system
```

## Example workload

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: tt-workload
spec:
  containers:
    - name: app
      image: ghcr.io/tenstorrent/tt-metal/tt-metalium/ubuntu-22.04-dev-amd64:latest
      resources:
        limits:
          tenstorrent.com/blackhole: 1
```

Each allocated container receives:
* `/dev/tenstorrent/N` device node (rw)
* `/dev/hugepages-1G` mount (rw, if present on host)
* `/sys` mount (ro)
* `TT_VISIBLE_DEVICES` environment variable

## Building

```bash
go build -o tt-device-plugin ./cmd/tt-device-plugin/
```

### Container image

```bash
docker build -t tt-device-plugin .
```

[k8s]: https://kubernetes.io
[dp]: https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/
[ds]: https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/
[tt-kmd]: https://github.com/tenstorrent/tt-kmd
