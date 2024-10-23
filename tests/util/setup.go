package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"
	podseidonclient "github.com/kubewharf/podseidon/client/clientset/versioned"
	podseidonv1a1client "github.com/kubewharf/podseidon/client/clientset/versioned/typed/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/util"
)

type Setup struct {
	CoreNativeClient    kubernetes.Interface
	CorePodseidonClient podseidonclient.Interface
	WorkerClients       []kubernetes.Interface

	Namespace string
	StartTime time.Time
}

func SetupBeforeEach() *Setup {
	//nolint:exhaustruct // late init
	setup := &Setup{StartTime: time.Now()}

	ginkgo.BeforeEach(func(ctx ginkgo.SpecContext) {
		setup.Namespace = fmt.Sprintf("test-%x", rand.Uint64())

		coreConfig, err := clientcmd.BuildConfigFromFlags("", "core-kubeconfig.host.yaml")
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

		createServiceAccount(ctx, setup.Namespace, &coreConfig)

		workerConfigs := []*rest.Config{}

		for workerId := range 2 {
			workerConfig, err := clientcmd.BuildConfigFromFlags(
				"",
				fmt.Sprintf("worker-%d-kubeconfig.host.yaml", workerId+1),
			)
			gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

			createServiceAccount(ctx, setup.Namespace, &workerConfig)

			workerConfigs = append(workerConfigs, workerConfig)
		}

		setup.CoreNativeClient = kubernetes.NewForConfigOrDie(coreConfig)
		setup.CorePodseidonClient = podseidonclient.NewForConfigOrDie(coreConfig)

		setup.WorkerClients = util.MapSlice(
			workerConfigs,
			func(config *rest.Config) kubernetes.Interface {
				return kubernetes.NewForConfigOrDie(config)
			},
		)
	})

	return setup
}

func createServiceAccount(ctx context.Context, namespace string, configPtr **rest.Config) {
	adminConfig := *configPtr
	client := kubernetes.NewForConfigOrDie(adminConfig)

	_, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   namespace,
			Labels: map[string]string{"test": namespace},
		},
	}, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	_, err = client.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "e2e-test",
		},
	}, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	_, err = client.RbacV1().Roles(namespace).Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name: "e2e-test",
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"*"},
				APIGroups: []string{"*"},
				Resources: []string{"*"},
			},
		},
	}, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	_, err = client.RbacV1().RoleBindings(namespace).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "e2e-test",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Kind:     "Role",
			Name:     "e2e-test",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "e2e-test",
				Namespace: namespace,
			},
		},
	}, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	e2eConfig := rest.CopyConfig(adminConfig)
	e2eConfig.Impersonate.UserName = fmt.Sprintf("system:serviceaccount:%s:e2e-test", namespace)

	*configPtr = e2eConfig
}

func (setup *Setup) ReportKelemetryTrace(
	ctx ginkgo.SpecContext,
	cluster string,
	gvr schema.GroupVersionResource,
	name string,
) {
	specReport := ctx.SpecReport()
	title := strings.Join(specReport.ContainerHierarchyTexts, "/")

	if specReport.LeafNodeText != "" {
		title += "/"
		title += specReport.LeafNodeText
	}

	traceTargetsDir, needTrace := os.LookupEnv("KELEMETRY_TRACE_TARGETS")
	if needTrace {
		file, err := os.OpenFile(
			path.Join(traceTargetsDir, setup.Namespace),
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			0o666,
		)
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

		defer file.Close()

		type jsonFile struct {
			Title     string    `json:"title"`
			Cluster   string    `json:"cluster"`
			Group     string    `json:"group"`
			Resource  string    `json:"resource"`
			Namespace string    `json:"namespace"`
			Name      string    `json:"name"`
			StartTime time.Time `json:"ts"`
		}

		encoder := json.NewEncoder(file)
		err = encoder.Encode(jsonFile{
			Title:     title,
			Cluster:   cluster,
			Group:     gvr.Group,
			Resource:  gvr.Resource,
			Namespace: setup.Namespace,
			Name:      name,
			StartTime: setup.StartTime,
		})
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	}
}

const TestPodLabelKey = "podseidon-e2e/test"

func (setup *Setup) PprClient() podseidonv1a1client.PodProtectorInterface {
	return setup.CorePodseidonClient.PodseidonV1alpha1().PodProtectors(setup.Namespace)
}

func (setup *Setup) PodClient(workerIndex WorkerIndex) corev1client.PodInterface {
	return setup.WorkerClients[workerIndex].CoreV1().Pods(setup.Namespace)
}

func (setup *Setup) CreatePodProtector(
	ctx ginkgo.SpecContext,
	pprName string,
	pprUid *types.UID,
	minAvailable int32,
	minReadySeconds int32,
	config podseidonv1a1.AdmissionHistoryConfig,
	pprModifier func(*podseidonv1a1.PodProtector),
) {
	setup.ReportKelemetryTrace(
		ctx,
		"core",
		podseidonv1a1.SchemeGroupVersion.WithResource("podprotectors"),
		pprName,
	)

	ppr := &podseidonv1a1.PodProtector{
		ObjectMeta: metav1.ObjectMeta{
			Name: pprName,
		},
		Spec: podseidonv1a1.PodProtectorSpec{
			MinAvailable:    minAvailable,
			MinReadySeconds: minReadySeconds,
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{TestPodLabelKey: setup.Namespace},
			},
			AdmissionHistoryConfig: config,
		},
	}
	pprModifier(ppr)

	ppr, err := setup.PprClient().
		Create(ctx, ppr, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	*pprUid = ppr.UID
}

