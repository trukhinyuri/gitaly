package service

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v19/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v19/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v19/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v19/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/gitcmd"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v19/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/config"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/hook/updateref"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage/relational"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage/storagemgr/partition/migration"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v19/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v19/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v19/internal/grpc/middleware/limithandler"
	"gitlab.com/gitlab-org/gitaly/v19/internal/limiter"
	"gitlab.com/gitlab-org/gitaly/v19/internal/log"
	"gitlab.com/gitlab-org/gitaly/v19/internal/streamcache"
)

// Dependencies assembles set of components required by different kinds of services.
type Dependencies struct {
	Logger                 log.Logger
	Cfg                    config.Cfg
	GitalyHookManager      gitalyhook.Manager
	TransactionManager     transaction.Manager
	StorageLocator         storage.Locator
	ClientPool             *client.Pool
	GitCmdFactory          gitcmd.CommandFactory
	BackchannelRegistry    *backchannel.Registry
	GitlabClient           gitlab.Client
	CatfileCache           catfile.Cache
	DiskCache              cache.Cache
	PackObjectsCache       streamcache.Cache
	PackObjectsLimiter     limiter.Limiter
	LimitHandler           *limithandler.LimiterMiddleware
	RepositoryCounter      *counter.RepositoryCounter
	UpdaterWithHooks       *updateref.UpdaterWithHooks
	HousekeepingManager    housekeepingmgr.Manager
	Node                   storage.Node
	BackupSink             *backup.Sink
	BackupLocator          backup.Locator
	PostReceiveRegistry    *gitalyhook.PostReceiveRegistry
	BundleURIManager       *bundleuri.GenerationManager
	LocalRepositoryFactory localrepo.Factory
	MigrationStateManager  migration.StateManager
	ArchiveCache           streamcache.Cache
	PoolMetadataStore      relational.PoolStore
	ObjectPoolStateManager relational.ObjectPoolStateManager
	CgroupsManager         cgroups.Manager
	// ShutdownContext is cancelled when the server begins shutting down. Services can
	// use it to terminate work that intentionally ignores the per-request context, so
	// that such work does not outlive the graceful shutdown period.
	ShutdownContext context.Context
}

// GetShutdownContext returns a context that is cancelled once the graceful
// shutdown period has elapsed. It returns a non-cancellable context.Background()
// when no shutdown context was wired up, so callers can register cancellation
// via context.AfterFunc without a nil check (AfterFunc on a parent that can
// never be cancelled registers nothing).
func (dc *Dependencies) GetShutdownContext() context.Context {
	if dc.ShutdownContext == nil {
		return context.Background()
	}
	return dc.ShutdownContext
}

// GetLogger returns the logger.
func (dc *Dependencies) GetLogger() log.Logger {
	return dc.Logger
}

// GetCfg returns service configuration.
func (dc *Dependencies) GetCfg() config.Cfg {
	return dc.Cfg
}

// GetHookManager returns hook manager.
func (dc *Dependencies) GetHookManager() gitalyhook.Manager {
	return dc.GitalyHookManager
}

// GetTxManager returns transaction manager.
func (dc *Dependencies) GetTxManager() transaction.Manager {
	return dc.TransactionManager
}

// GetLocator returns storage locator.
func (dc *Dependencies) GetLocator() storage.Locator {
	return dc.StorageLocator
}

// GetConnsPool returns gRPC connection pool.
func (dc *Dependencies) GetConnsPool() *client.Pool {
	return dc.ClientPool
}

// GetGitCmdFactory returns git commands factory.
func (dc *Dependencies) GetGitCmdFactory() gitcmd.CommandFactory {
	return dc.GitCmdFactory
}

// GetCgroupsManager returns the cgroups manager.
func (dc *Dependencies) GetCgroupsManager() cgroups.Manager {
	return dc.CgroupsManager
}

// GetBackchannelRegistry returns a registry of the backchannels.
func (dc *Dependencies) GetBackchannelRegistry() *backchannel.Registry {
	return dc.BackchannelRegistry
}

// GetGitlabClient returns client to access GitLab API.
func (dc *Dependencies) GetGitlabClient() gitlab.Client {
	return dc.GitlabClient
}

// GetCatfileCache returns catfile cache.
func (dc *Dependencies) GetCatfileCache() catfile.Cache {
	return dc.CatfileCache
}

// GetDiskCache returns the disk cache.
func (dc *Dependencies) GetDiskCache() cache.Cache {
	return dc.DiskCache
}

// GetPackObjectsCache returns the pack-objects cache.
func (dc *Dependencies) GetPackObjectsCache() streamcache.Cache {
	return dc.PackObjectsCache
}

// GetLimitHandler returns the RPC limit handler.
func (dc *Dependencies) GetLimitHandler() *limithandler.LimiterMiddleware {
	return dc.LimitHandler
}

// GetRepositoryCounter returns the repository counter.
func (dc *Dependencies) GetRepositoryCounter() *counter.RepositoryCounter {
	return dc.RepositoryCounter
}

// GetUpdaterWithHooks returns the updater with hooks executor.
func (dc *Dependencies) GetUpdaterWithHooks() *updateref.UpdaterWithHooks {
	return dc.UpdaterWithHooks
}

// GetHousekeepingManager returns the housekeeping manager.
func (dc *Dependencies) GetHousekeepingManager() housekeepingmgr.Manager {
	return dc.HousekeepingManager
}

// GetPackObjectsLimiter returns the pack-objects limiter.
func (dc *Dependencies) GetPackObjectsLimiter() limiter.Limiter {
	return dc.PackObjectsLimiter
}

// GetNode returns the Node.
func (dc *Dependencies) GetNode() storage.Node {
	return dc.Node
}

// GetBackupSink returns the backup.Sink.
func (dc *Dependencies) GetBackupSink() *backup.Sink {
	return dc.BackupSink
}

// GetBackupLocator returns the backup.Locator.
func (dc *Dependencies) GetBackupLocator() backup.Locator {
	return dc.BackupLocator
}

// GetPostReceiveRegistry returns the PostReceiveRegistry.
func (dc *Dependencies) GetPostReceiveRegistry() *gitalyhook.PostReceiveRegistry {
	return dc.PostReceiveRegistry
}

// GetRepositoryFactory returns the RepositoryFactory
func (dc *Dependencies) GetRepositoryFactory() localrepo.Factory {
	return dc.LocalRepositoryFactory
}

// GetBundleURIManager returns the bundleuri.GenerationManager
func (dc *Dependencies) GetBundleURIManager() *bundleuri.GenerationManager {
	return dc.BundleURIManager
}

// GetMigrationStateManager returns the migration.StateManager
func (dc *Dependencies) GetMigrationStateManager() migration.StateManager {
	return dc.MigrationStateManager
}

// GetArchiveCache returns the archive cache
func (dc *Dependencies) GetArchiveCache() streamcache.Cache {
	return dc.ArchiveCache
}

// GetPoolMetadataStore returns the pool metadata store
func (dc *Dependencies) GetPoolMetadataStore() relational.PoolStore {
	return dc.PoolMetadataStore
}

// GetObjectPoolStateManager returns the object pool state manager
func (dc *Dependencies) GetObjectPoolStateManager() relational.ObjectPoolStateManager {
	return dc.ObjectPoolStateManager
}
