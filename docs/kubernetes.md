# Kubernetes: Phase 9

This is a local, single-node learning deployment using Docker Desktop's Kubernetes
cluster. Keep Docker Desktop running in Linux-container mode and enable Kubernetes.
Run the commands below from the repository root in PowerShell. No registry account
or cloud subscription is needed for the local image.

## Components and concepts

| File in `deploy/kubernetes/` | Purpose |
| --- | --- |
| `namespace.yaml` | Groups the project's Kubernetes resources |
| `configmap.yaml` | Supplies the application's non-secret environment settings |
| `pvc.yaml` | Requests 1 GiB of persistent storage using the default StorageClass |
| `deployment.yaml` | Declares the desired Pod, image, probes, resources, and volumes |
| `service.yaml` | Selects ready Pods by label and exposes port 8080 inside the cluster |

A Deployment manages a ReplicaSet, which keeps the requested number of Pods
running. A Pod can be replaced; its container filesystem is disposable. The PVC
has a separate lifecycle and mounts the database directory into replacement Pods.
Docker Compose volumes and Kubernetes volumes are separate; existing Compose
data is not imported automatically.

Keep `replicas: 1`. Every StatusPulse process starts its own monitoring worker.
`Recreate` stops the old Pod before starting its replacement during Deployment
updates, allowing brief downtime while avoiding overlapping workers during those
updates. `ReadWriteOnce` means one node, not one Pod: it is not a singleton lock.
Do not scale this deployment or force-delete a running Pod. This setup does not
provide high availability or distributed fencing.

## Build and deploy

Check that the node is Ready and a default StorageClass exists:

```powershell
kubectl --context docker-desktop get nodes
kubectl --context docker-desktop get storageclass
docker build -t statuspulse:phase10 .

kubectl --context docker-desktop apply -f deploy/kubernetes/namespace.yaml
kubectl --context docker-desktop apply --dry-run=server -f deploy/kubernetes/
kubectl --context docker-desktop apply -f deploy/kubernetes/
kubectl --context docker-desktop -n statuspulse rollout status deployment/statuspulse --timeout=180s
kubectl --context docker-desktop -n statuspulse get pods,services,pvc
```

The local image must be available to the Kubernetes node. Docker Desktop's kubeadm
cluster can use the image built above. For a separate kind cluster, load it with
`kind load docker-image statuspulse:phase10 --name YOUR_CLUSTER` and use that
cluster's context. An image present only in the host Docker cache is not sufficient
for every cluster provisioner. A published registry image is another option below.

Open a terminal and keep port forwarding running:

```powershell
kubectl --context docker-desktop -n statuspulse port-forward service/statuspulse 8080:8080
```

Open <http://localhost:8080/>. If port 8080 is occupied, use `8081:8080` and open
port 8081 instead. Ctrl+C stops forwarding, not the deployment. Forwarding binds
to localhost by default. The Service is ClusterIP; there is no public ingress.
Use this unauthenticated application on a trusted local cluster.

## Probes, permissions, and resources

Kubernetes uses the probes in the Pod specification, not the Dockerfile HEALTHCHECK.
The HTTP startup probe allows about 60 seconds for startup. Readiness requests
`/readyz`, exercising HTTP and a bounded SQLite read; failure removes the Pod from
Service traffic. The HTTP liveness probe requests `/livez` without accessing storage.
It is deliberately limited: it cannot detect every worker failure. Monitored
endpoints reporting DOWN do not fail these probes. Dedicated health endpoints
are provided by Phase 10; see [observability](observability.md).

The process runs as UID/GID 10001, drops capabilities, uses a read-only root
filesystem, and gets no Kubernetes API token. `/data` is persistent; `/tmp` is
temporary. `fsGroup` requests group access to supported volumes; storage drivers
differ, so inspect volume permissions if startup reports permission denied.

CPU/memory requests reserve scheduling capacity; limits bound consumption. The
initial values (100m/64Mi requests, 500m/256Mi limits) are starting points for a small
demo. Tune them using measurements. The application retains history indefinitely;
watch disk usage. Local hostpath storage is tied to the desktop node, is not a
backup, and may not enforce the requested capacity as a filesystem quota.

## Verify monitoring and persistence

With forwarding running, use another terminal:

```powershell
$body = @{ name = 'Kubernetes demo'; url = 'http://127.0.0.1:8080/api/services' } | ConvertTo-Json
$service = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/services -ContentType application/json -Body $body
$service.id
```

This URL is loopback inside the Pod, providing a deterministic local monitoring
target. Wait up to one check interval (default one minute), then inspect history:

