package localvault

import (
	"context"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

func (v *LocalVault) CreateBatch(ctx context.Context, inputs []domain.CreateInput) (domain.BatchResult, error) {
	return runBatch(inputs, func(in domain.CreateInput) (string, domain.MutationResult, error) {
		np, err := v.normalizePath(in.Path)
		if err != nil {
			return in.Path, domain.MutationResult{}, err
		}
		res, err := v.Create(ctx, domain.CreateInput{Path: np.Storage, FrontMatter: in.FrontMatter, Body: in.Body})
		return in.Path, res, err
	}), nil
}

func (v *LocalVault) UpdateBodyBatch(ctx context.Context, items []domain.BatchUpdateBodyInput) (domain.BatchResult, error) {
	return runBatch(items, func(it domain.BatchUpdateBodyInput) (string, domain.MutationResult, error) {
		res, err := v.UpdateBody(ctx, it.Path, it.Body, it.IfMatch)
		return it.Path, res, err
	}), nil
}

func (v *LocalVault) PatchFrontMatterBatch(ctx context.Context, items []domain.BatchPatchFrontMatterInput) (domain.BatchResult, error) {
	return runBatch(items, func(it domain.BatchPatchFrontMatterInput) (string, domain.MutationResult, error) {
		res, err := v.PatchFrontMatter(ctx, it.Path, it.Fields, it.IfMatch)
		return it.Path, res, err
	}), nil
}

func runBatch[I any](items []I, fn func(I) (path string, res domain.MutationResult, err error)) domain.BatchResult {
	result := domain.BatchResult{Results: make([]domain.BatchItemResult, len(items))}
	for i, item := range items {
		path, res, err := fn(item)
		r := domain.BatchItemResult{Index: i, Path: path}
		if err != nil {
			r.Error = err.Error()
			result.FailureCount++
		} else {
			r.OK = true
			r.Summary = &res.Summary
			r.ETag = res.ETag
			result.SuccessCount++
		}
		result.Results[i] = r
	}
	return result
}
