// Package s3vault implements the Vault port backed by an S3-compatible object store.
// Multi-writer safety uses conditional PutObject (If-None-Match: * for creates,
// If-Match: <etag> for updates). Conditional-write support is probed at startup.
package s3vault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/index"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/noteutil"
)

// Option configures an S3Vault.
type Option func(*vaultConfig)

type vaultConfig struct {
	bucket   string
	region   string
	endpoint string // for S3-compatible stores (MinIO, etc.)
	log      *slog.Logger
	clock    noteutil.Clock
	ftsIndex ports.FTSIndex
}

// WithBucket sets the S3 bucket name.
func WithBucket(bucket string) Option {
	return func(c *vaultConfig) { c.bucket = bucket }
}

// WithRegion sets the AWS region.
func WithRegion(region string) Option {
	return func(c *vaultConfig) { c.region = region }
}

// WithEndpoint sets a custom endpoint URL for S3-compatible stores.
func WithEndpoint(endpoint string) Option {
	return func(c *vaultConfig) { c.endpoint = endpoint }
}

// WithLogger sets the logger (default: slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(c *vaultConfig) { c.log = l }
}

// WithClock overrides the time source (default: time.Now().UTC()).
func WithClock(fn noteutil.Clock) Option {
	return func(c *vaultConfig) { c.clock = fn }
}

// WithFTSIndex replaces the default BM25 index with a custom implementation.
func WithFTSIndex(i ports.FTSIndex) Option {
	return func(c *vaultConfig) { c.ftsIndex = i }
}

// S3Vault is an S3-backed implementation of ports.Vault.
type S3Vault struct {
	scope  string
	bucket string
	caps   domain.Capabilities
	clock  noteutil.Clock
	log    *slog.Logger

	s3    *s3.Client
	idx   ports.FTSIndex
	cache *noteutil.NoteCache
	graph *noteutil.BacklinkGraph
}

// New creates and initializes an S3Vault.
// It probes the bucket for conditional-write support and scans all existing
// notes into the in-memory indexes.
func New(ctx context.Context, scope string, opts ...Option) (*S3Vault, error) {
	cfg := &vaultConfig{
		log:   slog.Default(),
		clock: noteutil.DefaultClock,
	}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.bucket == "" {
		return nil, fmt.Errorf("s3vault: bucket name is required (use WithBucket)")
	}

	var awsCfgOpts []func(*awsconfig.LoadOptions) error
	if cfg.region != "" {
		awsCfgOpts = append(awsCfgOpts, awsconfig.WithRegion(cfg.region))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsCfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("s3vault: load AWS config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if cfg.endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.endpoint)
			o.UsePathStyle = true
		})
	}
	client := s3.NewFromConfig(awsCfg, s3Opts...)

	v := &S3Vault{
		scope:  scope,
		bucket: cfg.bucket,
		caps: domain.Capabilities{
			Writable:   true,
			SoftDelete: false, // S3 lacks cheap move; soft-delete moves objects
		},
		clock: cfg.clock,
		log:   cfg.log,
		s3:    client,
		cache: noteutil.NewNoteCache(),
		graph: noteutil.NewBacklinkGraph(),
	}

	if cfg.ftsIndex != nil {
		v.idx = cfg.ftsIndex
	} else {
		v.idx = index.New()
	}

	if err := v.probeConditionalWrites(ctx); err != nil {
		return nil, fmt.Errorf("s3vault: conditional-write probe: %w", err)
	}

	if err := v.loadAll(ctx); err != nil {
		return nil, fmt.Errorf("s3vault: initial scan: %w", err)
	}

	return v, nil
}

// probeConditionalWrites verifies the bucket supports If-None-Match on PutObject.
// It writes a probe object, then attempts a second write with If-None-Match: *,
// which must fail with PreconditionFailed. Any other outcome is a config error.
func (v *S3Vault) probeConditionalWrites(ctx context.Context) error {
	probeKey := v.objectKey(".paras_probe")
	probeBody := []byte("probe")

	// First write — must succeed unconditionally.
	_, err := v.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(v.bucket),
		Key:    aws.String(probeKey),
		Body:   bytes.NewReader(probeBody),
	})
	if err != nil {
		return fmt.Errorf("write probe object: %w", err)
	}
	// Clean up regardless of outcome below.
	defer v.s3.DeleteObject(ctx, &s3.DeleteObjectInput{ //nolint:errcheck
		Bucket: aws.String(v.bucket),
		Key:    aws.String(probeKey),
	})

	// Second write with If-None-Match: * must fail because the object exists.
	_, err = v.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(v.bucket),
		Key:         aws.String(probeKey),
		Body:        bytes.NewReader(probeBody),
		IfNoneMatch: aws.String("*"),
	})
	if err == nil {
		return fmt.Errorf("bucket does not enforce If-None-Match; conditional writes unsupported")
	}
	if !isPreconditionFailed(err) {
		return fmt.Errorf("unexpected error from conditional-write probe: %w", err)
	}
	return nil
}

// isPreconditionFailed reports whether err is a conditional-write rejection.
// S3 returns 409 ConditionalRequestConflict; some S3-compatible stores use
// 412 PreconditionFailed. Both signal that a conditional PUT was rejected.
func isPreconditionFailed(err error) bool {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "PreconditionFailed" || code == "ConditionalRequestConflict"
	}
	return false
}

// objectKey returns the S3 key for a vault-relative path.
func (v *S3Vault) objectKey(path string) string {
	if strings.HasPrefix(path, ".") {
		return v.scope + "/" + path
	}
	return v.scope + "/" + path
}

// Close releases the FTS index.
func (v *S3Vault) Close() error {
	v.idx.Close()
	return nil
}

// Scope returns the vault scope identifier.
func (v *S3Vault) Scope() domain.ScopeID { return v.scope }

// Capabilities returns the vault's capability flags.
func (v *S3Vault) Capabilities() domain.Capabilities { return v.caps }

// getObject fetches and parses a single S3 object into a domain.Note.
func (v *S3Vault) getObject(ctx context.Context, path string) (domain.Note, error) {
	out, err := v.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(v.bucket),
		Key:    aws.String(v.objectKey(path)),
	})
	if err != nil {
		if isNotFound(err) {
			return domain.Note{}, domain.ErrNotFound
		}
		return domain.Note{}, err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return domain.Note{}, err
	}
	fm, body, err := noteutil.ParseNote(data)
	if err != nil {
		return domain.Note{}, err
	}
	etag := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(fm), body)
	return domain.Note{
		Ref:         domain.NoteRef{Scope: v.scope, Path: path},
		FrontMatter: fm,
		Body:        body,
		ETag:        etag,
	}, nil
}

func isNotFound(err error) bool {
	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "NoSuchKey"
	}
	return false
}

// Verify S3Vault satisfies the Vault interface at compile time.
var _ interface {
	ports.Vault
	io.Closer
} = (*S3Vault)(nil)
