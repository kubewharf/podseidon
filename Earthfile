VERSION 0.8

SETUP_SSH:
    FUNCTION

    RUN apk add --no-cache openssh git

    RUN [ -d ~/.ssh ] || mkdir ~/.ssh
    # Fingerprint reference: https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints
    RUN echo 'github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl' >>~/.ssh/known_hosts
    RUN echo 'github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=' >>~/.ssh/known_hosts
    RUN echo 'github.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=' >>~/.ssh/known_hosts

    RUN git config --global url."git@github.com:".insteadOf "https://github.com/"

golang-base:
    ARG golang_image='golang:1.25-alpine3.22'
    FROM ${golang_image}

    ARG GOPROXY='https://proxy.golang.org,direct'
    RUN go env -w GOPROXY=${GOPROXY}
    ARG GONOPROXY=''
    RUN go env -w GONOPROXY=${GONOPROXY}
    ARG GOPRIVATE=''
    RUN go env -w GOPRIVATE=${GOPRIVATE}
    DO +SETUP_SSH

    ARG with_apk
    FOR dep IN ${with_apk}
        RUN apk add --no-cache $dep
    END

    WORKDIR /src

tools-base:
    FROM +golang-base

    COPY --dir tools/go.mod tools/go.sum tools/go.work tools
    COPY --if-exists --dir tools/go.work.sum tools
    WORKDIR tools
    RUN --ssh go mod download

    COPY tools/boilerplate.txt /etc/boilerplate.txt

crd-gen:
    FROM +tools-base
    RUN go install -v sigs.k8s.io/controller-tools/cmd/controller-gen

    COPY apis /src/apis
    WORKDIR /src/apis
    RUN controller-gen crd \
        paths=./v1alpha1 \
        output:crd:dir=/src/crds

    SAVE ARTIFACT /src/crds AS LOCAL chart/crds

deepcopy-gen:
    FROM +tools-base
    RUN go install -v k8s.io/code-generator/cmd/deepcopy-gen

    COPY apis /src/apis
    WORKDIR /src/apis
    RUN deepcopy-gen \
        --bounding-dirs=v1alpha1 \
        --output-file=zz_generated.deepcopy.go \
        --go-header-file /etc/boilerplate.txt \
        github.com/kubewharf/podseidon/apis/v1alpha1
    SAVE ARTIFACT /src/apis/v1alpha1/zz_generated.deepcopy.go AS LOCAL apis/v1alpha1/zz_generated.deepcopy.go

client-gen:
    FROM +tools-base
    RUN go install -v k8s.io/code-generator/cmd/client-gen

    COPY apis /src/apis
    WORKDIR /src/apis
	RUN client-gen \
		--input=apis/v1alpha1 \
		--input-base=/src \
		--output-pkg=github.com/kubewharf/podseidon/client/clientset \
		--output-dir=/src/client/clientset \
		--clientset-name=versioned \
		--go-header-file /etc/boilerplate.txt
    SAVE ARTIFACT /src/client/clientset

lister-gen:
    FROM +tools-base
    RUN go install -v k8s.io/code-generator/cmd/lister-gen

    COPY apis /src/apis
    WORKDIR /src/apis
	RUN lister-gen \
		--output-pkg=github.com/kubewharf/podseidon/client/listers \
		--output-dir=/src/client/listers \
		--go-header-file /etc/boilerplate.txt \
		github.com/kubewharf/podseidon/apis/v1alpha1
    SAVE ARTIFACT /src/client/listers

informer-gen:
    FROM +tools-base
    RUN go install -v k8s.io/code-generator/cmd/informer-gen

    COPY apis /src/apis
    WORKDIR /src/apis
	RUN informer-gen \
		--output-pkg=github.com/kubewharf/podseidon/client/informers \
		--output-dir=/src/client/informers \
		--versioned-clientset-package=github.com/kubewharf/podseidon/client/clientset/versioned \
		--listers-package=github.com/kubewharf/podseidon/client/listers \
		--go-header-file /etc/boilerplate.txt \
		github.com/kubewharf/podseidon/apis/v1alpha1
    SAVE ARTIFACT /src/client/informers

code-generator:
    FROM +golang-base

    COPY +deepcopy-gen/zz_generated.deepcopy.go apis/v1alpha1/zz_generated.deepcopy.go
    COPY client/go.mod client/go.sum client/
    COPY +client-gen/clientset client/clientset
    COPY +lister-gen/listers client/listers
    COPY +informer-gen/informers client/informers

    SAVE ARTIFACT apis/v1alpha1/zz_generated.deepcopy.go AS LOCAL apis/v1alpha1/zz_generated.deepcopy.go
    SAVE ARTIFACT client AS LOCAL .

