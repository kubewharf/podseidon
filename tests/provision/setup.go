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
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	jaegerjson "github.com/jaegertracing/jaeger/model/json"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	podseidonclient "github.com/kubewharf/podseidon/client/clientset/versioned"

	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/util"
)

func RegisterHooks(env *Env, request Request) {
	var setup *Setup
	ginkgo.BeforeEach(func(ctx ginkgo.SpecContext) {
		setup = NewSetup(ctx, request, env)
		gomega.Expect(TryInit(ctx, setup)).To(gomega.Succeed())
	})
	ginkgo.AfterEach(func(ctx ginkgo.SpecContext) {
		if setup != nil {
			if err := setup.TryCollectLogs(ctx); err != nil {
				setup.Run.Logger.Error(err, "collect logs before finalize")
			}
		}
	})
}

func TryInit(ctx context.Context, setup *Setup) error {
	if err := os.MkdirAll(setup.Paths.Temp, 0o777); err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	if err := os.MkdirAll(setup.Paths.Output, 0o777); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if _, err := setup.Paths.WriteOutput("title", []byte(setup.Run.SpecTitle)); err != nil {
		return fmt.Errorf("write spec title to output: %w", err)
	}

	clusters := setup.Request.Count.ClusterIds()
	setup.Env.Clusters = make([]ClusterEnv, len(clusters))

	var wg sync.WaitGroup
	errs := make([]error, len(clusters))
	for i, clusterId := range clusters {
		errPtr := &errs[i]
		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := setup.InitCluster(ctx, clusterId); err != nil {
				*errPtr = fmt.Errorf("init cluster %v: %w", clusterId, err)
			}
		}()
	}
	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		return err
	}

	if err := setup.DeployPodseidon(ctx); err != nil {
		return fmt.Errorf("deploy podseidon: %w", err)
	}

	if err := setup.InstallKelemetry(ctx); err != nil {
		return fmt.Errorf("install kelemetry: %w", err)
	}

	return nil
}

func (setup *Setup) InitCluster(ctx context.Context, clusterId ClusterId) error {
	if err := setup.StartKwok(ctx, clusterId); err != nil {
		return fmt.Errorf("start kwok cluster: %w", err)
	}

	clusterEnv := &setup.Env.Clusters[clusterId.Index()]
	if clusterId.IsCore() {
		if err := setup.InstallCoreChart(ctx, clusterEnv); err != nil {
			return fmt.Errorf("install core chart: %w", err)
		}
	} else {
		if err := setup.InstallWorkerChart(ctx, clusterEnv); err != nil {
			return fmt.Errorf("install worker chart for cluster %v: %w", clusterId, err)
		}
	}

	return nil
}

