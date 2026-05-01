package s3vault

import (
	"bytes"
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/noteutil"
)

func (v *S3Vault) Create(ctx context.Context, in domain.CreateInput) (domain.MutationResult, error) {
	np, err := domain.Normalize(in.Path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	now := v.clock()
	in.FrontMatter.CreatedAt = now
	in.FrontMatter.UpdatedAt = now
	etag := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(in.FrontMatter), in.Body)

	data, err := noteutil.FormatNote(in.FrontMatter, in.Body)
	if err != nil {
		return domain.MutationResult{}, err
	}

	_, err = v.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(v.bucket),
		Key:         aws.String(v.objectKey(np.Storage)),
		Body:        bytes.NewReader(data),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return domain.MutationResult{}, domain.ErrConflict
		}
		return domain.MutationResult{}, err
	}

	note := domain.Note{
		Ref:         domain.NoteRef{Scope: v.scope, Path: np.Storage},
		FrontMatter: in.FrontMatter,
		Body:        in.Body,
		ETag:        etag,
	}
	result := domain.MutationResult{Summary: note.Summary(), ETag: etag}
	ik := noteutil.IndexKey(np.Storage, false)
	links := noteutil.ParseLinks(in.Body)
	v.cache.Set(ik, result.Summary)
	v.graph.Upsert(np.Storage, links)
	v.idx.Add(noteutil.SummaryToDoc(result.Summary, in.Body))
	return result, nil
}

func (v *S3Vault) UpdateBody(ctx context.Context, path, body, ifMatch string) (domain.MutationResult, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	note, err := v.getObject(ctx, np.Storage)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if ifMatch != "" && note.ETag != ifMatch {
		return domain.MutationResult{}, domain.ErrConflict
	}

	note.FrontMatter.UpdatedAt = v.clock()
	note.Body = body
	etag := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(note.FrontMatter), body)

	data, err := noteutil.FormatNote(note.FrontMatter, body)
	if err != nil {
		return domain.MutationResult{}, err
	}

	// If-Match with the ETag the caller observed (or the one we just read).
	matchETag := ifMatch
	if matchETag == "" {
		matchETag = note.ETag
	}
	_, err = v.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:  aws.String(v.bucket),
		Key:     aws.String(v.objectKey(np.Storage)),
		Body:    bytes.NewReader(data),
		IfMatch: aws.String(matchETag),
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return domain.MutationResult{}, domain.ErrConflict
		}
		return domain.MutationResult{}, err
	}

	note.ETag = etag
	result := domain.MutationResult{Summary: note.Summary(), ETag: etag}
	ik := noteutil.IndexKey(np.Storage, false)
	links := noteutil.ParseLinks(body)
	v.cache.Set(ik, result.Summary)
	v.graph.Upsert(np.Storage, links)
	v.idx.Add(noteutil.SummaryToDoc(result.Summary, body))
	return result, nil
}

func (v *S3Vault) PatchFrontMatter(ctx context.Context, path string, fields map[string]any, ifMatch string) (domain.MutationResult, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	note, err := v.getObject(ctx, np.Storage)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if ifMatch != "" && note.ETag != ifMatch {
		return domain.MutationResult{}, domain.ErrConflict
	}

	domain.ApplyFrontMatterPatch(&note.FrontMatter, fields)
	note.FrontMatter.UpdatedAt = v.clock()
	etag := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(note.FrontMatter), note.Body)

	data, err := noteutil.FormatNote(note.FrontMatter, note.Body)
	if err != nil {
		return domain.MutationResult{}, err
	}

	matchETag := ifMatch
	if matchETag == "" {
		matchETag = note.ETag
	}
	_, err = v.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:  aws.String(v.bucket),
		Key:     aws.String(v.objectKey(np.Storage)),
		Body:    bytes.NewReader(data),
		IfMatch: aws.String(matchETag),
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return domain.MutationResult{}, domain.ErrConflict
		}
		return domain.MutationResult{}, err
	}

	note.ETag = etag
	result := domain.MutationResult{Summary: note.Summary(), ETag: etag}
	ik := noteutil.IndexKey(np.Storage, false)
	existingLinks := v.graph.Links(np.Storage)
	v.cache.Set(ik, result.Summary)
	v.graph.Upsert(np.Storage, existingLinks)
	return result, nil
}

