package objectstorage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/mvcc"
	"gitlab.com/gitlab-org/gitaly/v19/internal/testhelper"
	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"
)

const (
	testRepo = "@hashed/ab/cd/repo.git"
	// testRepoPrefix is testRepo with Gitaly's leading "@" marker stripped, i.e.
	// the bucket key prefix HashedPathPrefix produces.
	testRepoPrefix = "hashed/ab/cd/repo.git"
)

// genPointer is a base with a known generation, as ReadManifestPointer returns.
func genPointer(hash string, gen uint64) mvcc.ManifestPointer {
	return mvcc.ManifestPointer{Hash: hash, Generation: gen, HasGeneration: true}
}

// hashPointer is a base with only a hash (generation unknown), e.g. a read pin,
// which drives Commit's head-resolving fallback path.
func hashPointer(hash string) mvcc.ManifestPointer {
	return mvcc.ManifestPointer{Hash: hash}
}

func newMemStorage(t *testing.T) *Storage {
	t.Helper()
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	return NewStorage(bucket, HashedPathPrefix)
}

func put(t *testing.T, s *Storage, key, content string) {
	t.Helper()
	require.NoError(t, s.PutArtifact(testhelper.Context(t), testRepo, key, io.NopCloser(strings.NewReader(content))))
}

func readAll(t *testing.T, rc io.ReadCloser, err error) string {
	t.Helper()
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(data)
}

// readArtifact reads an artifact through the consume-callback interface and
// returns its contents (or the ReadArtifact error, e.g. fs.ErrNotExist).
func readArtifact(t *testing.T, s *Storage, key string) (string, error) {
	t.Helper()
	var got string
	err := s.ReadArtifact(testhelper.Context(t), testRepo, key, func(_ context.Context, r io.ReadCloser) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		got = string(data)
		return nil
	})
	return got, err
}

func TestStorage_ArtifactRoundTrip(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	put(t, s, "pack/abc.pack", "pack-bytes")
	got, err := readArtifact(t, s, "pack/abc.pack")
	require.NoError(t, err)
	require.Equal(t, "pack-bytes", got)

	// A manifest is just an artifact under the manifests/ prefix, so ReadManifest
	// resolves the same key PutArtifact wrote.
	put(t, s, "manifests/Z1", "manifest-body")
	rc, err := s.ReadManifest(ctx, testRepo, "Z1")
	require.Equal(t, "manifest-body", readAll(t, rc, err))
}

func TestStorage_ReadMissing(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	_, err := s.ReadManifestPointer(ctx, testRepo)
	require.ErrorIs(t, err, fs.ErrNotExist)

	_, err = readArtifact(t, s, "pack/missing.pack")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestStorage_CommitGenerations(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	// Genesis commit establishes the pointer at generation 0.
	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1"))
	pointer, err := s.ReadManifestPointer(ctx, testRepo)
	require.NoError(t, err)
	require.Equal(t, genPointer("Z1", 0), pointer)

	// Advancing from the resolved base succeeds and writes a new generation slot.
	require.NoError(t, s.Commit(ctx, testRepo, pointer, "Z2"))
	pointer, err = s.ReadManifestPointer(ctx, testRepo)
	require.NoError(t, err)
	require.Equal(t, genPointer("Z2", 1), pointer)

	// A commit from a stale base (generation 0 again) is rejected and leaves the
	// pointer untouched.
	require.ErrorIs(t, s.Commit(ctx, testRepo, genPointer("Z1", 0), "Z3"), mvcc.ErrManifestPointerConflict)
	pointer, err = s.ReadManifestPointer(ctx, testRepo)
	require.NoError(t, err)
	require.Equal(t, "Z2", pointer.Hash)
}

func TestStorage_CommitConcurrentSameBaseConflicts(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1"))
	base, err := s.ReadManifestPointer(ctx, testRepo)
	require.NoError(t, err)

	// Two writers from the same base target the same next generation slot. The
	// first create-if-absent wins; the second must see a conflict.
	require.NoError(t, s.Commit(ctx, testRepo, base, "Z2a"))
	require.ErrorIs(t, s.Commit(ctx, testRepo, base, "Z2b"), mvcc.ErrManifestPointerConflict)

	pointer, err := s.ReadManifestPointer(ctx, testRepo)
	require.NoError(t, err)
	require.Equal(t, "Z2a", pointer.Hash)
}

func TestStorage_EmptyBaseAfterGenesisConflicts(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	// Once a genesis commit exists, a later commit that still assumes an empty
	// base is stale and must conflict.
	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1"))
	require.ErrorIs(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1-other"), mvcc.ErrManifestPointerConflict)
}

func TestStorage_GenerationKeyEncoding(t *testing.T) {
	ctx := testhelper.Context(t)
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	s := NewStorage(bucket, HashedPathPrefix)

	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1")) // genesis -> generation 0
	base, err := s.ReadManifestPointer(ctx, testRepo)
	require.NoError(t, err)
	require.NoError(t, s.Commit(ctx, testRepo, base, "Z2")) // generation 1

	// The generation number is stored inverted (MaxUint64-gen) as 16 hex digits,
	// so generation 0 is the max key and the newer generation sorts first. Each
	// generation object is followed by its "<generation>.<hash>" sibling, which
	// sorts right after it and before the next generation. Keys are laid out under
	// the @-stripped repository prefix.
	require.Equal(t, []string{
		testRepoPrefix + "/generations/fffffffffffffffe",    // generation 1
		testRepoPrefix + "/generations/fffffffffffffffe.Z2", // generation 1 hash sibling
		testRepoPrefix + "/generations/ffffffffffffffff",    // generation 0 (genesis)
		testRepoPrefix + "/generations/ffffffffffffffff.Z1", // generation 0 hash sibling
	}, listKeys(t, ctx, bucket, testRepoPrefix+"/generations/"))
}

func TestStorage_StripsHashedPathMarker(t *testing.T) {
	ctx := testhelper.Context(t)
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	s := NewStorage(bucket, HashedPathPrefix)

	put(t, s, "pack/abc.pack", "pack-bytes")
	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1"))

	// Every key this package writes drops Gitaly's leading "@" hashed-path marker,
	// so nothing in the bucket starts with "@".
	for _, key := range listKeys(t, ctx, bucket, "") {
		require.False(t, strings.HasPrefix(key, "@"), "key %q must not keep the @ marker", key)
		require.True(t, strings.HasPrefix(key, testRepoPrefix+"/"), "unexpected key %q", key)
	}
}

func TestStorage_UsesInjectedPrefixResolver(t *testing.T) {
	ctx := testhelper.Context(t)
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })

	// The resolver decouples the bucket layout from the repository ID: objects
	// land under the opaque prefix it returns, not under the hashed path. This is
	// the seam an object-pool mapping will replace.
	s := NewStorage(bucket, func(string) (string, error) { return "opaque/token", nil })

	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1"))

	keys := listKeys(t, ctx, bucket, "")
	require.NotEmpty(t, keys)
	for _, key := range keys {
		require.True(t, strings.HasPrefix(key, "opaque/token/"), "unexpected key %q", key)
	}
}