build-base:
    FROM +golang-base

    COPY --dir go.work .
    COPY --if-exists --dir go.work.sum .
    # Generate stub modules so that go.work can work without loading the code of unrelated modules into the image
    FOR mod IN apis client util generator aggregator webhook allinone tests
        RUN mkdir $mod && echo "module github.com/kubewharf/podseidon/$mod" >$mod/go.mod
    END

    RUN rm -r apis
    COPY apis apis
    RUN --ssh cd apis && go mod download

    RUN rm -r client
    COPY +deepcopy-gen/zz_generated.deepcopy.go apis/v1alpha1/zz_generated.deepcopy.go
    COPY +code-generator/client client
    RUN --ssh cd client && go mod download

    RUN rm -r util
    COPY util util
    RUN --ssh cd util && go mod download

runtime:
    ARG runtime_image='alpine:3.22'
    FROM ${runtime_image}

build-bin:
    FROM +build-base

    ARG --required module
    ARG depends=''

    CACHE --id go-cache --sharing shared /root/.cache

    FOR dir IN $module $depends
        RUN rm -r $dir || true
        COPY $dir $dir
    END

    RUN --ssh cd $module && go mod download
    RUN go build -v -o output/podseidon-$module ./$module
    SAVE ARTIFACT output/podseidon-$module AS LOCAL output/podseidon-$module

build-image:
    FROM +runtime

    ARG module

    COPY (+build-bin/podseidon-$module --module=$module) /usr/local/bin/app

    WORKDIR /app
    ENTRYPOINT ["app"]

    ARG output_image_prefix='ghcr.io/kubewharf/podseidon-'
    ARG output_tag='latest'
    SAVE IMAGE --push ${output_image_prefix}${module}:${output_tag}

# Generate all files that have to be committed on Git.
generate:
    BUILD +crd-gen
    BUILD +code-generator

# Build all artifacts into the output directory.
build:
    BUILD +build-bin --module=generator
    BUILD +build-image --module=generator
    BUILD +build-bin --module=aggregator
    BUILD +build-image --module=aggregator
    BUILD +build-bin --module=webhook
    BUILD +build-image --module=webhook
    BUILD +build-bin --module=allinone --depends='generator aggregator webhook'


fmt-base:
    FROM +tools-base
    WAIT
        RUN go install mvdan.cc/gofumpt
        RUN go install github.com/segmentio/golines
        RUN go install golang.org/x/tools/cmd/goimports
        RUN go install github.com/daixiang0/gci
    END

fmt-work:
    FROM +fmt-base

    ARG --required fmt_modules

    FOR module IN $fmt_modules
        COPY --dir $module /work/$module
    END
    WORKDIR /work

	RUN gofumpt -l -w .
    RUN golines --base-formatter=gofumpt -w . -m 140
    RUN goimports -l -w .
    RUN gci write \
        -s standard \
        -s default \
        -s 'prefix(section/1,github.com/kubewharf/podseidon/api,github.com/kubewharf/podseidon/client)' \
        -s 'prefix(section/2,github.com/kubewharf/podseidon/util)' \
        -s 'prefix(section/3,github.com/kubewharf/podseidon/generator,github.com/kubewharf/podseidon/aggregator,github.com/kubewharf/podseidon/webhook)' \
        -s 'prefix(section/4,github.com/kubewharf/podseidon)' \
        .

    FOR module IN $fmt_modules
        SAVE ARTIFACT /work/$module AS LOCAL $module
    END

fmt:
    BUILD +fmt-work --fmt_modules='apis client util generator aggregator webhook allinone tests'
    BUILD +tidy

tidy:
    LOCALLY
    ARG fmt_modules='tools apis client util generator aggregator webhook allinone tests'

    FOR module IN $fmt_modules
        RUN cd $module && go mod tidy
    END

