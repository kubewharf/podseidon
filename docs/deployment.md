Deploying Podseidon
===

## Installation

### Configuration
Download [values.yaml](chart/values.yaml) and configure the settings (the default settings MAY NOT work!).
For multi-cluster setup, a separate values.yaml is required for each cluster.

#### `release`
- For single-cluster setup, leave as default.
- For multi-cluster setup, set `core: false` and `worker: true` for non-core clusters.
  For the core cluster, set `core: true`,
  and set `worker` to `true` or `false` depending on
  whether pods in the core cluster need to be protected.
  Also set `workerCellId` to a *unique* cluster name if `worker` is `true`.

#### `aggregator` (only relevant if `release.worker = true`)
- `coreCluster`:
  - For single-cluster setup, leave as default.
  - Provide a literal kubeconfig or reference an existing secret to the core cluster
    if the current release is `release.worker = true` and `release.core = false`.

#### `webhook`
- `host`:
  - For single-cluster setup, leave empty.
  - For multi-cluster and out-of-cluster setups, see the "Webhook service discovery" section.

- `tls`
  - Set `custom` to `true`, and generate a new certificate for the webhook.
    **This step is required for most setups** unless `webhook.host` is set to a reverse proxy
    trusted by the system CA root of kube-apiserver.
    To generate a new certificate for single-cluster setups, use one of the following methods:

##### `openssl`

- Change DAYS to number of days until certificate expiry.
- Change YOUR_NAMESPACE to the namespace for the Podseidon Helm release.

```shell
openssl req -x509 -newkey rsa:1024 -keyout webhook.key -out webhook.cert -sha256 -nodes -subj '/CN=CN' \
    -days DAYS \
    -addext 'subjectAltName=DNS:*.YOUR_NAMESPACE.svc'
```

Copy the contents of `webhook.key` and `webhook.cert` into the corresponding fields in values.yaml.

##### `cfssl`

1. Download `cfssl*.json` from `test/assets` of this repository.
2. Replace all occurrences of `podseidon-webhook` to `*.YOUR_NAMESPACE.svc`,
   where `YOUR_NAMESPACE` is the namespace for the Podseidon Helm release.
3. Run the following commands:

```shell
cfssl gencert -initca cfssl-ca-csr.json | cfssljson -bare root
cfssl gencert -ca=root.pem -ca-key=root-key.pem \
    -config=cfssl-config.json -profile=webhook \
    cfssl-webhook-csr.json | cfssljson -bare webhook
```

#### Other settings

Other settings are usable by default and self-explanatory.

### Installing the chart

Install the chart with the configured values for each cluster.

```shell
helm install podseidon oci://ghcr.io/kubewharf/podseidon-chart --values values.yaml
```

> [!TIP]
> For multi-cluster setups with multiple core clusters,
> treat each core cluster as a separate Podseidon installation.
> It is possible to install the Podseidon chart multiple times using different Helm release names.
> Each installation can be configured with a different `coreCluster`.
> However, note the possible performance impact since
> each installation runs a separate validation webhook,
> which kube-apiserver calls serially and may greatly increase request latency.

### Out-of-cluster deployment

There are two possible approaches for out-of-cluster deployment of the Podseidon components.

The first method is to use a virtual kubelet that
proxies the Podseidon controllers pods to a separate control plane (Kube-on-Kube) cluster.
To use this approach, simply set the `nodeSelector` under the relevant components.

The second method is to avoid creating the Deployments in the Helm chart
and manually create the Deployments in a separate control plane cluster.
This option is not natively supported by the Helm chart,
which is published for general users and is too complicated to include such options.
However, it is possible to modify the helm chart to generate non-workload templates only
by changing the main entrypoints to include the template
`podseidon.boilerplate.aux.obj` instead of `podseidon.boilerplate.entrypoint.obj`,
and separately generate the appropriate command-line options passed to the controller binaries
by invoking the template `podseidon.boilerplate.args.yaml-array`.
Note that `<component>.yaml` only invokes the templates in `_<component>.yaml`,
and the latter file only defines templates without invoking anything at the top level,
so it is possible to import all files starting with `_` as library templates
and build your own Helm chart.