func (setup *Setup) StartKwok(ctx context.Context, clusterId ClusterId) error {
	clusterName := clusterId.KwokName(setup.Run.Namespace)

	logger := setup.Run.Logger.WithValues("cluster", clusterName)
	logger.Info("Create kwok cluster")

	localConfigPaths := make([]string, 3)
	for i, assetFile := range []string{"kwokctl-config.yaml", "audit-policy.yaml", "audit-kubeconfig.yaml"} {
		p, err := setup.Paths.LocalizeAsset(assetFile, setup.Run.Namespace, clusterId)
		if err != nil {
			return err
		}
		localConfigPaths[i] = p
	}

	apiserverPort, err := allocatePort()
	if err != nil {
		return err
	}
	etcdPort, err := allocatePort()
	if err != nil {
		return err
	}

	kubeconfigPath := setup.Paths.TempPath(fmt.Sprintf("%s-kubeconfig.yaml", clusterId))
	if err := setup.Command(
		"kwokctl", "create", "cluster",
		"--config", localConfigPaths[0],
		"--name", clusterName,
		"--kube-apiserver-port", fmt.Sprint(apiserverPort),
		"--etcd-port", fmt.Sprint(etcdPort),
	).
		Env("KWOK_IMAGE_PREFIX", setup.Images.Kwok.KwokPrefix).
		Env("KWOK_KUBE_IMAGE_PREFIX", setup.Images.Kwok.KubePrefix).
		KubeconfigPath(kubeconfigPath).
		Exec(ctx); err != nil {
		return fmt.Errorf("invoke kwokctl: %w", err)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("read kubeconfig from kwok output: %w", err)
	}
	nativeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("construct native client: %w", err)
	}
	podseidonClient, err := podseidonclient.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("construct native client: %w", err)
	}

	logger.Info("Wait for apiserver ready to write")
	startWait := time.Now()
	if err := wait.PollUntilContextTimeout(ctx, time.Second, time.Minute, true, func(ctx context.Context) (_done bool, _err error) {
		_, err := nativeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: setup.Run.Namespace,
				Annotations: map[string]string{
					"spec-title": setup.Run.SpecTitle,
				},
				Labels: map[string]string{
					"name": setup.Run.Namespace,
				},
			},
		}, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return false, nil
		}

		return true, nil
	}); err != nil {
		return fmt.Errorf("apiserver not ready after one minute: %w", err)
	}
	logger.Info("apiserver ready", "elapsed", time.Since(startWait))

	containerKubeconfig, err := setup.Command(
		"kubectl", "config", "view",
		"--raw", "--flatten", "--minify", "-ojson",
	).KubeconfigPath(kubeconfigPath).CheckOutput(ctx)
	if err != nil {
		return fmt.Errorf("generate container kubeconfig: %w", err)
	}

	if err := editJson(&containerKubeconfig, func(config *map[string]any) {
		for _, cluster := range (*config)["clusters"].([]any) {
			cluster := cluster.(map[string]any)["cluster"].(map[string]any)
			cluster["server"] = fmt.Sprintf("https://kwok-%s-kube-apiserver:6443", clusterName)
		}
	}); err != nil {
		return fmt.Errorf("rewrite host kubeconfig as container kubeconfig: %w", err)
	}

	containerKubeconfigPath, err := setup.Paths.WriteTemp(fmt.Sprintf("container-%s-kubeconfig.yaml", clusterId), containerKubeconfig)
	if err != nil {
		return err
	}

	setup.Env.Clusters[clusterId.Index()] = ClusterEnv{
		Id:                      clusterId,
		HostKubeconfigPath:      kubeconfigPath,
		RestConfig:              restConfig,
		NativeClient:            nativeClient,
		PodseidonClient:         podseidonClient,
		containerKubeconfigPath: containerKubeconfigPath,
	}

	return nil
}

type (
	WebhookTlsValuesPatch struct {
		Cert string `json:"cert"`
		Key  string `json:"key"`
	}
	WebhookValuesPatch struct {
		Tls WebhookTlsValuesPatch `json:"tls"`
	}
	ReleaseValuesPatch struct {
		Core         bool   `json:"core"`
		Worker       bool   `json:"worker"`
		WorkerCellId string `json:"workerCellId,omitempty"`
	}
	ValuesPatch struct {
		Webhook WebhookValuesPatch `json:"webhook"`
		Release ReleaseValuesPatch `json:"release"`
	}
)

func (setup *Setup) InstallCoreChart(ctx context.Context, cluster *ClusterEnv) error {
	var valuesPatch ValuesPatch

	cert, err := setup.Paths.LoadAsset("webhook.pem")
	if err != nil {
		return err
	}
	valuesPatch.Webhook.Tls.Cert = string(cert)

	key, err := setup.Paths.LoadAsset("webhook-key.pem")
	if err != nil {
		return err
	}
	valuesPatch.Webhook.Tls.Key = string(key)

	valuesPatch.Release.Core = true
	valuesPatch.Release.Worker = false

	valuesPatchJson, err := json.Marshal(valuesPatch)
	if err != nil {
		return fmt.Errorf("marshal values patch: %w", err)
	}
	valuesPath, err := setup.Paths.WriteTemp("core-chart-values.json", valuesPatchJson)
	if err != nil {
		return err
	}

	if err := setup.Command(
		"helm", "install", "podseidon",
		setup.Paths.AssetPath("chart.tgz"),
		"-f", valuesPath,
	).Kubeconfig(cluster.Id).Exec(ctx); err != nil {
		return fmt.Errorf("execute helm: %w", err)
	}

	return nil
}