lint:
    ARG golangci_lint_docker_image='golangci/golangci-lint:v2.4.0-alpine'
    FROM ${golangci_lint_docker_image}

    ARG GOPROXY='https://proxy.golang.org,direct'
    RUN go env -w GOPROXY=$GOPROXY
    ARG GONOPROXY=''
    RUN go env -w GONOPROXY=$GONOPROXY
    ARG GOPRIVATE=''
    RUN go env -w GOPRIVATE=${GOPRIVATE}
    DO +SETUP_SSH

    RUN go install github.com/itchyny/gojq/cmd/gojq@latest

    CACHE --id go-cache --sharing shared /root/.cache

    COPY --dir go.work /work/
    COPY --if-exists --dir go.work.sum /work/
    WORKDIR /work

    ARG dep_modules='apis client'
    ARG lint_modules='util generator aggregator webhook allinone tests'

    FOR module IN $dep_modules $lint_modules
        RUN mkdir $module && echo "module github.com/kubewharf/podseidon/${module}" >${module}/go.mod
    END

    FOR module IN $dep_modules $lint_modules
        COPY ${module}/go.mod ${module}/go.mod
        RUN --ssh cd ${module} && echo ${module} && go mod download
    END

    FOR module IN $dep_modules $lint_modules
        COPY ${module} ${module}
    END

    COPY .golangci.yaml .

    COPY tools/boilerplate.txt /etc/boilerplate.txt

    FOR module IN $lint_modules
        RUN gojq '.' \
            --yaml-input .golangci.yaml \
            >${module}/.golangci-before-customize.json

        IF [ -f ${module}/.golangci.jq ]
            RUN gojq -f ${module}/.golangci.jq ${module}/.golangci-before-customize.json >${module}/.golangci.json
        ELSE
            RUN gojq '.' ${module}/.golangci-before-customize.json >${module}/.golangci.json
        END

        RUN cd $module && echo $module && \
            golangci-lint run --timeout=15m
    END

test-base:
    FROM +build-base --with_apk='gcc musl-dev'

    CACHE /root/.cache/go-build

    ARG dep_modules=''
    ARG test_modules='util generator aggregator webhook allinone'

    FOR module IN $dep_modules $test_modules
        COPY --dir $module/go.mod $module/go.sum $module
    END

    FOR module IN $dep_modules $test_modules
        RUN --ssh cd $module && go mod download
    END

    FOR module IN $dep_modules $test_modules
        COPY $module $module
    END

test:
    FROM +test-base

    ARG test_modules='util generator aggregator webhook allinone'
    ARG test_run=''

    RUN CGO_ENABLED=1 go test -v -race -coverprofile=/var/coverage.out \
        -run "$test_run" \
        $(for module in $test_modules; do echo "./$module/..."; done)

    SAVE ARTIFACT /var/coverage.out AS LOCAL output/coverage.out

chart:
    FROM +golang-base

    RUN apk add --no-cache helm kubectl

    ARG app_version='v0.0.0-dev'
    ARG chart_version='0.0.0'

    COPY chart chart
    COPY +crd-gen/crds chart/crds
    RUN helm package --version="$chart_version" --app-version="$app_version" chart
    RUN mv *.tgz chart.tgz

    SAVE ARTIFACT chart.tgz AS LOCAL output/chart.tgz

chart-push:
    FROM +golang-base

    RUN apk add --no-cache helm

    ARG registry='ghcr.io'
    ARG path_prefix='kubewharf'

    COPY +chart/chart.tgz chart.tgz
    RUN --mount type=secret,target=/root/.docker/config.json,id=docker_config \
        helm push chart.tgz oci://${registry}/${path_prefix}

build-kwok:
    FROM +golang-base

    ARG kwok_version='v0.6.0'
    RUN --ssh go install sigs.k8s.io/kwok/cmd/kwok@${kwok_version}
    RUN --ssh go install sigs.k8s.io/kwok/cmd/kwokctl@${kwok_version}
    SAVE ARTIFACT /go/bin/kwok
    SAVE ARTIFACT /go/bin/kwokctl

webhook-pem:
    ARG cfssl_image='cfssl/cfssl:v1.6.5'
    FROM ${cfssl_image}
    WORKDIR /work

    COPY tests/assets/cfssl-config.json .
    COPY tests/assets/cfssl-ca-csr.json .
    RUN cfssl gencert -initca cfssl-ca-csr.json | cfssljson -bare root
    COPY tests/assets/cfssl-webhook-csr.json .
    RUN cfssl gencert -ca=root.pem -ca-key=root-key.pem \
        -config=cfssl-config.json -profile=webhook \
        cfssl-webhook-csr.json | cfssljson -bare webhook
    SAVE ARTIFACT webhook-key.pem
    SAVE ARTIFACT webhook.pem

