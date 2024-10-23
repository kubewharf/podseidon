#!/bin/ash

set -euo pipefail
set -x

cd /etc/test-assets

setup_cluster() {
    set -euo pipefail
    set -x

    local cluster=$1
    local apiserver_port=$2
    local etcd_port=$3

    if [ ! -f kwok-cluster-${cluster}.yaml ]; then
        sed "s/CLUSTER_NAME/${cluster}/g" kwok-cluster-worker.yaml >kwok-cluster-${cluster}.yaml
    fi

    KUBECONFIG=$PWD/${cluster}-kubeconfig.host.yaml \
        kwokctl create cluster --config /etc/test-assets/kwok-cluster-${cluster}.yaml \
        --name ${cluster} --kube-apiserver-port $apiserver_port --etcd-port $((etcd_port))

    # Wait for apiservers ready
    local start_poll_time=$(date +%s)
    while ! KUBECONFIG=${cluster}-kubeconfig.host.yaml kubectl get ns; do
        sleep 0.1

        if [ $((start_poll_time + 30)) -lt $(date +%s) ]; then
            echo "Timeout waiting for $cluster to become ready"
            exit 1
        fi
    done

    KUBECONFIG=${cluster}-kubeconfig.host.yaml kubectl config view --raw=true --flatten=true --minify=true >${cluster}-kubeconfig.yaml
    sed -i "s#https://127.0.0.1:4000.#https://kwok-${cluster}-kube-apiserver:6443#g" ${cluster}-kubeconfig.yaml
}

# Setup Kubernetes clusters with kwok
setup_clusters() {
    setup_cluster core 40000 40001 &
    setup_cluster worker-1 40002 40003 &
    setup_cluster worker-2 40004 40005 &
    wait
}

install_core_chart() {
    set -euo pipefail
    set -x

    jq \
        --rawfile crt webhook.pem \
        --rawfile key webhook-key.pem \
        '
            .webhook.tls.cert = $crt |
            .webhook.tls.key = $key |
            .release.core = true |
            .release.worker = false
        ' values.json >values-core.json
    KUBECONFIG=core-kubeconfig.host.yaml helm install podseidon chart.tgz -f values-core.json
}

install_worker_chart() {
    set -euo pipefail
    set -x

    local worker=$1

    jq \
        --arg worker worker-$worker \
        --rawfile crt webhook.pem \
        --rawfile key webhook-key.pem \
        '
            .webhook.tls.cert = $crt |
            .webhook.tls.key = $key |
            .release.core = false |
            .release.worker = true |
            .release.workerCellId = $worker
        ' values.json >values-worker-${worker}.json
    KUBECONFIG=worker-${worker}-kubeconfig.host.yaml helm install podseidon chart.tgz -f values-worker-${worker}.json

    KUBECONFIG=worker-${worker}-kubeconfig.host.yaml kubectl create -f kwok-node.yaml
    KUBECONFIG=worker-${worker}-kubeconfig.host.yaml kubectl create -f e2e-node.yaml

    # Override service endpoint to direct external endpoint because kwok cluster has no nodes to run webhook
    KUBECONFIG=worker-${worker}-kubeconfig.host.yaml \
        kubectl patch validatingwebhookconfiguration podseidon.kubewharf.io --type=json -p '[
            {
                "op": "remove",
                "path": "/webhooks/0/clientConfig/service"
            },
            {
                "op": "replace",
                "path": "/webhooks/0/clientConfig/url",
                "value": "https://podseidon-webhook:8843/worker-'${worker}'"
            }
        ]'
}

# All nodes are kwok nodes, so the effect of this chart is just to create the necessary resources to run out-of-cluster.
install_chart() {
    install_core_chart &
    install_worker_chart 1 &
    install_worker_chart 2 &

    wait
}

