package vectorstore_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/whiskeyjimbo/para-mcp/internal/infra/semantic/vectorstore"
)

func TestPgvectorConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pgvector integration test in short mode")
	}

	ctx := context.Background()

	pgc, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Skipf("docker unavailable, skipping pgvector test: %v", err)
	}
	t.Cleanup(func() { pgc.Terminate(ctx) })

	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	_ = fmt.Sprintf("dsn: %s", dsn) // used below

	const dims = 4
	store, err := vectorstore.NewPgVector(ctx, vectorstore.PgVectorConfig{
		DSN:  dsn,
		Dims: dims,
	})
	if err != nil {
		t.Fatalf("NewPgVector: %v", err)
	}
	defer store.Close()

	runConformanceTests(t, store, dims)
}
