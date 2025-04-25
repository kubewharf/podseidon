// Copyright 2024 The Podseidon Authors.
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

package aggregator

import (
	"context"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/cache/sharding"
	"k8s.io/utils/clock"

	podseidonv1a1 "github.com/kubewharf/podseidon/apis/v1alpha1"

	"github.com/kubewharf/podseidon/util/component"
	"github.com/kubewharf/podseidon/util/defaultconfig"
	"github.com/kubewharf/podseidon/util/errors"
	utilflag "github.com/kubewharf/podseidon/util/flag"
	"github.com/kubewharf/podseidon/util/haschange"
	"github.com/kubewharf/podseidon/util/iter"
	"github.com/kubewharf/podseidon/util/kube"
	"github.com/kubewharf/podseidon/util/labelindex"
	"github.com/kubewharf/podseidon/util/o11y"
	"github.com/kubewharf/podseidon/util/optional"
	pprutil "github.com/kubewharf/podseidon/util/podprotector"
	"github.com/kubewharf/podseidon/util/util"
	"github.com/kubewharf/podseidon/util/worker"

	"github.com/kubewharf/podseidon/aggregator/constants"
	"github.com/kubewharf/podseidon/aggregator/observer"
	"github.com/kubewharf/podseidon/aggregator/synctime"
)

var NewController = component.Declare(
	func(ControllerArgs) string { return "aggregator" },
	func(_ ControllerArgs, fs *flag.FlagSet) ControllerOptions {
		return ControllerOptions{
			cellId: fs.String(
				"cell-id",
				"default",
				"the cell this aggregator instance is deployed for",
			),
			pprLabelSelector: utilflag.LabelSelectorEverything(
				fs,
				"podprotector-label-selector",
				"skip handling PodProtectors that do not match this label selector; "+
					"this currently does not affect the list-watch and is only used for fault injection",
			),
			podLabelSelector: utilflag.LabelSelectorEverything(
				fs,
				"pod-label-selector",
				"only watch pods matching this label selector, used to reduce memory usage by excluding irrelevant pods",
			),
			podInformerShards: fs.Int("pod-informer-shards", 1, "number of pod informer shards (must be supported by apiserver)"),
			podRelistPeriod: fs.Duration(
				"pod-relist-period",
				0,
				"pod informer relist frequency, enable if pod updates are infrequent",
			),
			aggRateJitter: [2]*float64{
				fs.Float64(
					"rate-jitter-low",
					0.5,
					"jitter aggregationRate with a uniformly distributed multiplier [jitter-low, jitter-high]",
				),
				fs.Float64(
					"rate-jitter-high",
					1.0,
					"jitter aggregationRate with a uniformly distributed multiplier [jitter-low, jitter-high]",
				),
			},
		}
	},
	func(args ControllerArgs, requests *component.DepRequests) ControllerDeps {
		return ControllerDeps{
			sourceProvider: component.DepPtr(requests, pprutil.RequestSourceProvider()),
			syncTimeAlgo:   component.DepPtr(requests, synctime.RequestPodInterpreter()),
			workerClient: component.DepPtr(
				requests,
				kube.NewClient(kube.ClientArgs{ClusterName: constants.WorkerClusterName}),
			),
			pprInformer: component.DepPtr(requests, pprutil.NewIndexedInformer(pprutil.IndexedInformerArgs{
				Suffix:  "",
				Elector: optional.Some(constants.ElectorArgs),
			})),
			observer: o11y.Request[observer.Observer](requests),
			elector:  component.DepPtr(requests, kube.NewElector(constants.ElectorArgs)),
			worker: component.DepPtr(requests, worker.New[pprutil.PodProtectorKey](
				"aggregator",
				args.Clock,
			)),
			defaultConfig: component.DepPtr(requests, defaultconfig.New(util.Empty{})),
		}
	},
	initController,
	component.Lifecycle[ControllerArgs, ControllerOptions, ControllerDeps, ControllerState]{
		Start: func(ctx context.Context, _ *ControllerArgs, _ *ControllerOptions, deps *ControllerDeps, state *ControllerState) error {
			go func() {
				ctx, err := deps.elector.Get().Await(ctx)
				if err != nil {
					return
				}

				state.startPodInformers(ctx.Done())

				for shardNumber, shard := range state.caches.podInformerShards {
					arg := observer.NextEventPoolMonitor{ShardNumber: shardNumber}

					go deps.observer.Get().NextEventPoolCurrentSize(
						ctx,
						arg,
						shard.notifyOnInformerEvent.ItemCount,
					)
					go deps.observer.Get().NextEventPoolCurrentLatency(
						ctx,
						arg,
						shard.notifyOnInformerEvent.TimeSinceLastDrain,
					)
				}
			}()

			return nil
		},
		Join:         nil,
		HealthChecks: nil,
	},
	func(*component.Data[ControllerArgs, ControllerOptions, ControllerDeps, ControllerState]) util.Empty {
		return util.Empty{}
	},
)

