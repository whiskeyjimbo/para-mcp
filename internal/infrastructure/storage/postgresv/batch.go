package postgresv

import (
	"context"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/noteutil"
)

func (v *PostgresVault) CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error) {
	return noteutil.RunBatch(inputs, func(in domain.CreateInput) (string, domain.MutationResult, error) {
		res, err := v.Create(ctx, in)
		return in.Path, res, err
	}), nil
}

func (v *PostgresVault) UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error) {
	return noteutil.RunBatch(items, func(it domain.BatchUpdateBodyInput) (string, domain.MutationResult, error) {
		res, err := v.UpdateBody(ctx, it.Path, it.Body, it.IfMatch)
		return it.Path, res, err
	}), nil
}

func (v *PostgresVault) PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error) {
	return noteutil.RunBatch(items, func(it domain.BatchPatchFrontMatterInput) (string, domain.MutationResult, error) {
		res, err := v.PatchFrontMatter(ctx, it.Path, it.Fields, it.IfMatch)
		return it.Path, res, err
	}), nil
}
