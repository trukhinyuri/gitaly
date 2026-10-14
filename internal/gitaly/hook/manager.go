package hook

import (
	"context"
	"io"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v19/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v19/internal/git/gitcmd"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v19/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v19/internal/log"
	"gitlab.com/gitlab-org/gitaly/v19/proto/go/gitalypb"
)

// ReferenceTransactionState is the state of the Git reference transaction. It reflects the first
// parameter of the reference-transaction hook. See githooks(1) for more information.
type ReferenceTransactionState int

const (
	// ReferenceTransactionPrepared indicates all reference updates have been queued to the
	// transaction and were locked on disk.
	ReferenceTransactionPrepared = ReferenceTransactionState(iota)
	// ReferenceTransactionCommitted indicates the reference transaction was committed and all
	// references now have their respective new value.
	ReferenceTransactionCommitted
	// ReferenceTransactionAborted indicates the transaction was aborted, no changes were
	// performed and the reference locks have been released.
	ReferenceTransactionAborted
	// ReferenceTransactionPreparing indicates all reference updates are ready to be locked
	ReferenceTransactionPreparing
)

// Manager is an interface providing the ability to execute Git hooks.
type Manager interface {
	// PreReceiveHook executes the pre-receive Git hook and any installed custom hooks. stdin
	// must contain all references to be updated and match the format specified in githooks(5).
	PreReceiveHook(ctx context.Context, repo *gitalypb.Repository, pushOptions, env []string, stdin io.Reader, stdout, stderr io.Writer) error

	// PostReceiveHook executes the post-receive Git hook and any installed custom hooks. stdin
	// must contain all references to be updated and match the format specified in githooks(5).
	PostReceiveHook(ctx context.Context, repo *gitalypb.Repository, pushOptions, env []string, stdin io.Reader, stdout, stderr io.Writer) error

	// ReceiveReportHook runs after references have been updated but before the push status is
	// reported to the client. Returning an error aborts the push so that git reports the
	// references as failed. stdin must contain the successfully updated references and match
	// the format specified in githooks(5). It is not implemented yet and always succeeds.
	ReceiveReportHook(ctx context.Context, repo *gitalypb.Repository, pushOptions, env []string, stdin io.Reader, stdout, stderr io.Writer) error

	// UpdateHook executes the update Git hook and any installed custom hooks for the reference
	// `ref` getting updated from `oldValue` to `newValue`.
	UpdateHook(ctx context.Context, repo *gitalypb.Repository, ref, oldValue, newValue string, env []string, stdout, stderr io.Writer) error

	// ReferenceTransactionHook executes the reference-transaction Git hook. stdin must contain
	// all references to be updated and match the format specified in githooks(5).
	ReferenceTransactionHook(ctx context.Context, state ReferenceTransactionState, env []string, stdin io.Reader) error
}

// GitLabHookManager is a hook manager containing Git hook business logic. It
// uses the GitLab API to authenticate and track ongoing hook calls.
type GitLabHookManager struct {
	cfg                  config.Cfg
	locator              storage.Locator
	logger               log.Logger
	gitCmdFactory        gitcmd.CommandFactory
	cgroupsManager       cgroups.Manager
	txManager            transaction.Manager
	gitlabClient         gitlab.Client
	postReceiveRegistry  *PostReceiveRegistry
	refUpdatesCounter    *prometheus.CounterVec
	praefectSignalTicker func(time.Duration) helper.Ticker
	// postReceiveWaitTimeout is how long waitForPraefectSignal waits for the
	// Praefect signal before failing the push. It backs praefectSignalTicker and
	// is reported in the timeout error.
	postReceiveWaitTimeout time.Duration
}

// Describe is used to describe Prometheus metrics.
func (m *GitLabHookManager) Describe(descs chan<- *prometheus.Desc) {
	m.refUpdatesCounter.Describe(descs)
}

// Collect is used to collect Prometheus metrics.
func (m *GitLabHookManager) Collect(metrics chan<- prometheus.Metric) {
	m.refUpdatesCounter.Collect(metrics)
}

// Label values for the "update_type" label of the post-receive ref-updates
// counter. They are defined as constants so the pre-initialization here and the
// increment sites in postreceive.go can't drift or typo apart.
const (
	refUpdateCreated = "created"
	refUpdateUpdated = "updated"
	refUpdateDeleted = "deleted"
)

// PostReceiveRegistry provides the PostReceiveRegistry assigned to the Manager. The registry
// coordinates PostReceiveHook execution with Praefect signaling.
func (m *GitLabHookManager) PostReceiveRegistry() *PostReceiveRegistry {
	return m.postReceiveRegistry
}

// NewManager returns a new hook manager
func NewManager(
	cfg config.Cfg,
	locator storage.Locator,
	logger log.Logger,
	gitCmdFactory gitcmd.CommandFactory,
	cgroupsManager cgroups.Manager,
	txManager transaction.Manager,
	gitlabClient gitlab.Client,
	postReceiveRegistry *PostReceiveRegistry,
) *GitLabHookManager {
	refUpdatesCounter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gitaly",
			Subsystem: "hook",
			Name:      "post_receive_pack_ref_updates_total",
			Help:      "Number of reference updates processed by the post-receive hook, by update type",
		},
		[]string{"update_type"},
	)
	// Pre-initialize all known label combinations so the series are present in
	// /metrics from startup, enabling rate() and absent() alerting rules to work
	// correctly before the first push.
	for _, updateType := range []string{refUpdateCreated, refUpdateUpdated, refUpdateDeleted} {
		refUpdatesCounter.WithLabelValues(updateType)
	}

	// Fall back to the built-in default when the config default has not been
	// applied (e.g. a Cfg constructed directly in tests without Sanitize).
	waitTimeout := config.DefaultPostReceiveWaitTimeout
	if configured := cfg.Hooks.PostReceiveWaitTimeout.Duration(); configured > 0 {
		waitTimeout = configured
	}

	return &GitLabHookManager{
		cfg:                 cfg,
		locator:             locator,
		logger:              logger,
		gitCmdFactory:       gitCmdFactory,
		cgroupsManager:      cgroupsManager,
		txManager:           txManager,
		gitlabClient:        gitlabClient,
		postReceiveRegistry: postReceiveRegistry,
		refUpdatesCounter:   refUpdatesCounter,
		praefectSignalTicker: func(duration time.Duration) helper.Ticker {
			return helper.NewTimerTicker(duration)
		},
		postReceiveWaitTimeout: waitTimeout,
	}
}