func TestStorage_PutArtifactIsIdempotent(t *testing.T) {
	s := newMemStorage(t)

	// Artifacts are content-addressable, so re-putting the same key is a no-op
	// that must succeed rather than conflict. The first write wins and its bytes
	// remain: a create-if-absent write leaves the existing object untouched.
	put(t, s, "pack/abc.pack", "first")
	put(t, s, "pack/abc.pack", "second")

	got, err := readArtifact(t, s, "pack/abc.pack")
	require.NoError(t, err)
	require.Equal(t, "first", got)
}

func TestStorage_CommitWritesHashSiblingKey(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1"))

	// The sibling key carries the manifest hash in its name so head can resolve it
	// from a listing without reading the generation body.
	require.Contains(t,
		listKeys(t, ctx, s.bucket, generationListPrefix(testRepoPrefix)),
		generationHashKey(testRepoPrefix, 0, "Z1"))

	pointer, err := s.ReadManifestPointer(ctx, testRepo)
	require.NoError(t, err)
	require.Equal(t, genPointer("Z1", 0), pointer)
}

func TestStorage_HeadFallsBackToGenerationBody(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1"))

	// A reader may observe the generation object before its hash sibling is
	// written. Deleting the sibling forces head onto the body-read fallback, which
	// must still resolve the same generation and hash.
	require.NoError(t, s.bucket.Delete(ctx, generationHashKey(testRepoPrefix, 0, "Z1")))

	pointer, err := s.ReadManifestPointer(ctx, testRepo)
	require.NoError(t, err)
	require.Equal(t, genPointer("Z1", 0), pointer)
}

func listKeys(t *testing.T, ctx context.Context, bucket *blob.Bucket, prefix string) []string {
	t.Helper()
	iter := bucket.List(&blob.ListOptions{Prefix: prefix})
	var keys []string
	for {
		obj, err := iter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		keys = append(keys, obj.Key)
	}
	return keys
}

func TestStorage_CommitRequiresBaseGeneration(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1"))

	// A non-empty base without a generation is a contract violation: the cache
	// always resolves the generation via ReadManifestPointer. It is neither a
	// genesis nor a CAS conflict, so it returns a plain error.
	err := s.Commit(ctx, testRepo, hashPointer("Z1"), "Z2")
	require.Error(t, err)
	require.NotErrorIs(t, err, mvcc.ErrManifestPointerConflict)
}

func TestStorage_RejectsUnsafeKeys(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	// A ".." segment or a leading "/" must be rejected before path.Join can clean
	// it and let a key escape the repository's prefix in the shared bucket.
	for _, key := range []string{"../other/pack", "pack/../../escape", "/abs/pack", ""} {
		_, readErr := readArtifact(t, s, key)
		require.Error(t, readErr, "ReadArtifact must reject %q", key)

		putErr := s.PutArtifact(ctx, testRepo, key, io.NopCloser(strings.NewReader("x")))
		require.Error(t, putErr, "PutArtifact must reject %q", key)

		_, manErr := s.ReadManifest(ctx, testRepo, key)
		require.Error(t, manErr, "ReadManifest must reject %q", key)

		// newHash is embedded into the generation sibling key, so Commit must
		// reject it too rather than rely on callers validating upstream.
		commitErr := s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, key)
		require.Error(t, commitErr, "Commit must reject newHash %q", key)
	}
}

func TestStorage_RejectsEmptyRepositoryID(t *testing.T) {
	ctx := testhelper.Context(t)
	s := newMemStorage(t)

	// "@" strips to an empty prefix, which would collapse keys to the bucket root.
	for _, repo := range []string{"", "@"} {
		_, err := s.ReadManifestPointer(ctx, repo)
		require.Error(t, err, "empty repository id %q must be rejected", repo)
		require.Error(t, s.Commit(ctx, repo, mvcc.ManifestPointer{}, "Z1"))
	}
}

func TestStorage_TearDownKeepsBucketOpen(t *testing.T) {
	ctx := testhelper.Context(t)

	// The caller owns the bucket, so TearDown must not close it: the bucket stays
	// usable afterwards.
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	s := NewStorage(bucket, HashedPathPrefix)

	require.NoError(t, s.TearDown(ctx))
	require.NoError(t, s.Commit(ctx, testRepo, mvcc.ManifestPointer{}, "Z1"), "bucket must stay open after TearDown")
}