func (setup *Setup) CreatePod(
	ctx context.Context,
	pprName string,
	pprUid types.UID,
	podId PodId,
	podModifier func(*corev1.Pod),
) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   podId.PodName(),
			Labels: map[string]string{TestPodLabelKey: setup.Namespace},
			Annotations: map[string]string{
				"kelemetry.kubewharf.io/parent-link": MustMarshalJson(
					map[string]string{
						"cluster":   "core",
						"group":     podseidonv1a1.SchemeGroupVersion.Group,
						"version":   podseidonv1a1.SchemeGroupVersion.Version,
						"resource":  "podprotectors",
						"namespace": setup.Namespace,
						"name":      pprName,
						"uid":       string(pprUid),
					},
				),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "example.com/e2e/no:image",
				},
			},
			SchedulerName: "e2e-no-schedule",
		},
	}
	podModifier(pod)
	_, err := setup.PodClient(podId.Worker).Create(ctx, pod, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
}

func (setup *Setup) CreatePodProtectorAndPods(
	ctx ginkgo.SpecContext,
	pprName string,
	replicas PodCounts,
	minAvailable int32,
	minReadySeconds int32,
	config podseidonv1a1.AdmissionHistoryConfig,
) {
	var pprUid types.UID

	setup.CreatePodProtector(
		ctx,
		pprName,
		&pprUid,
		minAvailable,
		minReadySeconds,
		config,
		func(*podseidonv1a1.PodProtector) {},
	)

	for _, podId := range replicas.Iter() {
		setup.CreatePod(ctx, pprName, pprUid, podId, func(*corev1.Pod) {})
	}
}

func (setup *Setup) MarkPodAsReady(
	ctx context.Context,
	podId PodId,
	readyTime time.Time,
) {
	err := setup.WorkerClients[podId.Worker].CoreV1().
		Pods(setup.Namespace).
		Bind(ctx, &corev1.Binding{
			ObjectMeta: metav1.ObjectMeta{
				Name: podId.PodName(),
			},
			Target: corev1.ObjectReference{
				Kind: "Node",
				Name: "e2e-node-1",
			},
		}, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	pod, err := setup.PodClient(podId.Worker).Get(ctx, podId.PodName(), metav1.GetOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	updatePodCondition(pod, corev1.PodInitialized, "True", readyTime)
	updatePodCondition(pod, corev1.ContainersReady, "True", readyTime)
	updatePodCondition(pod, corev1.PodReady, "True", readyTime)
	updatePodCondition(pod, corev1.PodScheduled, "True", readyTime)
	pod.Status.Phase = corev1.PodRunning
	_, err = setup.PodClient(podId.Worker).
		UpdateStatus(ctx, pod, metav1.UpdateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
}

//nolint:unparam // status is currently always True, but extracting it does not improve anything.
func updatePodCondition(
	pod *corev1.Pod,
	conditionType corev1.PodConditionType,
	status corev1.ConditionStatus,
	readyTime time.Time,
) {
	condition := util.GetOrAppendSliceWith(
		&pod.Status.Conditions,
		func(condition *corev1.PodCondition) bool { return condition.Type == conditionType },
		func() corev1.PodCondition { return corev1.PodCondition{Type: conditionType} },
	)
	condition.Status = status
	condition.LastTransitionTime = metav1.NewTime(readyTime)
}

func (setup *Setup) TryDeleteAllPodsIn(ctx context.Context, workerIndex WorkerIndex) error {
	return setup.PodClient(workerIndex).
		DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
			LabelSelector: metav1.FormatLabelSelector(&metav1.LabelSelector{
				MatchLabels: map[string]string{TestPodLabelKey: setup.Namespace},
			}),
		})
}

func (setup *Setup) MaintainExternalUnmatchedPod(
	ctx context.Context,
	workerIndex WorkerIndex,
	podName string,
) {
	defer ginkgo.GinkgoRecover()

	_, err := setup.PodClient(workerIndex).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Annotations: map[string]string{"trigger-reconcile": "0"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "example.com/e2e/no:image",
				},
			},
			SchedulerName: "e2e-no-schedule",
		},
	}, metav1.CreateOptions{})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	runs := 0

	wait.UntilWithContext(ctx, func(ctx context.Context) {
		runs++

		jsonMessage := func(value any) *json.RawMessage {
			return ptr.To(json.RawMessage(MustMarshalJson(value)))
		}

		_, err := setup.PodClient(workerIndex).Patch(
			ctx, podName, types.JSONPatchType,
			[]byte(MustMarshalJson(jsonpatch.Patch{
				{
					"op":    jsonMessage("replace"),
					"path":  jsonMessage("/metadata/annotations/trigger-reconcile"),
					"value": jsonMessage(strconv.Itoa(runs)),
				},
			})), metav1.PatchOptions{})
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	}, time.Millisecond*100)
}

func MustMarshalJson(object any) string {
	data, err := json.Marshal(object)
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	return string(data)
}

type (
	WorkerIndex int32
	PodIndex    int32
)

type PodId struct {
	Worker WorkerIndex
	Pod    PodIndex
}

func (id PodId) PodName() string {
	return fmt.Sprintf("worker-%d-pod-%d", int32(id.Worker)+1, id.Pod)
}

type PodCounts [2]PodIndex

func (pc PodCounts) Iter() []PodId {
	out := make([]PodId, 0, int(pc[0]+pc[1]))

	for workerIndex := range WorkerIndex(2) {
		for podIndex := range pc[workerIndex] {
			out = append(out, PodId{Worker: workerIndex, Pod: podIndex})
		}
	}

	return out
}