type ControllerArgs struct {
	Clock clock.WithTicker
}

type ControllerOptions struct {
	cellId            *string
	pprLabelSelector  *labels.Selector
	podLabelSelector  *labels.Selector
	podInformerShards *int
	podRelistPeriod   *time.Duration
	aggRateJitter     [2]*float64
}

type ControllerDeps struct {
	sourceProvider component.Dep[pprutil.SourceProvider]
	syncTimeAlgo   component.Dep[synctime.PodInterpreter]
	workerClient   component.Dep[*kube.Client]
	pprInformer    component.Dep[pprutil.IndexedInformer]
	elector        component.Dep[*kube.Elector]
	observer       component.Dep[observer.Observer]
	worker         component.Dep[worker.Api[pprutil.PodProtectorKey]]
	defaultConfig  component.Dep[*defaultconfig.Options]
}

type ControllerState struct {
	startPodInformers func(<-chan util.Empty)

	caches Caches
}

type Sets = *labelindex.Locked[
	types.NamespacedName, map[string]string, labelindex.NamespacedQuery[metav1.LabelSelector], util.Empty, error,
	*labelindex.Namespaced[
		map[string]string, metav1.LabelSelector, util.Empty, error, *labelindex.Sets[string],
	],
]

type Caches struct {
	podIndex Sets

	pprSourceProvider pprutil.SourceProvider
	pprInformer       pprutil.IndexedInformer

	podInformerShards []podInformerShard
}

func (caches *Caches) findPodFromAnyShard(nsName types.NamespacedName) (*corev1.Pod, error) {
	var err error

	// TODO: Getting from shards[HashFNV32(nsName.Name)] is asymptotically faster.
	// Is it worth changing to the O(1) algorithm instead?
	for _, shard := range caches.podInformerShards {
		var pod *corev1.Pod
		if pod, err = shard.podLister.Pods(nsName.Namespace).Get(nsName.Name); err == nil && pod != nil {
			//nolint:wrapcheck // wrapped by the caller
			return pod, err
		}
	}

	//nolint:wrapcheck // wrapped by the caller
	return nil, err
}

func (caches *Caches) getShardNumber(podName string) int {
	return int(sharding.HashFNV32(podName)) % len(caches.podInformerShards)
}

type podInformerShard struct {
	podLister             corev1listers.PodLister
	informerSyncReader    synctime.Reader
	notifyOnInformerEvent *nextEventPool
}

