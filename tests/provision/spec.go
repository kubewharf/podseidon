// Copyright 2025 The Podseidon Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provision

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/fnv"
	"os"
	"path"
	"time"

	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	podseidonclient "github.com/kubewharf/podseidon/client/clientset/versioned"
	podseidonv1a1client "github.com/kubewharf/podseidon/client/clientset/versioned/typed/apis/v1alpha1"

	testutil "github.com/kubewharf/podseidon/tests/util"
)

type ClusterId = testutil.ClusterId

type Setup struct {
	Paths    Paths
	Images   Images
	Timeouts Timeouts
	Run      Run

	Request Request

	// To be populated by the provision package to expose environment to tests.
	Env *Env
}

func NewSetup(ctx ginkgo.SpecContext, request Request, env *Env) *Setup {
	h := fnv.New32a()
	_, _ = h.Write([]byte(ctx.SpecReport().FullText())) // fnv writer is infallible
	namespace := hex.EncodeToString(h.Sum(nil))

	env.Namespace = namespace

	return &Setup{
		Paths: Paths{
			Assets: "/etc/test-assets",
			Temp:   path.Join("/tmp/tests", namespace),
			Output: path.Join("/var/test-output", namespace),
		},
		Images: Images{
			Kwok: KwokImages{
				KwokPrefix: os.Getenv("KWOK_IMAGE_PREFIX"),
				KubePrefix: os.Getenv("KWOK_KUBE_IMAGE_PREFIX"),
			},
			Kelemetry: KelemetryImages{
				Etcd:                os.Getenv("KELEMETRY_ETCD_IMAGE"),
				JaegerQuery:         os.Getenv("KELEMETRY_JAEGER_QUERY_IMAGE"),
				JaegerCollector:     os.Getenv("KELEMETRY_JAEGER_COLLECTOR_IMAGE"),
				JaegerRemoteStorage: os.Getenv("KELEMETRY_JAEGER_REMOTE_STORAGE_IMAGE"),
				KelemetryAllInOne:   os.Getenv("KELEMETRY_ALLINONE_IMAGE"),
			},
			Podseidon: PodseidonImages{
				Generator:  "podseidon-generator",
				Aggregator: "podseidon-aggregator",
				Webhook:    "podseidon-webhook",
			},
		},
		Timeouts: Timeouts{
			KelemetryWarmup:   parseEnvDuration("KELEMETRY_WARMUP_SLEEP"),
			KelemetryWaitSpan: parseEnvDuration("KELEMETRY_WAIT_SPAN_SLEEP"),
		},
		Run: Run{
			SpecTitle: ctx.SpecReport().FullText(),
			Namespace: namespace,
			StartTime: time.Now(),
			Logger:    ginkgo.GinkgoLogr.WithValues("namespace", namespace),
		},
		Request: request,
		Env:     env,
	}
}

func parseEnvDuration(name string) time.Duration {
	str, ok := os.LookupEnv(name)
	if !ok {
		panic(fmt.Sprintf("Missing env var %s", name))
	}

	duration, err := time.ParseDuration(str)
	if err != nil {
		panic(fmt.Sprintf("Parse duration from env var %s: %v", name, err))
	}

	return duration
}

type Paths struct {
	Assets string
	Temp   string
	Output string
}

func (paths Paths) AssetPath(file string) string {
	return path.Join(paths.Assets, file)
}

func (paths Paths) LoadAsset(file string) ([]byte, error) {
	assetPath := path.Join(paths.Assets, file)

	contents, err := os.ReadFile(assetPath)
	if err != nil {
		return nil, fmt.Errorf("read asset file %s: %w", assetPath, err)
	}

	return contents, nil
}

func (paths Paths) LocalizeAsset(file string, namespace string, clusterId ClusterId) (string, error) {
	contents, err := paths.LoadAsset(file)
	if err != nil {
		return "", err
	}

	contents = bytes.ReplaceAll(contents, []byte("CLUSTER_NAME"), []byte(clusterId.String()))
	contents = bytes.ReplaceAll(contents, []byte("NAMESPACE"), []byte(namespace))
	contents = bytes.ReplaceAll(contents, []byte("TEMP_DIR"), []byte(paths.Temp))

	return paths.WriteTemp(fmt.Sprintf("%s-%s", clusterId.String(), file), contents)
}