func (v *S3Vault) Replace(ctx context.Context, path string, fields map[string]any, body, ifMatch string) (domain.MutationResult, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	note, err := v.getObject(ctx, np.Storage)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if ifMatch != "" && note.ETag != ifMatch {
		return domain.MutationResult{}, domain.ErrConflict
	}

	domain.ApplyFrontMatterPatch(&note.FrontMatter, fields)
	note.FrontMatter.UpdatedAt = v.clock()
	note.Body = body
	etag := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(note.FrontMatter), body)

	data, err := noteutil.FormatNote(note.FrontMatter, body)
	if err != nil {
		return domain.MutationResult{}, err
	}

	matchETag := ifMatch
	if matchETag == "" {
		matchETag = note.ETag
	}
	_, err = v.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:  aws.String(v.bucket),
		Key:     aws.String(v.objectKey(np.Storage)),
		Body:    bytes.NewReader(data),
		IfMatch: aws.String(matchETag),
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return domain.MutationResult{}, domain.ErrConflict
		}
		return domain.MutationResult{}, err
	}

	note.ETag = etag
	result := domain.MutationResult{Summary: note.Summary(), ETag: etag}
	ik := noteutil.IndexKey(np.Storage, false)
	links := noteutil.ParseLinks(body)
	v.cache.Set(ik, result.Summary)
	v.graph.Upsert(np.Storage, links)
	v.idx.Add(noteutil.SummaryToDoc(result.Summary, body))
	return result, nil
}

func (v *S3Vault) Move(ctx context.Context, path, newPath string, ifMatch string) (domain.MutationResult, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}
	nnp, err := domain.Normalize(newPath, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	note, err := v.getObject(ctx, np.Storage)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if ifMatch != "" && note.ETag != ifMatch {
		return domain.MutationResult{}, domain.ErrConflict
	}

	data, err := noteutil.FormatNote(note.FrontMatter, note.Body)
	if err != nil {
		return domain.MutationResult{}, err
	}

	// Write to new key (must not exist) then delete old key.
	_, err = v.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(v.bucket),
		Key:         aws.String(v.objectKey(nnp.Storage)),
		Body:        bytes.NewReader(data),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return domain.MutationResult{}, domain.ErrConflict
		}
		return domain.MutationResult{}, err
	}
	// Best-effort delete of the old key; failure is non-fatal (the note exists at new path).
	_, _ = v.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(v.bucket),
		Key:    aws.String(v.objectKey(np.Storage)),
	})

	note.Ref.Path = nnp.Storage
	result := domain.MutationResult{Summary: note.Summary(), ETag: note.ETag}
	links := noteutil.ParseLinks(note.Body)
	oldKey := noteutil.IndexKey(np.Storage, false)
	newKey := noteutil.IndexKey(nnp.Storage, false)
	v.cache.Move(oldKey, newKey, result.Summary)
	v.graph.Remove(np.Storage)
	v.graph.Upsert(nnp.Storage, links)
	v.idx.Remove(domain.NoteRef{Scope: v.scope, Path: np.Storage})
	v.idx.Add(noteutil.SummaryToDoc(result.Summary, note.Body))
	return result, nil
}

func (v *S3Vault) Delete(ctx context.Context, path string, soft bool, ifMatch string) error {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return err
	}

	if ifMatch != "" {
		note, err := v.getObject(ctx, np.Storage)
		if err != nil {
			return err
		}
		if note.ETag != ifMatch {
			return domain.ErrConflict
		}
	}

	if soft {
		// Soft-delete: move to .trash/ prefix.
		trashKey := v.objectKey(".trash/" + np.Storage)
		note, err := v.getObject(ctx, np.Storage)
		if err != nil {
			return err
		}
		data, err := noteutil.FormatNote(note.FrontMatter, note.Body)
		if err != nil {
			return err
		}
		if _, err := v.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(v.bucket),
			Key:    aws.String(trashKey),
			Body:   bytes.NewReader(data),
		}); err != nil {
			return err
		}
	}

	if _, err := v.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(v.bucket),
		Key:    aws.String(v.objectKey(np.Storage)),
	}); err != nil {
		return err
	}

	ik := noteutil.IndexKey(np.Storage, false)
	v.cache.Delete(ik)
	v.graph.Remove(np.Storage)
	v.idx.Remove(domain.NoteRef{Scope: v.scope, Path: np.Storage})
	return nil
}
