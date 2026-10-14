package repository

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v19/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v19/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v19/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/gitcmd"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v19/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage/storagemgr/partition/migration"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v19/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v19/internal/log"
	"gitlab.com/gitlab-org/gitaly/v19/internal/streamcache"
	"gitlab.com/gitlab-org/gitaly/v19/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v19/internal/unarycache"
	"gitlab.com/gitlab-org/gitaly/v19/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedRepositoryServiceServer
	logger                log.Logger
	conns                 *client.Pool
	locator               storage.Locator
	txManager             transaction.Manager
	node                  storage.Node
	gitCmdFactory         gitcmd.CommandFactory
	cfg                   config.Cfg
	loggingCfg            config.Logging
	catfileCache          catfile.Cache
	housekeepingManager   housekeepingmgr.Manager
	backupSink            *backup.Sink
	backupLocator         backup.Locator
	repositoryCounter     *counter.RepositoryCounter
	localRepoFactory      localrepo.Factory
	licenseCache          *unarycache.Cache[git.ObjectID, *gitalypb.FindLicenseResponse]
	bundleURIManager      *bundleuri.GenerationManager
	migrationStateManager migration.StateManager
	archiveCache          streamcache.Cache
	cgroupsManager        cgroups.Manager
}

// NewServer creates a new instance of a gRPC repo server
func NewServer(deps *service.Dependencies) gitalypb.RepositoryServiceServer {
	return &server{
		logger:                deps.GetLogger(),
		locator:               deps.GetLocator(),
		txManager:             deps.GetTxManager(),
		node:                  deps.GetNode(),
		gitCmdFactory:         deps.GetGitCmdFactory(),
		conns:                 deps.GetConnsPool(),
		cfg:                   deps.GetCfg(),
		loggingCfg:            deps.GetCfg().Logging,
		catfileCache:          deps.GetCatfileCache(),
		housekeepingManager:   deps.GetHousekeepingManager(),
		backupSink:            deps.GetBackupSink(),
		backupLocator:         deps.GetBackupLocator(),
		repositoryCounter:     deps.GetRepositoryCounter(),
		localRepoFactory:      deps.GetRepositoryFactory(),
		licenseCache:          newLicenseCache(),
		bundleURIManager:      deps.GetBundleURIManager(),
		migrationStateManager: deps.GetMigrationStateManager(),
		archiveCache:          deps.GetArchiveCache(),
		cgroupsManager:        deps.GetCgroupsManager(),
	}
}

func (s *server) quarantinedRepo(ctx context.Context, repo *gitalypb.Repository) (*quarantine.Dir, *localrepo.Repo, func(), error) {
	quarantineDir, cleanup, err := quarantine.New(ctx, repo, s.logger, s.locator)
	if err != nil {
		return nil, nil, nil, structerr.NewInternal("creating object quarantine: %w", err)
	}

	quarantineRepo := s.localRepoFactory.Build(quarantineDir.QuarantinedRepo())
	return quarantineDir, quarantineRepo, cleanup, nil
}