func initController(
	ctx context.Context,
	args ControllerArgs,
	options ControllerOptions,
	deps ControllerDeps,
) (*ControllerState, error) {
	if *options.podInformerShards < 1 {
		return nil, errors.TagErrorf("TooFewShards", "--aggregator-pod-informer-shards must be a positive integer")
	}

	podIndex := labelindex.NewLocked(
		labelindex.NewNamespaced(labelindex.NewSets[string], labelindex.ErrorErrAdapter{}),
		labelindex.ErrorErrAdapter{},
	)

	shards := make([]podInformerShard, *options.podInformerShards)
	podInformers := make([]cache.SharedIndexInformer, *options.podInformerShards)

	for shardNumber := range shards {
		shard, podInformer, err := newPodInformerShard(ctx, options, deps, shardNumber, podIndex)
		if err != nil {
			return nil, err
		}

		shards[shardNumber] = shard

		podInformers[shardNumber] = podInformer
	}

	state := &ControllerState{
		caches: Caches{
			podIndex:          podIndex,
			pprSourceProvider: deps.sourceProvider.Get(),
			pprInformer:       deps.pprInformer.Get(),
			podInformerShards: shards,
		},
		startPodInformers: func(stopCh <-chan util.Empty) {
			for _, informer := range podInformers {
				go informer.Run(stopCh)
			}
		},
	}

	queue := deps.worker.Get()

	prereqs := map[string]worker.Prereq{
		"ppr-informer-synced": worker.HasSyncedPrereq(deps.pprInformer.Get().HasSynced),
	}

	for shardNumber, podInformer := range podInformers {
		prereqs[fmt.Sprintf("pod-informer-%d-synced", shardNumber)] = worker.HasSyncedPrereq(podInformer.HasSynced)
	}

	queue.SetExecutor(
		func(ctx context.Context, item pprutil.PodProtectorKey) error {
			return reconcile(ctx, args, options, deps, queue, &state.caches, item)
		},
		prereqs,
	)
	queue.SetBeforeStart(deps.elector.Get().Await)

	deps.pprInformer.Get().AddPostHandler(func(key pprutil.PodProtectorKey) {
		queue.EnqueueDelayed(key, jitterAggregationRate(options.aggRateJitter, *deps.defaultConfig.Get().AggregationRate))
	})

	return state, nil
}

func jitterAggregationRate(jitter [2]*float64, configValue time.Duration) time.Duration {
	lerp := rand.Float64()
	multiplier := *jitter[0] + (*jitter[1]-*jitter[0])*lerp

	return time.Duration(float64(configValue) * multiplier)
}

func newPodInformerShard(
	ctx context.Context,
	options ControllerOptions,
	deps ControllerDeps,
	shardNumber int,
	podIndex Sets,
) (_zero podInformerShard, _ cache.SharedIndexInformer, _ error) {
	informerSyncInitialMarker, informerSyncNotifier, informerSyncReader := synctime.New(deps.syncTimeAlgo.Get())

	nextEventPool := newNextEventPool()

	podInformer, podLister, err := createPodInformer(
		ctx,
		options,
		deps,
		shardNumber,
		podIndex,
		informerSyncInitialMarker,
		informerSyncNotifier,
		nextEventPool,
	)
	if err != nil {
		return _zero, nil, errors.TagWrapf("CreatePodInformer", err, "start PodProtector informer")
	}

	return podInformerShard{
		podLister:             podLister,
		informerSyncReader:    informerSyncReader,
		notifyOnInformerEvent: nextEventPool,
	}, podInformer, nil
}

