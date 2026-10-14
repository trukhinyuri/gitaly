package commit

import (
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/gitcmd"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v19/internal/log"
	"gitlab.com/gitlab-org/gitaly/v19/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedCommitServiceServer
	logger           log.Logger
	locator          storage.Locator
	gitCmdFactory    gitcmd.CommandFactory
	catfileCache     catfile.Cache
	cfg              config.Cfg
	localRepoFactory localrepo.Factory
	txManager        transaction.Manager
}

// NewServer creates a new instance of a grpc CommitServiceServer
func NewServer(deps *service.Dependencies) gitalypb.CommitServiceServer {
	return &server{
		logger:           deps.GetLogger(),
		locator:          deps.GetLocator(),
		gitCmdFactory:    deps.GetGitCmdFactory(),
		catfileCache:     deps.GetCatfileCache(),
		cfg:              deps.GetCfg(),
		localRepoFactory: deps.GetRepositoryFactory(),
		txManager:        deps.GetTxManager(),
	}
}