func (setup *Setup) InstallWorkerChart(ctx context.Context, cluster *ClusterEnv) error {
	var valuesPatch ValuesPatch

	cert, err := setup.Paths.LoadAsset("webhook.pem")
	if err != nil {
		return err
	}
	valuesPatch.Webhook.Tls.Cert = string(cert)

	key, err := setup.Paths.LoadAsset("webhook-key.pem")
	if err != nil {
		return err
	}
	valuesPatch.Webhook.Tls.Key = string(key)

	valuesPatch.Release.Core = false
	valuesPatch.Release.Worker = true
	valuesPatch.Release.WorkerCellId = cluster.Id.String()

	valuesPatchJson, err := json.Marshal(valuesPatch)
	if err != nil {
		return fmt.Errorf("marshal values patch: %w", err)
	}
	valuesPath, err := setup.Paths.WriteTemp(fmt.Sprintf("%v-chart-values.json", cluster.Id), valuesPatchJson)
	if err != nil {
		return err
	}

	if err := setup.Command(
		"helm", "install", "podseidon",
		setup.Paths.AssetPath("chart.tgz"),
		"-f", valuesPath,
	).Kubeconfig(cluster.Id).Exec(ctx); err != nil {
		return fmt.Errorf("execute helm: %w", err)
	}

	if err := setup.Command(
		"kubectl", "create",
		"-f", setup.Paths.AssetPath("e2e-node.yaml"),
	).Kubeconfig(cluster.Id).Exec(ctx); err != nil {
		return fmt.Errorf("create e2e node: %w", err)
	}

	return nil
}

func (setup *Setup) kwokNetworkNames() []string {
	return util.MapSlice(setup.Request.Count.ClusterIds(), func(id ClusterId) string {
		return fmt.Sprintf("kwok-%s", id.KwokName(setup.Run.Namespace))
	})
}