//revive:disable-next-line:argument-limit // cannot reasonably reduce
func createPodInformer(
	ctx context.Context,
	options ControllerOptions,
	deps ControllerDeps,
	shardNumber int,
	podIndex Sets,
	markInitialInformerSync synctime.InitialMarker,
	notifyInformerSync synctime.Notifier,
	nextEventPool *nextEventPool,
) (_podInformer cache.SharedIndexInformer, _ corev1listers.PodLister, _ error) {
	defaultConfig := deps.defaultConfig.Get()
	workerCluster := deps.workerClient.Get()
	queue := deps.worker.Get()
	obs := deps.observer.Get()

	// Do not use the shared pod informer here to avoid transformation corrupting global view.
	podStore := cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	}

	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: nil,
			ListWithContextFunc: func(ctx context.Context, listOptions metav1.ListOptions) (runtime.Object, error) {
				configureListOptions(&listOptions, options, shardNumber)

				list, err := workerCluster.NativeClientSet().
					CoreV1().
					Pods(workerCluster.TargetNamespace()).
					List(ctx, listOptions)
				if err != nil {
					//nolint:wrapcheck // lifted from NewFilteredPodInformer
					return nil, err
				}

				if len(list.Items) == 0 {
					markInitialInformerSync()
				}

				return list, nil
			},
			WatchFunc: nil,
			WatchFuncWithContext: func(ctx context.Context, listOptions metav1.ListOptions) (watch.Interface, error) {
				configureListOptions(&listOptions, options, shardNumber)

				return workerCluster.NativeClientSet().
					CoreV1().
					Pods(workerCluster.TargetNamespace()).
					Watch(ctx, listOptions)
			},
			DisableChunking: false,
		},
		&corev1.Pod{},
		*options.podRelistPeriod,
		podStore,
	)

	if err := informer.SetTransform(func(obj any) (any, error) {
		if pod, isPod := obj.(*corev1.Pod); isPod {
			pod.Spec = corev1.PodSpec{} // avoid keeping unused pod spec in memory
		}
		return obj, nil
	}); err != nil {
		return nil, nil, errors.TagWrapf("SetTransform", err, "set pod transformation")
	}

	_, err := informer.AddEventHandler(&podEventHandler{
		ctx:                ctx,
		observer:           obs,
		defaultConfig:      defaultConfig,
		aggRateJitter:      options.aggRateJitter,
		podIndex:           podIndex,
		pprInformer:        deps.pprInformer.Get(),
		queue:              queue,
		notifyInformerSync: notifyInformerSync,
		nextEventPool:      nextEventPool,
	})
	if err != nil {
		return nil, nil, errors.TagWrapf(
			"AddPodEventHandler",
			err,
			"add event handler to Pod informer",
		)
	}

	return informer, corev1listers.NewPodLister(informer.GetIndexer()), nil
}

func configureListOptions(listOptions *metav1.ListOptions, options ControllerOptions, shardNumber int) {
	listOptions.LabelSelector = (*options.podLabelSelector).String()

	if *options.podInformerShards > 1 {
		listOptions.ShardingIndex = int64(shardNumber)
		listOptions.ShardingCount = int64(*options.podInformerShards)
		listOptions.ShardingLabelKey = sharding.DefaultInformerShardingLabelKey
	}
}

type podEventHandler struct {
	//nolint:containedctx // cannot pass context through event handler
	ctx context.Context

	observer observer.Observer

	defaultConfig *defaultconfig.Options
	aggRateJitter [2]*float64

	podIndex    Sets
	pprInformer pprutil.IndexedInformer

	queue              worker.Api[pprutil.PodProtectorKey]
	notifyInformerSync synctime.Notifier
	nextEventPool      *nextEventPool
}

func (handler *podEventHandler) OnAdd(obj any, _ bool) {
	handler.handle(handler.ctx, obj, true)
}

func (handler *podEventHandler) OnUpdate(_, newObj any) {
	handler.handle(handler.ctx, newObj, true)
}

func (handler *podEventHandler) OnDelete(obj any) {
	handler.handle(handler.ctx, obj, false)
}

