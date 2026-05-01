package wirecheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/whiskeyjimbo/paras/internal/analysis/wirecheck"
)

func TestWirecheck(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), wirecheck.Analyzer, "a")
}