In both approaches, the `coreCluster` and `workerCluster` in the corresponding components
need to be configured accordingly to avoid using the `inCluster` configuration
and connecting to the control plane cluster incorrectly.
The `impersonate` option may be useful to
authenticate as the service accounts created by the Podseidon chart,
which declares more granular access to cluster resources,
while using a user account with higher access from the `literal`/`fromSecret` kubeconfigs.

### Webhook service discovery

Podseidon webhook instances are only deployed in the release where `release.core = true`.
For releases where `release.worker = true` and `release.core = false`,
set `webhook.host` to a URL that resolves to the webhook Service created in the host cluster.
The provision of such URL is subject to the multi-cluster service discovery solution used.

## Canary release procedure

To minimize disruption to existing operations,
the following options are available for seamless adoption:

- Set `webhook.failurePolicy` to `Ignore` initially to check if the webhook is actually reachable.
- Set `webhook.dryRun` to `true` to obtain metrics on rejection rate without actually blocking pod deletion.
- Do not select everything under `generator.protectedSelector` initially.
  Only label specific canary workloads with the selector
  to observe any disruption to operations on these workloads before expanding to all other workloads.

## Maintenance

### Monitoring

Each container exposes Prometheus metrics over HTTP at port 9090.
The following metrics have been found relevant.

All time-related metrics are in seconds.

- `heartbeat`: The age of active instances.
- `leader_heartbeat`: Time since acquisition of current leader lease for a component.
  Consistently low value may indicate a crash loop.
- `generator_reconcile`: A histogram of reconcile duration in generator.
  The `error` tag indicates the error rate of various causes.
- `aggregator_reconcile`: A histogram of reconcile duration in aggregator.
  The `error` tag indicates the error rate of various causes.
- `aggregator_next_event_pool_current_size`, `aggregator_next_event_pool_current_latency`:
  The former is the number of PodProtector objects with newer webhook admission events
  than the last watch event received from the Pod watch stream in aggregator.
  The latter is the time since the oldest of such events.
  Elevated values may indicate Pod watch lag or clock skew between webhook and aggregator.
- `webhook_request`: Number of webhook requests processed.
  A sudden drop in request rate indicates possible misconfiguration in the apiserver &rarr; webhook path.
  The histogram indicates the total processing time for each webhook request.
- `webhook_handle_pod_in_ppr`: Number of PodProtector&ndash;Pod pairs processed by webhook.
  A sudden drop in processing rate indicates possible problem with PodProtectors,
  either due to webhook informer cache inconsistency or selector misconfiguration.
  The `rejected` tag indicates whether the admission review is rejected,
  which may be used to monitor either false positives/negatives
  or actual incidents caused by other controllers trying to delete existing pods.
- `webhook_http_error`: Number of webhook requests that failed
  (instead of getting rejected or approved).
  Cross check with `apiserver_admission_webhook_rejection_count{error_type=*}` from kube-apiserver.
- `retrybatch_submit_retry_count`: A histogram of the number of PodProtector updates
  involved with each PodProtector&ndash;Pod pair.
  Note that multiple Pods for the same PodProtector
  may be involved in the same PodProtector update,
  of which the count is indicated by the `retrybatch_execute_batch_size` histogram.
  Elevated values may indicate a high conflict rate,
  e.g. caused by too many webhook instances.

### Graceful degradation

#### Core cluster control plane malfunction

##### Symptoms

Core cluster kube-apiserver/etcd not ready

##### Impact

Podseidon webhook incorrectly blocks pod deletion in worker clusterss
due to inability to update PodProtector.

##### Response

If the core cluster control plane cannot be recovered shortly
and pod deletion in worker clusters urgently needs to recover,
consider the following steps:

1. Stop the relevant controllers to avoid unexpected activity.
2. Disable the Podseidon webhook by patching the webhook configuration:

```shell
KUBECONFIG=worker-cluster.yaml kubectl \
  patch validatingwebhookconfiguration podseidon.kubewharf.io \
  --type=json -p '[{
    "op": "replace",
    "path": "/webhooks/0/objectSelector",
    "value": {"matchLabels": {"this-selector-does": "not-match-anything"}}
  }]'
```

