package _tools

// Stub imports to avoid `go mod tidy` from clearing them up

import (
	_ "github.com/daixiang0/gci/cmd/gci"
	_ "github.com/itchyny/gojq/cli"
	_ "github.com/segmentio/golines"
	_ "golang.org/x/tools/cmd/goimports"
	_ "k8s.io/code-generator/cmd/client-gen/args"
	_ "k8s.io/code-generator/cmd/deepcopy-gen/args"
	_ "k8s.io/code-generator/cmd/informer-gen/args"
	_ "k8s.io/code-generator/cmd/lister-gen/args"
	_ "mvdan.cc/gofumpt"
	_ "sigs.k8s.io/controller-tools/pkg/crd"
)
