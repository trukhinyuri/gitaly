package repository

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v19/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/gitcmd"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v19/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v19/proto/go/gitalypb"
)

func (s *server) CreateRepository(ctx context.Context, req *gitalypb.CreateRepositoryRequest) (*gitalypb.CreateRepositoryResponse, error) {
	repository := req.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository, storage.WithSkipRepositoryExistenceCheck()); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	hash, err := git.ObjectHashByProto(req.GetObjectFormat())
	if err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	refBackend, err := git.ReferenceBackendByProto(req.GetReferenceBackend())
	if err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	if refBackend.Name == "" {
		// No opinion from the caller, so the node decides.
		ctx = withNewRepositoryReferenceBackend(ctx)
	} else {
		// The backend on the context selects both the Git binary and the ref-format.
		ctx = gitcmd.ContextWithReferenceBackend(ctx, refBackend)
	}

	if err := repoutil.Create(
		ctx,
		s.logger,
		s.locator,
		s.gitCmdFactory,
		s.catfileCache,
		s.txManager,
		s.repositoryCounter,
		repository,
		func(repo *gitalypb.Repository) error {
			// We do not want to seed the repository with any contents, so we just
			// return directly.
			return nil
		},
		repoutil.WithBranchName(string(req.GetDefaultBranch())),
		repoutil.WithObjectHash(hash),
	); err != nil {
		return nil, structerr.NewInternal("creating repository: %w", err)
	}

	if tx := storage.ExtractTransaction(ctx); tx != nil {
		if err := s.migrationStateManager.RecordKeyCreation(
			tx,
			tx.OriginalRepository(repository).GetRelativePath(),
		); err != nil {
			return nil, structerr.NewInternal("recording migration key: %w", err)
		}
	}

	return &gitalypb.CreateRepositoryResponse{}, nil
}

// withNewRepositoryReferenceBackend returns a context carrying the reference backend that new
// repositories should be created with, if any. When the MVCC backend feature flag is enabled, the
// MVCC reference backend is carried in the context. This selects the MVCC-enabled Git binary for
// every command run while creating the repository (git-init or git-clone as well as the subsequent
// for-each-ref, config and repack commands issued by repoutil.Create) and makes git-init and
// git-clone use the MVCC ref-format.
func withNewRepositoryReferenceBackend(ctx context.Context) context.Context {
	if featureflag.NewRepoMVCCBackend.IsEnabled(ctx) {
		ctx = gitcmd.ContextWithReferenceBackend(ctx, git.ReferenceBackendMVCC)
	}

	return ctx
}