//revive:disable-next-line:flag-parameter // stillPresent indicates whether the object exists, which does not leak control logic
func (handler *podEventHandler) handle(ctx context.Context, obj any, stillPresent bool) {
	if del, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = del.Obj
	}

	pod, isPod := obj.(*corev1.Pod)
	if !isPod {
		handler.observer.EnqueueError(ctx, observer.EnqueueError{
			Namespace: "",
			Name:      "",
			Err:       errors.TagErrorf("EventNotPod", "event object is not a Pod"),
		})

		return
	}

	podNsName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}

	ctx, cancelFunc := handler.observer.StartEnqueue(ctx, observer.StartEnqueue{
		Namespace: podNsName.Namespace,
		Name:      podNsName.Name,
		Kind:      "Pod",
	})
	defer cancelFunc()

	defer handler.observer.EndEnqueue(ctx, observer.EndEnqueue{})

	if stillPresent {
		handler.podIndex.Track(podNsName, pod.Labels)
	} else {
		handler.podIndex.Untrack(podNsName)
	}

	if err := handler.notifyInformerSync(pod); err != nil {
		handler.observer.EnqueueError(ctx, observer.EnqueueError{
			Namespace: podNsName.Namespace,
			Name:      podNsName.Name,
			Err:       err,
		})

		return
	}

	matchedPprs := handler.pprInformer.Query(pod.Namespace, pod.Labels)

	triggerReconcile, objectLatency, timeSinceLastDrain := handler.nextEventPool.Drain()
	handler.observer.NextEventPoolSingleDrain(
		ctx,
		observer.NextEventPoolSingleDrain{
			Size:               triggerReconcile.Len(),
			ObjectLatency:      objectLatency,
			TimeSinceLastDrain: timeSinceLastDrain,
		},
	)

	for _, ppr := range matchedPprs {
		triggerReconcile.Insert(ppr)
	}

	for nsName := range triggerReconcile {
		pprConfig := optional.None[podseidonv1a1.AdmissionHistoryConfig]()

		ppr, err := handler.pprInformer.Get(nsName)
		if err == nil {
			pprConfig = optional.Map(
				ppr,
				func(ppr *podseidonv1a1.PodProtector) podseidonv1a1.AdmissionHistoryConfig {
					return ppr.Spec.AdmissionHistoryConfig
				},
			)
		}

		computedConfig := handler.defaultConfig.Compute(pprConfig)
		handler.queue.EnqueueDelayed(nsName, jitterAggregationRate(handler.aggRateJitter, computedConfig.AggregationRate))
	}
}

func reconcile(
	ctx context.Context,
	args ControllerArgs,
	options ControllerOptions,
	deps ControllerDeps,
	queue worker.Api[pprutil.PodProtectorKey],
	caches *Caches,
	item pprutil.PodProtectorKey,
) error {
	obs := deps.observer.Get()

	reconcileCtx, cancelFunc := obs.StartReconcile(
		ctx,
		observer.StartReconcile{
			Namespace: item.Namespace,
			Name:      item.Name,
		},
	)
	defer cancelFunc()

	status := tryReconcile(
		reconcileCtx,
		reconcileOptions{
			myCellId:      *options.cellId,
			defaultConfig: deps.defaultConfig.Get(),
			clk:           args.Clock,
			pprSelector:   *options.pprLabelSelector,
		},
		obs,
		queue,
		caches,
		item,
	)

	obs.EndReconcile(reconcileCtx, status)

	return status.Err
}

type reconcileOptions struct {
	myCellId      string
	defaultConfig *defaultconfig.Options
	clk           clock.Clock
	pprSelector   labels.Selector
}

