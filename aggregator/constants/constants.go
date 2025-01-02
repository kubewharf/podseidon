package constants

import "github.com/kubewharf/podseidon/util/kube"

const (
	WorkerClusterName kube.ClusterName = "worker"
)

const LeaderPhase kube.InformerPhase = "main"

const ElectorName kube.ElectorName = "aggregator"

var ElectorArgs = kube.ElectorArgs{
	ClusterName: WorkerClusterName,
	ElectorName: ElectorName,
}

const AnnotUpdateTriggerTime string = "podseidon.kubewharf.io/update-trigger-time"
