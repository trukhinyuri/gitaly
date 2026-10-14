package ssh

import (
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v19/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v19/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/hook/updateref"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v19/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v19/internal/log"
	"gitlab.com/gitlab-org/gitaly/v19/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedSSHServiceServer
	logger                                   log.Logger
	cfg                                      config.Cfg
	locator                                  storage.Locator
	txManager                                transaction.Manager
	hookManager                              hook.Manager
	updater                                  *updateref.UpdaterWithHooks
	uploadPackRequestTimeoutTickerFactory    func() helper.Ticker
	uploadArchiveRequestTimeoutTickerFactory func() helper.Ticker
	packfileNegotiationMetrics               *prometheus.CounterVec
	packfileNegotiationDeepenMetrics         prometheus.Histogram
	uploadPackServedBytesMetrics             *prometheus.CounterVec
	receivePackObjectsMetrics                prometheus.Histogram
	backupLocator                            backup.Locator
	backupSink                               *backup.Sink
	localRepoFactory                         localrepo.Factory
	bundleURIManager                         *bundleuri.GenerationManager
}

// NewServer creates a new instance of a grpc SSHServer
func NewServer(deps *service.Dependencies, serverOpts ...ServerOpt) gitalypb.SSHServiceServer {
	s := &server{
		logger:      deps.GetLogger(),
		cfg:         deps.GetCfg(),
		locator:     deps.GetLocator(),
		txManager:   deps.GetTxManager(),
		hookManager: deps.GetHookManager(),
		updater:     deps.GetUpdaterWithHooks(),
		uploadPackRequestTimeoutTickerFactory: func() helper.Ticker {
			return helper.NewTimerTicker(deps.Cfg.Timeout.UploadPackNegotiation.Duration())
		},
		uploadArchiveRequestTimeoutTickerFactory: func() helper.Ticker {
			return helper.NewTimerTicker(deps.Cfg.Timeout.UploadArchiveNegotiation.Duration())
		},
		packfileNegotiationMetrics: prometheus.NewCounterVec(
			prometheus.CounterOpts{},
			[]string{"git_negotiation_feature"},
		),
		packfileNegotiationDeepenMetrics: prometheus.NewHistogram(prometheus.HistogramOpts{}),
		uploadPackServedBytesMetrics: prometheus.NewCounterVec(
			prometheus.CounterOpts{},
			[]string{"clone_type"},
		),
		receivePackObjectsMetrics: prometheus.NewHistogram(prometheus.HistogramOpts{}),
		backupLocator:             deps.GetBackupLocator(),
		backupSink:                deps.GetBackupSink(),
		localRepoFactory:          deps.GetRepositoryFactory(),
		bundleURIManager:          deps.GetBundleURIManager(),
	}

	for _, serverOpt := range serverOpts {
		serverOpt(s)
	}

	return s
}

// ServerOpt is a self referential option for server
type ServerOpt func(s *server)

// WithUploadPackRequestTimeoutTickerFactory sets the upload pack request timeout ticker factory.
func WithUploadPackRequestTimeoutTickerFactory(factory func() helper.Ticker) ServerOpt {
	return func(s *server) {
		s.uploadPackRequestTimeoutTickerFactory = factory
	}
}

// WithArchiveRequestTimeoutTickerFactory sets the upload pack request timeout ticker factory.
func WithArchiveRequestTimeoutTickerFactory(factory func() helper.Ticker) ServerOpt {
	return func(s *server) {
		s.uploadArchiveRequestTimeoutTickerFactory = factory
	}
}

//nolint:revive // This is unintentionally missing documentation.
func WithPackfileNegotiationMetrics(c *prometheus.CounterVec) ServerOpt {
	return func(s *server) {
		s.packfileNegotiationMetrics = c
	}
}

// WithPackfileNegotiationDeepenMetrics overrides the default histogram metric.
func WithPackfileNegotiationDeepenMetrics(h prometheus.Histogram) ServerOpt {
	return func(s *server) {
		s.packfileNegotiationDeepenMetrics = h
	}
}

// WithUploadPackServedBytesMetrics overrides the default upload-pack served
// bytes counter.
func WithUploadPackServedBytesMetrics(c *prometheus.CounterVec) ServerOpt {
	return func(s *server) {
		s.uploadPackServedBytesMetrics = c
	}
}

// WithReceivePackObjectsMetrics overrides the default receive-pack objects
// histogram.
func WithReceivePackObjectsMetrics(h prometheus.Histogram) ServerOpt {
	return func(s *server) {
		s.receivePackObjectsMetrics = h
	}
}