##### Recovery

Revert the response steps in reverse order.
Ensure the webhook is handling events correctly before restarting the stopped controllers
to avoid the risk of controller malfunction
if the control plane experienced data corruption or cache inconsistency.

#### Single worker cluster control plane malfunction

##### Symptoms

Worker cluster kube-apiserver/etcd not ready

##### Impact

Aggregator for the worker cluster may not update correctly, voiding protection by Podseidon.
Assuming the absence of data plane fault and
considering that other controllers are equally unable
to dispatch pod-disrupting requests to the cluster,
no action is required as the cluster status is mostly stationary and hence accurate.
However, if the issue persists for a long time, failed pod eviction may accumulate,
and since apiserver is unable to create new pods correctly,
this may develop into data plane fault (see next row).

##### Response

Monitor pod health in case this develops into a data plane fault.

#### Single worker cluster data plane fault

##### Symptoms

Data center network connectivity issues, large scale pod unreachability

##### Impact

Aggregator is unable to obtain the latest cluster status from the worker cluster
and/or write the latest status into the data into the PodProtector in the core cluster.
This may result in an excessive count of available pods when they are actually unusable.
Thus, webhook may incorrectly allow killing healthy pods in other worker clusters
and further worsen the situation.

##### Response

Instruct generator to delete the cell status
for all or selected workloads with an incorrect status:
```shell
  KUBECONFIG=core-cluster.yaml kubectl \
    annotate ppr ${NAME_OR_LABEL_SELECTOR} \
    podseidon.kubewharf.io/remove-cell-once=${WORKER_CELL_ID}
```
This command is a backdoor implemented in generator to
force clear aggregator and webhook status for a worker cluster
when the cluster is removed or unusable.
If aggregator somehow manages to add back the status to the command,
stop aggregator for that worker cluster.

##### Recovery

If aggregator was stopped,
restart it after ensuring that the pods are really available as indicated in pod status.

#### Generator not working

##### Symptoms

- Generator leader heartbeat disappearance
- Generator reconcile QPS drop
- PodProtector `minAvailable` not updating

##### Impact

Webhook may perceive a lagging replica requirement
greater/less than the desired value,
thus incorrectly blocking/allowing pod deletion.

##### Response

Try each of the procedures in order.

- Scale up generator if CPU/memory resource saturation is observed.
- Force delete and recreate generator pods to attempt recovery.
- Confirm the problem is merely caused by Podseidon,
  and all webhook rejections are false positives.
  Confirm the absence of concurrent control plane malfunction.
- Disable the Podseidon webhook in all worker clusters
  by patching ValidatingWebhookConfiguration.

#### Aggregator not working

##### Symptoms

- Aggregator leader heartbeat disappearance
- Accumulation of `next_event_pool_*` metrics
- Accumulation of admission history in PodProtector status
- High `.status.summary.maxLatencyMillis` in PodProtector objects

##### Impact

Webhook admission history may accumulate due to lack of cleanup from aggregator,
resulting in false positive rejection.

##### Response

Try each of the procedures in order.

- Scale up aggregator if CPU/memory resource saturation is observed.
- Force delete and recreate aggregator pods to attempt recovery.
- Confirm the problem is merely caused by Podseidon,
  and all webhook rejections are false positives.
  Confirm the absence of concurrent control plane malfunction.
- Disable the Podseidon webhook in worker clusters
  by patching ValidatingWebhookConfiguration.
  The webhook for worker clusters may also need to be disabled
  since they share the same unavailability counter.

#### Webhook unavailability

##### Symptoms

Elevated values of the kube-apiserver metric
`apiserver_admission_webhook_rejection_count{error_type=calling_webhook_error,name=podseidon.kubewharf.io}`.

##### Impact

Worker clusters may be uanble to delete pods normally.

##### Response

Try each of the procedures in order.

- Scale up/out webhook if CPU/memory resource saturation is observed.
- Force delete and recreate webhook pods to attempt recovery.
- Confirm the problem is merely caused by Podseidon.
  Confirm the absence of concurrent control plane malfunction.
- Disable the Podseidon webhook in all worker clusters
  by patching ValidatingWebhookConfiguration.