func (setup *Setup) DeployPodseidon(ctx context.Context) error {
	if err := setup.DockerRun(setup.Images.Podseidon.Generator, "podseidon-generator").
		AddNetwork(setup.kwokNetworkNames()...).
		OptionMount("core-kube-config", setup.Env.CoreCluster().containerKubeconfigPath).
		Option("core-kube-impersonate-username", "system:serviceaccount:default:podseidon-generator").
		Option("klog-v", "6").
		Option("healthz-bind-addr", "0.0.0.0").Option("healthz-port", "8081").
		Option("pprof-bind-addr", "0.0.0.0").Option("pprof-port", "6060").
		Option("prometheus-http-bind-addr", "0.0.0.0").Option("prometheus-http-port", "9090").
		Run(ctx); err != nil {
		return err
	}

	if err := setup.DockerRun(setup.Images.Podseidon.Webhook, "podseidon-webhook").
		AddNetwork(setup.kwokNetworkNames()...).
		DockerOption("hostname", "podseidon-webhook").
		OptionMount("core-kube-config", setup.Env.CoreCluster().containerKubeconfigPath).
		Option("core-kube-impersonate-username", "system:serviceaccount:default:podseidon-webhook").
		Option("webhook-enable", "false").Option("webhook-https-enable", "true").
		OptionMount("webhook-https-cert-file", setup.Paths.AssetPath("webhook.pem")).
		OptionMount("webhook-https-key-file", setup.Paths.AssetPath("webhook-key.pem")).
		Option("klog-v", "6").
		Option("healthz-bind-addr", "0.0.0.0").Option("healthz-port", "8081").
		Option("pprof-bind-addr", "0.0.0.0").Option("pprof-port", "6060").
		Option("prometheus-http-bind-addr", "0.0.0.0").Option("prometheus-http-port", "9090").
		Option("webhook-https-bind-addr", "0.0.0.0").Option("webhook-https-port", "8080").
		Run(ctx); err != nil {
		return err
	}

	for _, cluster := range setup.Request.Count.WorkerClusterIds() {
		if err := setup.fixPodseidonWebhookConfig(ctx, cluster); err != nil {
			return err
		}

		if err := setup.DockerRun(setup.Images.Podseidon.Aggregator, fmt.Sprintf("podseidon-aggregator-%s", cluster.String())).
			AddNetwork(setup.kwokNetworkNames()...).
			OptionMount("core-kube-config", setup.Env.CoreCluster().containerKubeconfigPath).
			Option("core-kube-impersonate-username", "system:serviceaccount:default:podseidon-aggregator").
			OptionMount("worker-kube-config", setup.Env.Cluster(cluster).containerKubeconfigPath).
			Option("worker-kube-impersonate-username", "system:serviceaccount:default:podseidon-aggregator").
			Option("aggregator-cell-id", cluster.String()).
			Option("aggregator-podprotector-label-selector", "aggregator-ignore-ppr!=true").
			Option("klog-v", "6").
			Option("healthz-bind-addr", "0.0.0.0").Option("healthz-port", "8081").
			Option("pprof-bind-addr", "0.0.0.0").Option("pprof-port", "6060").
			Option("prometheus-http-bind-addr", "0.0.0.0").Option("prometheus-http-port", "9090").
			Option("update-trigger-enable", fmt.Sprint(setup.Request.Clusters[cluster].EnableAggregatorUpdateTrigger)).
			Option("update-trigger-dummy-pod-namespace", setup.Run.Namespace).
			Run(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (setup *Setup) fixPodseidonWebhookConfig(ctx context.Context, cluster ClusterId) error {
	if _, err := setup.Env.Cluster(cluster).NativeClient.CoreV1().
		Services(metav1.NamespaceDefault).
		Create(ctx, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "podseidon-webhook",
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Port:       8080,
					TargetPort: intstr.FromInt(8080),
					Name:       "webhook",
					Protocol:   "TCP",
				}},
			},
		}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create webhook route service: %w", err)
	}

	webhookAddr, err := setup.PollContainerAddressInNetwork(
		ctx,
		"podseidon-webhook",
		fmt.Sprintf("kwok-%s", cluster.KwokName(setup.Run.Namespace)),
	)
	if err != nil {
		return err
	}

	if _, err := setup.Env.Cluster(cluster).NativeClient.DiscoveryV1().
		EndpointSlices(metav1.NamespaceDefault).
		Create(ctx, &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "podseidon-webhook",
				Labels: map[string]string{
					"kubernetes.io/service-name": "podseidon-webhook",
				},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Ports: []discoveryv1.EndpointPort{{
				Name:     ptr.To("webhook"),
				Protocol: ptr.To(corev1.ProtocolTCP),
				Port:     ptr.To[int32](8080),
			}},
			Endpoints: []discoveryv1.Endpoint{{
				Addresses: []string{webhookAddr},
			}},
		}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create webhook route endpoint slice: %w", err)
	}

	vwcClient := setup.Env.Cluster(cluster).NativeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations()
	vwc, err := vwcClient.
		Get(ctx, "podseidon.kubewharf.io", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get webhook: %w", err)
	}

	vwc.Webhooks[0].ClientConfig.Service = nil
	vwc.Webhooks[0].ClientConfig.URL = ptr.To("https://podseidon-webhook:8080/" + cluster.String())

	if _, err := vwcClient.Update(ctx, vwc, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update webhook configuration: %w", err)
	}

	return nil
}

