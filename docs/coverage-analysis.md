# Coverage analysis

It is impossible to enumerate all failure scenarios,
much less protect from all of them.
However, on this page we try to analyze the impact of various components malfunctioning.

## Core control plane

"Core control plane" refers to the common dependency of all control plane components,
namely kube-apiserver and etcd.

<table>
  <tr>
    <th>Cluster</th>
    <th>Scenario</th>
    <th>Affected objects</th>
    <th>What happens without Podseidon</th>
    <th>What happens with Podseidon</th>
  </tr>

  <tr>
    <td rowspan="5">Core</td>
    <td rowspan="4">
      Data disappearance (e.g. due to etcd data corruption or buggy controllers)
    </td>
    <td>Source workload only</td>
    <td>
      :x:
      GC controller (or equivalent cascade deletion controllers)
      would cascade-delete all pods.
    </td>
    <td>
      :white_check_mark:
      PodProtector is not cascade-deleted due to lack of explicit deletionTimestamp.
      Cascade deletion of underlying pods is rejected by the Podseidon webhook.
    </td>
  </tr>

  <tr>
    <td>PodProtector only</td>
    <td>N/A</td>
    <td>
      :warning:
      Webhooks can no longer reject pod deletion,
      but controllers will not actively try to delete the pods
      since the normal path is not affected.
    </td>
  </tr>

  <tr>
    <td>PodProtector + source workload/intermediate objects</td>
    <td>
      :x:
      GC controller (or equivalent cascade deletion controllers)
      would cascade-delete all pods.
    </td>
    <td>
      :x:
      GC controller (or equivalent cascade deletion controllers)
      would cascade-delete all pods.
      Podseidon webhook is unable to protect the pods
      if kube-apiserver sent the deletion event to its informer.
    </td>
  </tr>

  <tr>
    <td>Other dependency objects</td>
    <td colspan="2">
      :warning:
      No direct impact to running pods,
      but recreated pods cannot start correctly.
    </td>
  </tr>

  <tr>
    <td>Loss of strong consistency</td>
    <td>PodProtector</td>
    <td>N/A</td>
    <td>
      :warning:
      No direct impact to normal operations,
      but webhook may be incorrectly allow pod deletion
      if apiserver returns 200 OK to conflicting PodProtector status updates.
    </td>
  </tr>

  <tr>
    <td rowspan="5">Worker</td>
    <td rowspan="3">
      Data disappearance (e.g. due to etcd data corruption or buggy controllers)
    </td>
    <td>Pod</td>
    <td colspan="2">
      :x:
      Kubelet will kill pods without warning.
      This cannot be mitigated without modifying kubelet code.
    </td>
  </tr>

  <tr>
    <td>Intermediate objects (e.g. ReplicaSet)</td>
    <td>
      :x:
      GC controller (or equivalent cascade deletion controllers)
      would cascade-delete all pods.
    </td>
    <td>
      :white_check_mark:
      PodProtector is not cascade-deleted due to lack of explicit deletionTimestamp.
      Cascade deletion of underlying pods is rejected by the Podseidon webhook.
    </td>
  </tr>

  <tr>
    <td>Podseidon ValidatingWebhookConfiguration</td>
    <td>N/A</td>
    <td>
      :warning:
      kube-apiserver no longer calls Podseidon webhook,
      so protection is lost.
      Such data disappearance is often correlated with mass pod disappearance as well,
      so the pod count drops immediately,
      and the ReplicaSet controller is unlikely to try to delete pods at the same time.
    </td>
  </tr>

  <tr>
    <td>
      Significant watch cache lag
      (but any available watch events are still delivered in order)
    </td>
    <td>Pod &rarr; Podseidon Aggregator</td>
    <td>N/A</td>
    <td>
      :warning:
      Normal operations (such as scaling and eviction) may be disrupted
      due to Podseidon webhook not observing new pods becoming available
      thus resetting the quota for pod deletion.
      <br/>
      If `--aggregator-informer-synctime-algorithm=clock`,
      this may result in incorrect approval of pod deletion
      due to the lag between PodProtector admission and event reception.
      This issue does not happen if `status` is used instead.
    </td>
  </tr>

  <tr>
    <td>Loss of strong consistency</td>
    <td>Pod &rarr; Podseidon Aggregator watch</td>
    <td>N/A</td>
    <td>
      :warning:
      Aggregator incorrectly invalidates old `admissionHistory` entries,
      which have not been observed in the current view of pod list yet.
      The resultant `estimatedAvailableReplicas` is greater than actual,
      resulting in incorrect approval of pod deletion.
    </td>
  </tr>
</table>

## Podseidon components

<table>
  <tr>
    <th>Component</th>
    <th>Scenario</th>
    <th>Consequence</th>
  </tr>

  <tr>
    <td rowspan="2">Generator</td>
    <td>Not working</td>
    <td rowspan="2">
      :warning:
      Insufficient protection after scaling up.<br/>
      Incorrect rejection after scaling down.
    </td>
  </tr>
  <tr>
    <td>Incorrect logic</td>
  </tr>

  <tr>
    <td rowspan="2">Aggregator</td>
    <td>Not working</td>
    <td>
      :warning:
      False positives in admission history are not cleared in time.<br/>
      New available pods are not observed in aggregation.<br/>
      Both may disrupt normal operations due to incorrect rejections from Podseidon webhook.
    </td>
  </tr>
  <tr>
    <td>Incorrect logic</td>
    <td>
      :warning:
      Admission history may be incorrectly cleared or preserved,
      or aggregated replica count may be too large or too little,
      resulting in incorrect approval or rejection from Podseidon webhook respectively.
    </td>
  </tr>

  <tr>
    <td rowspan="2">Webhook</td>
    <td>Unavailable</td>
    <td>
      :warning:
      Pods will be denied from deletion if
      `failurePolicy` is set to `Fail` and all instances are unavailable,
      disrupting normal operations.
    </td>
  </tr>
  <tr>
    <td>Incorrect logic</td>
    <td>
      :warning:
      Webhook may incorrectly approve or reject pod deletions.
    </td>
  </tr>
</table>

## Other components

:white_check_mark:
Disruptions to the chain between the source of truth (main workload) and pods
shall not result in service disruption beyond the level permitted by `maxUnavailable`.