```powershell
Invoke-RestMethod "http://localhost:8080/api/services/$($service.id)/checks"
kubectl --context docker-desktop -n statuspulse rollout restart deployment/statuspulse
kubectl --context docker-desktop -n statuspulse rollout status deployment/statuspulse --timeout=180s
```

Restart port forwarding after the Pod changes. Fetch the service and history again;
the same service ID and pre-restart check IDs should remain. Confirm the new Pod
is Ready, with no restart loop, and the claim is Bound. Delete the demo when done:

```powershell
Invoke-RestMethod -Method Delete -Uri "http://localhost:8080/api/services/$($service.id)"
```

## Change configuration or image

Edit `configmap.yaml`, apply it, then restart the Deployment. Environment variables
are read when a container starts; updating a ConfigMap does not refresh running
processes. Keep port 8080 aligned with Service/probes and the database path under
the mounted `/data` directory.

```powershell
kubectl --context docker-desktop apply -f deploy/kubernetes/configmap.yaml
kubectl --context docker-desktop -n statuspulse rollout restart deployment/statuspulse
kubectl --context docker-desktop -n statuspulse rollout status deployment/statuspulse --timeout=180s
```

For code updates, build a new image tag (for example `statuspulse:phase10-2`), change
`image:` in `deployment.yaml`, and apply that file. Unique tags avoid stale local
image caches. To deploy a Phase 8 release, replace `image:` with an actually
published `ghcr.io/sanjitt-k/status-pulse:vX.Y.Z` or an immutable digest. Private
packages require an `imagePullSecrets` reference and a registry Secret in this
namespace; never commit registry credentials. Public packages need no pull secret.

`kubectl --context docker-desktop -n statuspulse rollout undo deployment/statuspulse`
can restore a previous Pod template. It does not restore database contents,
reverse migrations, or roll back ConfigMap edits. Check database compatibility
before rolling an application image back, and keep the manifest aligned afterward.
Deployment remains manual; CI has no cluster credentials.

## Troubleshooting and stopping

```powershell
kubectl --context docker-desktop -n statuspulse get pods,pvc
kubectl --context docker-desktop -n statuspulse describe deployment statuspulse
kubectl --context docker-desktop -n statuspulse describe pods -l app=statuspulse
kubectl --context docker-desktop -n statuspulse logs deployment/statuspulse --tail=100
kubectl --context docker-desktop -n statuspulse get events --sort-by=.metadata.creationTimestamp
```

- `ImagePullBackOff`: check the image tag, node image cache, or registry access.
- PVC `Pending`: inspect the default StorageClass and provisioning events.
- `CrashLoopBackOff`: inspect logs (including `kubectl logs POD --previous`),
  configuration, write permissions, and memory limits.
- `Running` but not Ready: inspect readiness events and the API/database error.
- Forwarding disconnected after restart: start the port-forward command again.

Stop the application while retaining its database:

```powershell
kubectl --context docker-desktop -n statuspulse scale deployment/statuspulse --replicas=0
```

Resume with `--replicas=1` or reapply the Deployment manifest. For a backup, scale
to zero and wait for Pods to terminate before using storage tooling to copy SQLite.
Do not delete the PVC, namespace, or cluster to stop the app. In particular,
`kubectl delete -f deploy/kubernetes/` removes the claim and namespace and can
permanently delete the database with the default Delete reclaim policy.

## Verification in this workspace

Verified on Docker Desktop Kubernetes v1.36.1: the image built successfully,
server-side manifest validation passed, the Pod became Ready as UID 10001, and
the 1 GiB PVC became Bound. The dashboard responded over port forwarding and
the worker recorded an HTTP 200/UP check against a local test endpoint. After
`rollout restart`, a replacement Pod retained the original service and check
ID/timestamp, and monitoring continued. The disposable test service was removed.
The deployment and PVC remain available; temporary verification forwarding was
stopped. No application Go code or CI deployment credentials were added.

## Phase 9 completion checklist

- [ ] Explain Deployment, Pod, labels/selectors, Service, ConfigMap, and PVC.
- [ ] Build an image and deploy successfully; Pod Ready and PVC Bound.
- [ ] Reach the dashboard through port forwarding and register a service.
- [ ] Confirm monitoring writes checks inside the Pod.
- [ ] Restart the Deployment and verify service and history survive.
- [ ] Explain single-replica/Recreate tradeoffs and expected update downtime.
- [ ] Find logs/events and diagnose an unavailable image or failed probe.
- [ ] Know how to stop the app without deleting data.

References: [Deployments](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/),
[persistent volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/),
[probes](https://kubernetes.io/docs/concepts/workloads/pods/probes/), and
[security contexts](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/).