func (setup *Setup) InstallKelemetry(ctx context.Context) error {
	// Adapted from kelemetry quickstart.docker-compose.yaml
	kelemetryNetwork := fmt.Sprintf("%s-kelemetry", setup.Run.Namespace)

	if err := setup.Command("docker", "network", "create", kelemetryNetwork).Exec(ctx); err != nil {
		return err
	}

	if err := setup.DockerRun(setup.Images.Kelemetry.Etcd, "kelemetry-etcd").
		Entrypoint("etcd").
		AddNetwork(kelemetryNetwork).
		Option("name", "main").
		Option("advertise-client-urls", fmt.Sprintf("http://%s-kelemetry-etcd:2379", setup.Run.Namespace)).
		Option("listen-client-urls", "http://0.0.0.0:2379").
		Option("initial-advertise-peer-urls", fmt.Sprintf("http://%s-kelemetry-etcd:2380", setup.Run.Namespace)).
		Option("listen-peer-urls", "http://0.0.0.0:2380").
		Option("initial-cluster", fmt.Sprintf("main=http://%s-kelemetry-etcd:2380", setup.Run.Namespace)).
		Option("initial-cluster-state", "new").
		Option("initial-cluster-token", "etcd-cluster-1").
		Option("data-dir", "/var/run/etcd/default.etcd").
		Run(ctx); err != nil {
		return err
	}

	if err := setup.DockerRun(setup.Images.Kelemetry.JaegerQuery, "kelemetry-jaeger-query").
		AddNetwork(kelemetryNetwork).
		Env("GRPC_STORAGE_SERVER", fmt.Sprintf("%s-kelemetry-core:17271", setup.Run.Namespace)).
		Env("SPAN_STORAGE_TYPE", "grpc").
		Run(ctx); err != nil {
		return err
	}

	if err := setup.DockerRun(setup.Images.Kelemetry.JaegerCollector, "kelemetry-jaeger-collector").
		AddNetwork(kelemetryNetwork).
		Env("GRPC_STORAGE_SERVER", fmt.Sprintf("%s-kelemetry-jaeger-badger:17271", setup.Run.Namespace)).
		Env("SPAN_STORAGE_TYPE", "grpc").
		Env("COLLECTOR_OTLP_ENABLED", "true").
		Run(ctx); err != nil {
		return err
	}

	if err := setup.DockerRun(setup.Images.Kelemetry.JaegerRemoteStorage, "kelemetry-jaeger-badger").
		AddNetwork(kelemetryNetwork).
		Env("SPAN_STORAGE_TYPE", "badger").
		Run(ctx); err != nil {
		return err
	}

	jaegerClusterNames := strings.Join(util.MapSlice(setup.Request.Count.ClusterIds(), ClusterId.String), ",")

	for _, cluster := range setup.Request.Count.ClusterIds() {
		run := dockerRunKelemetry(setup, cluster, kelemetryNetwork)

		if cluster.IsCore() {
			run = run.
				Option("audit-consumer-enable", "true").
				Option("audit-producer-enable", "true").
				Option("audit-webhook-enable", "true").
				Option("diff-decorator-enable", "true").
				Option("annotation-linker-enable", "true").
				Option("owner-linker-enable", "true").
				Option("mq", "local").
				Option("audit-consumer-partition", "0,1,2,3").
				Option("linker-worker-count", "8").
				Option("http-address", "0.0.0.0").
				Option("http-port", "8080").
				Option("jaeger-storage-plugin-enable", "true").
				Option("jaeger-redirect-server-enable", "true").
				Option("jaeger-storage-plugin-address", "0.0.0.0:17271").
				Option("jaeger-cluster-names", jaegerClusterNames).
				Option("jaeger-backend", "jaeger-storage").
				Option("jaeger-storage.span-storage.type", "grpc-plugin").
				Option("jaeger-storage.grpc-storage.server", fmt.Sprintf("%s-kelemetry-jaeger-badger:17271", setup.Run.Namespace))
		}

		if err := run.Run(ctx); err != nil {
			return err
		}
	}

	time.Sleep(setup.Timeouts.KelemetryWarmup)

	return nil
}