func (paths Paths) TempPath(file string) string {
	return path.Join(paths.Temp, file)
}

func (paths Paths) WriteTemp(file string, contents []byte) (string, error) {
	tempPath := path.Join(paths.Temp, file)

	err := os.WriteFile(tempPath, contents, 0o666)
	if err != nil {
		return "", fmt.Errorf("write temp file %s: %w", tempPath, err)
	}

	return tempPath, nil
}

func (paths Paths) OutputPath(file string) string {
	return path.Join(paths.Output, file)
}

func (paths Paths) WriteOutput(file string, contents []byte) (string, error) {
	outputPath := path.Join(paths.Output, file)

	err := os.WriteFile(outputPath, contents, 0o666)
	if err != nil {
		return "", fmt.Errorf("write output file %s: %w", outputPath, err)
	}

	return outputPath, nil
}

type Images struct {
	Kwok      KwokImages
	Kelemetry KelemetryImages
	Podseidon PodseidonImages
}

type Timeouts struct {
	KelemetryWarmup   time.Duration
	KelemetryWaitSpan time.Duration
}

type KwokImages struct {
	KwokPrefix string
	KubePrefix string
}

type KelemetryImages struct {
	Etcd                string
	JaegerQuery         string
	JaegerCollector     string
	JaegerRemoteStorage string
	KelemetryAllInOne   string
}

type PodseidonImages struct {
	Generator  string
	Aggregator string
	Webhook    string
}

type Run struct {
	SpecTitle string
	Namespace string
	StartTime time.Time
	Logger    logr.Logger
}

type Request struct {
	Hash     uint32
	Count    testutil.ClusterCount
	Clusters map[testutil.ClusterId]ClusterRequest
}

type ClusterRequest struct {
	EnableAggregatorUpdateTrigger bool
}

func (req ClusterRequest) Hash(hasher hash.Hash) {
	if req.EnableAggregatorUpdateTrigger {
		_, _ = hasher.Write([]byte{1})
	} else {
		_, _ = hasher.Write([]byte{0})
	}
}

func NewRequest(numWorkers int, mutateCluster func(cluster testutil.ClusterId, req *ClusterRequest)) Request {
	count := testutil.ClusterCount{Workers: numWorkers}

	clusters := make(map[testutil.ClusterId]ClusterRequest, numWorkers+1)
	for _, cluster := range count.ClusterIds() {
		req := ClusterRequest{
			EnableAggregatorUpdateTrigger: false,
		}
		mutateCluster(cluster, &req)
		clusters[cluster] = req
	}

	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte{uint8(len(count.ClusterIds()))})
	for _, cluster := range count.ClusterIds() {
		clusters[cluster].Hash(hasher)
	}

	return Request{
		Hash:     hasher.Sum32(),
		Count:    count,
		Clusters: clusters,
	}
}

type Env struct {
	Namespace    string
	Clusters     []ClusterEnv
	TraceReports []TraceReport
}

func (env *Env) CoreCluster() *ClusterEnv {
	return &env.Clusters[0]
}

func (env *Env) WorkerClusters() []ClusterEnv {
	return env.Clusters[1:]
}

func (env *Env) PprClient() podseidonv1a1client.PodProtectorInterface {
	return env.CoreCluster().PodseidonClient.PodseidonV1alpha1().PodProtectors(env.Namespace)
}

func (env *Env) PodClient(cluster testutil.ClusterId) corev1client.PodInterface {
	return env.Cluster(cluster).NativeClient.CoreV1().Pods(env.Namespace)
}

func (env *Env) Cluster(id ClusterId) *ClusterEnv {
	return &env.Clusters[id.Index()]
}

type ClusterEnv struct {
	Id                 ClusterId
	HostKubeconfigPath string
	RestConfig         *rest.Config
	NativeClient       kubernetes.Interface
	PodseidonClient    podseidonclient.Interface

	containerKubeconfigPath string
}

type TraceReport struct {
	Cluster ClusterId
	Gvr     schema.GroupVersionResource
	Name    string
}

func (env *Env) ReportKelemetryTrace(cluster ClusterId, gvr schema.GroupVersionResource, name string) {
	env.TraceReports = append(env.TraceReports, TraceReport{
		Cluster: cluster,
		Gvr:     gvr,
		Name:    name,
	})
}