//nolint:cyclop // flow is mostly linear; abstracting code to functions isn't going to significantly improve readability.
func tryReconcile(
	ctx context.Context,
	options reconcileOptions,
	obs observer.Observer,
	queue worker.Api[pprutil.PodProtectorKey],
	caches *Caches,
	queueItem pprutil.PodProtectorKey,
) observer.EndReconcile {
	pprOpt, err := caches.pprInformer.Get(queueItem)
	if err != nil {
		return observer.EndReconcile{
			Err:       errors.TagWrapf("PprListerGet", err, "get PodProtector from lister"),
			HasChange: haschange.New[observer.StatusChangeCause](),
			Action:    "",
		}
	}

	ppr, hasPpr := pprOpt.Get()
	if !hasPpr {
		// nothing to process
		return observer.EndReconcile{
			Err:       nil,
			HasChange: haschange.New[observer.StatusChangeCause](),
			Action:    observer.ReconcileActionNoPpr,
		}
	}

	if !options.pprSelector.Matches(labels.Set(ppr.Labels)) {
		// skipped due to selector
		return observer.EndReconcile{
			Err:       nil,
			HasChange: haschange.New[observer.StatusChangeCause](),
			Action:    observer.ReconcileActionSelectorMismatch,
		}
	}

	ppr = ppr.DeepCopy()

	relevantPods, err := findRelevantPods(caches, ppr)
	if err != nil {
		return observer.EndReconcile{
			Err:       err,
			HasChange: haschange.New[observer.StatusChangeCause](),
			Action:    "",
		}
	}

	status := util.GetOrAppendSliceWith(
		&ppr.Status.Cells,
		func(status *podseidonv1a1.PodProtectorCellStatus) bool { return status.CellId == options.myCellId },
		func() podseidonv1a1.PodProtectorCellStatus {
			return podseidonv1a1.PodProtectorCellStatus{CellId: options.myCellId}
		},
	)

	requeue := optional.None[time.Duration]()
	hasChange := haschange.New[observer.StatusChangeCause]()

	if err := aggregateStatus(ctx, options.clk, obs, relevantPods, ppr, &requeue, &status.Aggregation, &hasChange); err != nil {
		return observer.EndReconcile{
			Err:       err,
			HasChange: haschange.New[observer.StatusChangeCause](),
			Action:    "",
		}
	}

	lastEventTimeByShard := make([]time.Time, len(caches.podInformerShards))

	for shardNumber, shard := range caches.podInformerShards {
		lastEventTime, hasLastEventTime := shard.informerSyncReader().Get()
		if !hasLastEventTime {
			return observer.EndReconcile{
				Err: errors.TagErrorf(
					"NoInformerSyncTime",
					"did not receive prior informer sync time",
				),
				HasChange: haschange.New[observer.StatusChangeCause](),
				Action:    "",
			}
		}

		lastEventTimeByShard[shardNumber] = lastEventTime
	}

	minLastEventTime := iter.FromSlice(lastEventTimeByShard).Extremum(time.Time.After).MustGet("number of shards is nonzero")

	updateLastEventTime(
		caches, queueItem, status, &hasChange,
		lastEventTimeList{
			shards: lastEventTimeByShard,
			min:    minLastEventTime,
		},
	)

	if hasChange.HasChanged() {
		status.Aggregation.LastEventTime.Time = minLastEventTime
	}

	if requeue, shouldRequeue := requeue.Get(); shouldRequeue {
		queue.EnqueueDelayed(queueItem, requeue)
	}

	if hasChange.HasChanged() {
		computedConfig := options.defaultConfig.Compute(optional.Some(ppr.Spec.AdmissionHistoryConfig))
		pprutil.Summarize(computedConfig, ppr)

		err := caches.pprSourceProvider.
			UpdateStatus(ctx, queueItem.SourceName, ppr)
		if err != nil {
			return observer.EndReconcile{
				Err: errors.TagWrapf(
					"UpdatePprStatus",
					err,
					"apiserver error while updating aggregation status",
				),
				HasChange: hasChange,
				Action:    "",
			}
		}
	}

	return observer.EndReconcile{
		Err:       nil,
		HasChange: hasChange,
		Action:    observer.ReconcileActionUpdated,
	}
}

