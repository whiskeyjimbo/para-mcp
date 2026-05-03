package s3vault

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/noteutil"
)

func (v *S3Vault) loadAll(ctx context.Context) error {
	prefix := v.scope + "/"
	paginator := s3.NewListObjectsV2Paginator(v.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(v.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if !strings.HasSuffix(key, ".md") {
				continue
			}
			// Derive vault-relative path from key.
			path := strings.TrimPrefix(key, prefix)
			note, err := v.getObject(ctx, path)
			if err != nil {
				v.log.Warn("s3vault: skip unreadable object", "key", key, "err", err)
				continue
			}
			s := note.Summary()
			ik := noteutil.IndexKey(path, false)
			links := noteutil.ParseLinks(note.Body)
			v.cache.Set(ik, s)
			v.graph.Upsert(path, links)
			v.idx.Add(noteutil.SummaryToDoc(s, note.Body))
		}
	}
	return nil
}