run_out_of_cluster() {
    docker run -d \
        -v=/etc/test-assets:/etc/test-assets \
        --network=kwok-core --network=kwok-worker-1 --network=kwok-worker-2 \
        --name=podseidon-generator \
        --restart=unless-stopped \
        podseidon-generator \
        --core-kube-config=/etc/test-assets/core-kubeconfig.yaml \
        --core-kube-impersonate-username="system:serviceaccount:default:podseidon-generator" \
        --klog-v=6 \
        --healthz-bind-addr=0.0.0.0 --healthz-port=8081 \
        --pprof-bind-addr=0.0.0.0 --pprof-port=6060 \
        --prometheus-http-bind-addr=0.0.0.0 --prometheus-http-port=9090

    for worker in 1 2; do
        docker run -d \
            -v /etc/test-assets:/etc/test-assets \
            --network=kwok-core --network=kwok-worker-1 --network=kwok-worker-2 \
            --name=podseidon-aggregator-${worker} \
            --restart=unless-stopped \
            podseidon-aggregator \
            --core-kube-config=/etc/test-assets/core-kubeconfig.yaml \
            --core-kube-impersonate-username="system:serviceaccount:default:podseidon-aggregator" \
            --worker-kube-config=/etc/test-assets/worker-${worker}-kubeconfig.yaml \
            --worker-kube-impersonate-username="system:serviceaccount:default:podseidon-aggregator" \
            --aggregator-cell-id=worker-${worker} \
            --aggregator-podprotector-label-selector="aggregator-ignore-ppr!=true" \
            --klog-v=6 \
            --healthz-bind-addr=0.0.0.0 --healthz-port=8081 \
            --pprof-bind-addr=0.0.0.0 --pprof-port=6060 \
            --prometheus-http-bind-addr=0.0.0.0 --prometheus-http-port=9090
    done

    docker run -d \
        -v=/etc/test-assets:/etc/test-assets \
        -p=8843:8843 \
        --network=kwok-core --network=kwok-worker-1 --network=kwok-worker-2 \
        --name=podseidon-webhook \
        --restart=unless-stopped \
        podseidon-webhook \
        --core-kube-config=/etc/test-assets/core-kubeconfig.yaml \
        --core-kube-impersonate-username="system:serviceaccount:default:podseidon-webhook" \
        --webhook-enable=false \
        --webhook-https-enable \
        --webhook-https-cert-file=/etc/test-assets/webhook.pem \
        --webhook-https-key-file=/etc/test-assets/webhook-key.pem \
        --klog-v=6 \
        --healthz-bind-addr=0.0.0.0 --healthz-port=8081 \
        --pprof-bind-addr=0.0.0.0 --pprof-port=6060 \
        --prometheus-http-bind-addr=0.0.0.0 --prometheus-http-port=9090 \
        --webhook-https-bind-addr=0.0.0.0 --webhook-https-port=8843
}

install_kelemetry() {
    # Adopted from kelemetry quickstart.docker-compose.yaml

    docker network create kelemetry

    docker run -d \
        --network=kelemetry \
        --name=kelemetry-etcd \
        --restart=unless-stopped \
        --entrypoint=etcd \
        -v=/var/run/etcd/default.etcd \
        $KELEMETRY_ETCD_IMAGE \
        -name=main \
        -advertise-client-urls=http://kelemetry-etcd:2379 \
        -listen-client-urls=http://0.0.0.0:2379 \
        -initial-advertise-peer-urls=http://kelemetry-etcd:2380 \
        -listen-peer-urls=http://0.0.0.0:2380 \
        -initial-cluster-state=new \
        -initial-cluster=main=http://kelemetry-etcd:2380 \
        -initial-cluster-token=etcd-cluster-1 \
        -data-dir=/var/run/etcd/default.etcd

    docker run -d \
        --network=kelemetry \
        --name=kelemetry-jaeger-query \
        -e=GRPC_STORAGE_SERVER=kelemetry-core:17271 \
        -e=SPAN_STORAGE_TYPE=grpc-plugin \
        -p=16686:16686 \
        --restart=unless-stopped \
        $KELEMETRY_JAEGER_QUERY_IMAGE

    docker run -d \
        --network=kelemetry \
        --name=kelemetry-jaeger-collector \
        -e=COLLECTOR_OTLP_ENABLED=true \
        -e=SPAN_STORAGE_TYPE=grpc-plugin \
        -e=GRPC_STORAGE_SERVER=kelemetry-jaeger-remote-storage:17271 \
        --restart=unless-stopped \
        $KELEMETRY_JAEGER_COLLECTOR_IMAGE

    docker run -d \
        --network=kelemetry \
        --name=kelemetry-jaeger-remote-storage \
        -e=SPAN_STORAGE_TYPE=badger \
        --restart=unless-stopped \
        $KELEMETRY_JAEGER_REMOTE_STORAGE_IMAGE

    KUBE_CONFIG_PATHS=core=/etc/test-assets/core-kubeconfig.yaml,worker-1=/etc/test-assets/worker-1-kubeconfig.yaml,worker-2=/etc/test-assets/worker-2-kubeconfig.yaml

    docker run -d \
        --network=kelemetry \
        --network=kwok-core --network=kwok-worker-1 --network=kwok-worker-2 \
        --name=kelemetry-core \
        -v=/etc/test-assets:/etc/test-assets \
        -p=8888:8080 \
        --restart=unless-stopped \
        $KELEMETRY_ALLINONE_IMAGE \
        kelemetry \
        --audit-consumer-enable --audit-producer-enable --audit-webhook-enable \
        --diff-decorator-enable --annotation-linker-enable --owner-linker-enable \
        --mq=local --audit-consumer-partition=0,1,2,3 \
        --linker-worker-count=8 \
        --http-address=0.0.0.0 --http-port=8080 \
        --kube-target-cluster=core \
        --kube-target-rest-burst=100 --kube-target-rest-qps=100 \
        --kube-config-paths=$KUBE_CONFIG_PATHS \
        --diff-cache=etcd --diff-cache-etcd-endpoints=kelemetry-etcd:2379 --diff-cache-wrapper-enable \
        --event-informer-enable --diff-controller-enable \
        --diff-controller-leader-election-enable=false --event-informer-leader-election-enable=false \
        --span-cache=etcd --span-cache-etcd-endpoints=kelemetry-etcd:2379 \
        --tracer-otel-endpoint=kelemetry-jaeger-collector:4317 --tracer-otel-insecure \
        --jaeger-storage-plugin-enable --jaeger-redirect-server-enable \
        --jaeger-storage-plugin-address=0.0.0.0:17271 \
        --jaeger-cluster-names=core,worker-1,worker-2 \
        --jaeger-backend=jaeger-storage \
        --jaeger-storage.span-storage.type=grpc-plugin \
        --jaeger-storage.grpc-storage.server=kelemetry-jaeger-remote-storage:17271

    for worker in worker-1 worker-2; do
        docker run -d \
            --network=kelemetry \
            --network=kwok-core --network=kwok-worker-1 --network=kwok-worker-2 \
            --name=kelemetry-controller-${worker} \
            -v=/etc/test-assets:/etc/test-assets \
            --restart=unless-stopped \
            $KELEMETRY_ALLINONE_IMAGE \
            kelemetry \
            --kube-target-cluster=${worker} \
            --kube-target-rest-burst=100 --kube-target-rest-qps=100 \
            --kube-config-paths=$KUBE_CONFIG_PATHS \
            --diff-cache=etcd --diff-cache-etcd-endpoints=kelemetry-etcd:2379 --diff-cache-wrapper-enable \
            --event-informer-enable --diff-controller-enable \
            --diff-controller-leader-election-enable=false --event-informer-leader-election-enable=false \
            --span-cache=etcd --span-cache-etcd-endpoints=kelemetry-etcd:2379 \
            --tracer-otel-endpoint=kelemetry-jaeger-collector:4317 --tracer-otel-insecure
    done
}