func findRelevantPods(caches *Caches, ppr *podseidonv1a1.PodProtector) ([]*corev1.Pod, error) {
	podNames, err := caches.podIndex.Query(labelindex.NamespacedQuery[metav1.LabelSelector]{
		Namespace: ppr.Namespace,
		Query:     pprutil.GetAggregationSelector(ppr),
	})
	if err != nil {
		return nil, errors.TagWrapf(
			"QueryPodIndex",
			err,
			"query pods matching PodProtector selector from index",
		)
	}

	relevantPods := []*corev1.Pod{}

	if err := podNames.TryForEach(func(podName types.NamespacedName) error {
		pod, err := caches.findPodFromAnyShard(podName)
		if err != nil {
			return errors.TagWrapf("PodListerGet", err, "get pod from lister")
		}

		if pod != nil && pod.DeletionTimestamp.IsZero() {
			// Possible race condition: store is updated but event handler is not called yet to untrack the pod
			relevantPods = append(relevantPods, pod)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return relevantPods, nil
}

func aggregateStatus(
	ctx context.Context,
	clk clock.Clock,
	obs observer.Observer,
	relevantPods []*corev1.Pod,
	ppr *podseidonv1a1.PodProtector,
	requeue *optional.Optional[time.Duration],
	target *podseidonv1a1.PodProtectorAggregation,
	changed *haschange.Changed[observer.StatusChangeCause],
) error {
	if len(relevantPods) > math.MaxInt32 {
		return errors.TagErrorf("TooManyRelevantPods", "more than 2^31 pods")
	}

	// #nosec G115 -- len(relevantPods) <= MaxInt32 checked above
	totalReplicas := int32(len(relevantPods))
	haschange.Assign(changed, &target.TotalReplicas, totalReplicas, observer.StatusChangeCauseCreated)

	readyReplicas := int32(0)
	scheduledReplicas := int32(0)
	runningReplicas := int32(0)
	availableReplicas := int32(0)

	for _, pod := range relevantPods {
		if isPodConditionTrue(pod, corev1.PodReady) {
			readyReplicas++
		}

		if isPodConditionTrue(pod, corev1.PodScheduled) {
			scheduledReplicas++
		}

		if pod.Status.Phase == corev1.PodRunning {
			runningReplicas++
		}

		if isPodAvailable(clk, pod, ppr.Spec.MinReadySeconds, requeue) {
			availableReplicas++
		}
	}

	haschange.Assign(changed, &target.ReadyReplicas, readyReplicas, observer.StatusChangeCauseReady)
	haschange.Assign(changed, &target.ScheduledReplicas, scheduledReplicas, observer.StatusChangeCauseScheduled)
	haschange.Assign(changed, &target.RunningReplicas, runningReplicas, observer.StatusChangeCauseRunning)
	haschange.Assign(changed, &target.AvailableReplicas, availableReplicas, observer.StatusChangeCauseAvailable)

	obs.Aggregated(ctx, observer.Aggregated{
		NumPods:           len(relevantPods),
		ReadyReplicas:     readyReplicas,
		ScheduledReplicas: scheduledReplicas,
		RunningReplicas:   runningReplicas,
		AvailableReplicas: availableReplicas,
	})

	return nil
}

func isPodConditionTrue(
	pod *corev1.Pod,
	conditionType corev1.PodConditionType,
) bool {
	return iter.Any(iter.Map(
		iter.FromSlice(pod.Status.Conditions),
		func(condition corev1.PodCondition) bool {
			return condition.Type == conditionType && condition.Status == corev1.ConditionTrue
		},
	))
}

// Tests if a pod is available.
// Sets requeue to a shorter period if the availability state is going to change.
func isPodAvailable(
	clk clock.Clock,
	pod *corev1.Pod,
	minReadySeconds int32,
	requeue *optional.Optional[time.Duration],
) bool {
	if !pod.DeletionTimestamp.IsZero() {
		return false // terminating
	}

	readyConditionIndex := util.FindInSliceWith(
		pod.Status.Conditions,
		func(condition corev1.PodCondition) bool { return condition.Type == corev1.PodReady },
	)
	if readyConditionIndex == -1 {
		return false // readiness unknown
	}

	readyCondition := pod.Status.Conditions[readyConditionIndex]
	if readyCondition.Status != corev1.ConditionTrue {
		return false // not ready
	}

	readyTime := clk.Since(readyCondition.LastTransitionTime.Time)
	minReadyDuration := time.Duration(minReadySeconds) * time.Second

	if readyTime < minReadyDuration {
		readyIn := minReadyDuration - readyTime
		requeue.SetOrFn(
			readyIn,
			func(base, increment time.Duration) time.Duration { return min(base, increment) },
		)

		return false
	}

	return true
}

// Clean up obsolete admission history observed by the current aggregation.
//
//nolint:gocognit // prefer laying out all branches explicitly for clarity of each scenario.
func updateLastEventTime(
	caches *Caches,
	queueItem pprutil.PodProtectorKey,
	outputStatus *podseidonv1a1.PodProtectorCellStatus,
	outputChanged *haschange.Changed[observer.StatusChangeCause],
	lastEventTime lastEventTimeList,
) {
	changed := false
	newBuckets := []podseidonv1a1.PodProtectorAdmissionBucket{}

	shardHasOutstandingBuckets := make([]bool, len(caches.podInformerShards))

	for _, bucket := range outputStatus.History.Buckets {
		//nolint:nestif // Keep an explicit decision tree for all cases of is_compact * is_obsolete * is_name_aggregated
		if bucket.PodUid != nil {
			// Use pod name to determine the informer shard to retrieve lastEventTime from.
			// If there is only one shard, list.get() always returns the same value, so pod name has no effect.
			// If there are multiple shards, a pod event can only be compacted
			// if an event is received from its corresponding informer shard;
			// if pod name is missing in such a scenario, the oldest event time is assumed,
			// which may be inaccurate since the reflector for this pod may not have synced this event yet
			// despite another reflector having synced.
			podName := optional.IfNonZero(bucket.PodName)
			shardNumber := optional.Map(podName, caches.getShardNumber)

			// Single-pod bucket.
			if bucket.StartTime.Time.Before(lastEventTime.get(shardNumber)) {
				// Obsoleted by the current aggregation.
				// Do not copy to the new bucket list, no matter pod is aggregated or not.
				changed = true
			} else {
				// The effect of this admission has not been observed by this aggregation yet.
				//
				// If the pod is part of this aggregation, it is potentially deleted,
				// so the admission history will indicate its possible deletion.
				//
				// If the pod is not part of this aggregation,
				// this means it is a new pod that got created and
				// quickly deleted again before it gets caught by aggregator.
				// This is unexpected since watch lag is usually much shorter than
				// the time a pod takes to become ready,
				// so for simplicity we do not treat it specially.
				newBuckets = append(newBuckets, *bucket.DeepCopy())

				if shardNumber, isSome := shardNumber.Get(); isSome {
					// This outstanding bucket can be cleaned up when the shard is updated.
					shardHasOutstandingBuckets[shardNumber] = true
				} else {
					// We don't know which shard this bucket belongs to,
					// so any informer event that brings the shard from Before(bucket.StartTime) to After
					// may resolve this shard.

					for shardNumber := range len(lastEventTime.shards) {
						if lastEventTime.shards[shardNumber].Before(bucket.StartTime.Time) {
							shardHasOutstandingBuckets[shardNumber] = true
						}
					}
				}
			}
		} else {
			// Compacted bucket.
			if bucket.EndTime.Time.Before(lastEventTime.min) {
				// The entire bucket is obsoleted by the current aggregation.
				// Do not copy to the new bucket list.
				changed = true
			} else {
				// Some or all of the bucket is not covered by the current aggregation.
				// Produce an observer event that warns about this.
				// This indicates one of the following possible issues:
				// - informer sync time algorithm is inaccurate (e.g. due to clock skew)
				// - CompactThreshold is set too small
				newBuckets = append(newBuckets, *bucket.DeepCopy())

				for shardNumber := range len(lastEventTime.shards) {
					if lastEventTime.shards[shardNumber].Before(bucket.StartTime.Time) {
						shardHasOutstandingBuckets[shardNumber] = true
					}
				}
			}
		}
	}

	if changed {
		outputStatus.History.Buckets = newBuckets
		outputChanged.Add(observer.StatusChangeCauseHistoryBucketAggregated)
	}

	if len(newBuckets) > 0 {
		// There are still outstanding buckets, wait for the next watch event.

		for shardNumber, shard := range caches.podInformerShards {
			if shardHasOutstandingBuckets[shardNumber] {
				shard.notifyOnInformerEvent.Push(queueItem)
			}
		}
	}
}

type lastEventTimeList struct {
	shards []time.Time
	min    time.Time
}

func (list lastEventTimeList) get(shardNumber optional.Optional[int]) time.Time {
	if shardNumber, isSome := shardNumber.Get(); isSome {
		return list.shards[shardNumber]
	}

	return list.min
}
