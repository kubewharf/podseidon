package tests_test

import (
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	gomegaformat "github.com/onsi/gomega/format"

	_ "github.com/kubewharf/podseidon/tests/aggregator"
	_ "github.com/kubewharf/podseidon/tests/generator"
	_ "github.com/kubewharf/podseidon/tests/webhook"
)

func TestSuite(t *testing.T) {
	gomegaformat.MaxLength = 0

	gomega.RegisterFailHandler(ginkgo.Fail)

	ginkgo.RunSpecs(t, "E2E tests")
}
