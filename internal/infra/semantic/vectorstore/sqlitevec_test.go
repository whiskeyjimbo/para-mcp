package vectorstore_test

import (
	"context"
	"testing"

	"github.com/whiskeyjimbo/para-mcp/internal/infra/semantic/vectorstore"
)

func TestSqliteVecConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sqlite-vec integration test in short mode")
	}

	const dims = 4
	store, err := vectorstore.NewSqliteVec(context.Background(), vectorstore.SqliteVecConfig{
		DSN:  ":memory:",
		Dims: dims,
	})
	if err != nil {
		// sqlite-vec WASM/CGO setup may not be available in all CI environments.
		// See paras-nku-infra bead for tracking full conformance test enablement.
		t.Skipf("sqlite-vec unavailable (infrastructure): %v", err)
	}
	defer store.Close()

	runConformanceTests(t, store, dims)
}
