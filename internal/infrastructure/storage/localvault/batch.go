package localvault

import (
	"context"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/noteutil"
)

func (v *LocalVault) CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error) {
	return noteutil.RunBatch(inputs, func(in domain.CreateInput) (string, domain.MutationResult, error) {
		np, err := v.normalizePath(in.Path)
		if err != nil {
			return in.Path, domain.MutationResult{}, err
		}
		res, err := v.Create(ctx, domain.CreateInput{Path: np.Storage, FrontMatter: in.FrontMatter, Body: in.Body})
		return in.Path, res, err
	}), nil
}

func (v *LocalVault) UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error) {
	return noteutil.RunBatch(items, func(it domain.BatchUpdateBodyInput) (string, domain.MutationResult, error) {
		res, err := v.UpdateBody(ctx, it.Path, it.Body, it.IfMatch)
		return it.Path, res, err
	}), nil
}

func (v *LocalVault) PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error) {
	return noteutil.RunBatch(items, func(it domain.BatchPatchFrontMatterInput) (string, domain.MutationResult, error) {
		res, err := v.PatchFrontMatter(ctx, it.Path, it.Fields, it.IfMatch)
		return it.Path, res, err
	}), nil
}