func dockerRunKelemetry(setup *Setup, cluster ClusterId, kelemetryNetwork string) *DockerRun {
	kubeConfigPaths := util.MapSlice(setup.Request.Count.ClusterIds(), func(cluster ClusterId) iter.Pair[string, string] {
		return iter.NewPair(cluster.String(), setup.Env.Cluster(cluster).containerKubeconfigPath)
	})

	return setup.DockerRun(setup.Images.Kelemetry.KelemetryAllInOne, fmt.Sprintf("kelemetry-%s", cluster.String())).
		AddNetwork(kelemetryNetwork).
		AddNetwork(setup.kwokNetworkNames()...).
		Option("kube-target-cluster", cluster.String()).
		Option("kube-target-rest-burst", "100").
		Option("kube-target-rest-qps", "100").
		OptionMountMap("kube-config-paths", kubeConfigPaths).
		Option("diff-cache", "etcd").
		Option("diff-cache-etcd-endpoints", fmt.Sprintf("%s-kelemetry-etcd:2379", setup.Run.Namespace)).
		Option("diff-cache-wrapper-enable", "true").
		Option("event-informer-enable", "true").
		Option("diff-controller-enable", "true").
		Option("diff-controller-leader-election-enable", "false").
		Option("event-informer-leader-election-enable", "false").
		Option("span-cache", "etcd").
		Option("span-cache-etcd-endpoints", fmt.Sprintf("%s-kelemetry-etcd:2379", setup.Run.Namespace)).
		Option("tracer-otel-endpoint", fmt.Sprintf("%s-kelemetry-jaeger-collector:4317", setup.Run.Namespace)).
		Option("tracer-otel-insecure", "true")
}

func (setup *Setup) TryCollectLogs(ctx context.Context) error {
	if err := os.MkdirAll(path.Join(setup.Paths.Output, "traces"), 0o777); err != nil {
		return fmt.Errorf("create trace output directory: %w", err)
	}

	var wg sync.WaitGroup

	containerNames := []string{"podseidon-generator", "podseidon-webhook"}
	for _, worker := range setup.Request.Count.WorkerClusterIds() {
		containerNames = append(containerNames, fmt.Sprintf("podseidon-aggregator-%s", worker.String()))
	}

	for _, containerName := range containerNames {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := setup.Command("docker", "logs", containerName).
				StdoutFile(setup.Paths.OutputPath(fmt.Sprintf("%s.stdout.log", containerName))).
				StderrFile(setup.Paths.OutputPath(fmt.Sprintf("%s.stderr.log", containerName))).
				Exec(ctx); err != nil {
				setup.Run.Logger.Error(err, "export container logs", "container", containerName)
			}

			if err := setup.collectMetrics(ctx, containerName); err != nil {
				setup.Run.Logger.Error(err, "collect container metrics", "container", containerName)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		setup.collectTraces(ctx)
	}()

	wg.Wait()
	return nil
}

func (setup *Setup) collectMetrics(ctx context.Context, containerName string) error {
	containerAddr, err := setup.GetContainerAddress(ctx, containerName)
	if err != nil {
		return err
	}

	metricsData, err := setup.httpRequest(ctx, "GET", &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(containerAddr, "9090"),
	}, nil)
	if err != nil {
		return fmt.Errorf("query container metrics endpoint (%s:9090): %w", containerAddr, err)
	}

	if _, err := setup.Paths.WriteOutput(fmt.Sprintf("%s.metrics.txt", containerName), metricsData); err != nil {
		return err
	}

	return nil
}

func (setup *Setup) collectTraces(ctx context.Context) {
	setup.Run.Logger.Info("Waiting for Kelemetry spans to get ready")
	time.Sleep(setup.Timeouts.KelemetryWaitSpan)

	var wg sync.WaitGroup
	traces := make([][]*jaegerjson.Trace, len(setup.Env.TraceReports))

	for i, report := range setup.Env.TraceReports {
		tracePtr := &traces[i]
		wg.Add(1)

		go func() {
			defer wg.Done()
			if reportTraces, err := setup.collectTrace(ctx, report); err != nil {
				setup.Run.Logger.Error(err, "collect kelemetry trace", "name", report.Name)
			} else {
				*tracePtr = reportTraces
			}
		}()
	}
	wg.Wait()

	flattened := []*jaegerjson.Trace(nil)
	for _, reportTraces := range traces {
		flattened = append(flattened, reportTraces...)
	}

	if len(flattened) == 0 {
		setup.Run.Logger.Error(nil, "No spans matched")
		return
	}

	if err := setup.persistTrace(flattened); err != nil {
		setup.Run.Logger.Error(err, "persist kelemetry trace")
	}
}