archive_kelemetry_traces() {
    mkdir /var/trace/outputs

    while read -r trace_file; do
        curl -sS -iG \
            --data-urlencode cluster="$(jq -r .cluster ${trace_file})" \
            --data-urlencode group="$(jq -r .group ${trace_file})" \
            --data-urlencode resource="$(jq -r .resource ${trace_file})" \
            --data-urlencode namespace="$(jq -r .namespace ${trace_file})" \
            --data-urlencode name="$(jq -r .name ${trace_file})" \
            --data-urlencode ts="$(jq -r .ts ${trace_file})" \
            -o /tmp/curl-output.http \
            localhost:8888/redirect

        if ! (head -n1 /tmp/curl-output.http | grep "302 Found"); then
            echo "Warning: No trace found for $trace_file"
            cat $trace_file
            touch /var/trace/error
            continue
        fi

        full_trace_id=$(grep '^Location: /trace/' /tmp/curl-output.http | cut -d/ -f3 | tr -d '\r')
        if [ "x$full_trace_id" == x ]; then
            echo "Warning: No trace found for $trace_file"
            cat $trace_file
            touch /var/trace/error
            continue
        fi

        fixed_id=${full_trace_id:10}
        full_tree_trace_id="ff21000000${fixed_id}"

        curl -sS -o /var/trace/outputs/${full_tree_trace_id} localhost:16686/api/traces/${full_tree_trace_id}

        link_path=/var/trace/outputs/"$(jq -r '.title | gsub(" "; "_") | gsub("/"; "-")' ${trace_file})"
        cp /var/trace/outputs/${full_tree_trace_id} "$link_path"
    done

    tar -czf /var/trace/traces.tar.gz -C /var/trace/outputs/ .
}

setup_clusters
install_chart
run_out_of_cluster

install_kelemetry
sleep $KELEMETRY_WAIT_WARMUP_SECONDS

mkdir -p /var/trace/targets

set +e
KELEMETRY_TRACE_TARGETS=/var/trace/targets \
    ginkgo --vv -p \
    --output-interceptor-mode=none \
    ./tests.test
if [ $? -ne 0 ]; then
    touch /var/trace/error
fi
set -e

sleep $KELEMETRY_WAIT_SPAN_SLEEP_SECONDS
find /var/trace/targets -type f | archive_kelemetry_traces /var/trace/targets

for component in generator aggregator-1 aggregator-2 webhook; do
    docker logs podseidon-${component} >/var/log/podseidon-${component}.stdout.log 2>/var/log/podseidon-${component}.stderr.log
    curl -o /var/log/podseidon-${component}.metrics.txt \
        $(docker inspect podseidon-${component} | jq -r '.[0].NetworkSettings.Networks | to_entries[0].value.IPAddress'):9090
done