build-e2e-tests:
    FROM +build-base

    CACHE /root/.cache/go-build

    RUN apk add --no-cache gcc musl-dev

    COPY tests/go.mod tests/go.sum tests/
    RUN --ssh cd tests && go mod download
    RUN --ssh go install github.com/onsi/ginkgo/v2/ginkgo

    ARG test_deps='./generator ./aggregator ./webhook'
    ARG test_mod='./tests'
    FOR module IN ${test_deps}
        COPY --dir ${module} .
    END
    COPY --dir ${test_mod} .
    RUN --ssh /go/bin/ginkgo build \
        --race --gcflags='all=-N -l' \
        ${test_mod}

    SAVE ARTIFACT /go/bin/ginkgo
    SAVE ARTIFACT tests/tests.test

build-dlv:
    FROM +build-base
    RUN --ssh go install github.com/go-delve/delve/cmd/dlv@latest
    SAVE ARTIFACT /go/bin/dlv

e2e:
    ARG dind_image='earthly/dind:alpine-3.20-docker-26.1.5-r0'
    FROM ${dind_image}

    RUN apk add --no-cache kubectl helm jq curl

    COPY +build-kwok/kwok +build-kwok/kwokctl /usr/local/bin/
    COPY tests/assets /etc/test-assets
    COPY +webhook-pem/webhook.pem /etc/test-assets/
    COPY +webhook-pem/webhook-key.pem /etc/test-assets/
    COPY +chart/chart.tgz /etc/test-assets/chart.tgz
    COPY +build-e2e-tests/ginkgo /usr/local/bin/
    COPY +build-e2e-tests/tests.test /etc/test-assets/

    ARG with_dlv='no'
    IF [ x${with_dlv} == xyes ]
        COPY +build-dlv/dlv /usr/local/bin/
    END

    RUN mkdir /var/trace

    ARG KWOK_KUBE_IMAGE_PREFIX='registry.k8s.io'
    ARG KWOK_IMAGE_PREFIX='registry.k8s.io/kwok'

    ARG KELEMETRY_ETCD_IMAGE='quay.io/coreos/etcd:v3.4.33'
    ARG KELEMETRY_JAEGER_QUERY_IMAGE='jaegertracing/jaeger-query:1.65.0'
    ARG KELEMETRY_JAEGER_COLLECTOR_IMAGE='jaegertracing/jaeger-collector:1.65.0'
    ARG KELEMETRY_JAEGER_REMOTE_STORAGE_IMAGE='jaegertracing/jaeger-remote-storage:1.65.0'
    ARG KELEMETRY_ALLINONE_IMAGE='ghcr.io/kubewharf/kelemetry:v0.2.7'
    ARG KELEMETRY_WARMUP_SLEEP='5s'
    ARG KELEMETRY_WAIT_SPAN_SLEEP='15s'
    ARG FAIL_FAST_FOR_INTERACTIVE='no' # If set to yes, fail fast and don't save artifacts on error

    WORKDIR /etc/test-assets

    WITH DOCKER \
            --load podseidon-generator=(+build-image --module=generator) \
            --load podseidon-aggregator=(+build-image --module=aggregator) \
            --load podseidon-webhook=(+build-image --module=webhook) \
            --pull $KWOK_IMAGE_PREFIX/kwok:v0.7.0 \
            --pull $KWOK_KUBE_IMAGE_PREFIX/etcd:3.5.11-0 \
            --pull $KWOK_KUBE_IMAGE_PREFIX/kube-apiserver:v1.33.1 \
            --pull $KWOK_KUBE_IMAGE_PREFIX/kube-controller-manager:v1.33.1 \
            --pull $KWOK_KUBE_IMAGE_PREFIX/kube-scheduler:v1.33.1 \
            --pull $KELEMETRY_ETCD_IMAGE \
            --pull $KELEMETRY_JAEGER_QUERY_IMAGE \
            --pull $KELEMETRY_JAEGER_COLLECTOR_IMAGE \
            --pull $KELEMETRY_JAEGER_REMOTE_STORAGE_IMAGE \
            --pull $KELEMETRY_ALLINONE_IMAGE

        RUN (ginkgo --vv --output-interceptor-mode=none -p --trace ./tests.test || touch /var/trace/error); \
            if [ x${FAIL_FAST_FOR_INTERACTIVE} == xyes ]; then \
                if [ -f /var/trace/error ]; then \
                    exit 1; \
                fi; \
            fi
    END

    WAIT
        SAVE ARTIFACT /var/test-output AS LOCAL output/test-output
    END

    ARG fail_on_error='yes'
    IF [ x${fail_on_error} == xyes ]
        RUN [ ! -f /var/trace/error ]
    END