func (setup *Setup) collectTrace(ctx context.Context, report TraceReport) ([]*jaegerjson.Trace, error) {
	queryAddr, err := setup.GetContainerAddress(ctx, "kelemetry-jaeger-query")
	if err != nil {
		return nil, err
	}

	tagsJson, err := json.Marshal(map[string]string{
		"group":     report.Gvr.Group,
		"resource":  report.Gvr.Resource,
		"namespace": setup.Run.Namespace,
		"name":      report.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("serialize tags as JSON: %w", err)
	}

	searchResp, err := httpJsonRequest[jaegerStructuredResponse[[]struct {
		TraceID string `json:"traceID"`
	}]](ctx, setup, "GET", &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(queryAddr, "16686"),
		Path:   "/api/traces",
		RawQuery: url.Values{
			"end":      {fmt.Sprint(time.Now().UnixMicro())},
			"lookback": {time.Since(setup.Run.StartTime).String()},
			"service":  {"tracing"},
			"tags":     {string(tagsJson)},
		}.Encode(),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("search traces %s from jaeger query: %w", string(tagsJson), err)
	}

	setup.Run.Logger.Info("list traces from jaeger query", "traces", len(searchResp.Data))

	traces := []*jaegerjson.Trace(nil)
	for _, trace := range searchResp.Data {
		getResp, err := httpJsonRequest[jaegerStructuredResponse[[]*jaegerjson.Trace]](ctx, setup, "GET", &url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort(queryAddr, "16686"),
			Path:   fmt.Sprintf("/api/traces/%s", trace.TraceID),
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("get trace from query server: %w", err)
		}

		traces = append(traces, getResp.Data...)
	}

	if len(traces) == 0 {
		setup.Run.Logger.Info("no matching traces", "tags", string(tagsJson))
	}

	return traces, nil
}

func (setup *Setup) persistTrace(traces []*jaegerjson.Trace) error {
	merged := mergeTraces(traces)
	mergedJson, err := json.Marshal(jaegerStructuredResponse[[]*jaegerjson.Trace]{
		Data:   []*jaegerjson.Trace{merged},
		Total:  1,
		Limit:  1,
		Offset: 0,
		Errors: nil,
	})
	if err != nil {
		return fmt.Errorf("serialize merged trace JSON: %w", err)
	}
	if _, err := setup.Paths.WriteOutput("traces/trace.json", mergedJson); err != nil {
		return fmt.Errorf("persist trace to output file: %w", err)
	}

	return nil
}

func mergeTraces(traces []*jaegerjson.Trace) *jaegerjson.Trace {
	output := &jaegerjson.Trace{
		TraceID:   traces[0].TraceID,
		Spans:     nil,
		Processes: make(map[jaegerjson.ProcessID]jaegerjson.Process, len(traces)),
		Warnings:  nil,
	}

	for _, trace := range traces {
		type processKey struct {
			cluster  string
			group    string
			resource string
		}

		processIdMap := make(map[processKey]jaegerjson.ProcessID, len(output.Processes))
		for _, span := range trace.Spans {
			var pk processKey
			for _, tag := range span.Tags {
				//revive:disable-next-line:enforce-switch-style
				switch tag.Key {
				case "cluster":
					pk.cluster, _ = tag.Value.(string)
				case "group":
					pk.group, _ = tag.Value.(string)
				case "resource":
					pk.resource, _ = tag.Value.(string)
				}
			}

			processId, hasProcess := processIdMap[pk]
			if !hasProcess {
				processId = jaegerjson.ProcessID(fmt.Sprint(len(output.Processes)))
				output.Processes[processId] = jaegerjson.Process{
					ServiceName: fmt.Sprintf("%s %s", pk.cluster, pk.resource),
					Tags:        nil,
				}
			}

			span.ProcessID = processId
			output.Spans = append(output.Spans, span)
		}
	}

	return output
}
