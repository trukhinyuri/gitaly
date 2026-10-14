package hook

import (
	"testing"

	"github.com/stretchr/testify/require"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v19/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v19/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v19/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v19/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v19/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func TestSignalPostReceiveReady(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	postReceiveRegistry := gitalyhook.NewPostReceiveRegistry()

	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		deps.PostReceiveRegistry = postReceiveRegistry
		gitalypb.RegisterHookServiceServer(srv, NewServer(deps))
	}, testserver.WithDisablePraefect())

	client, conn := newHooksClient(t, cfg.SocketPath)
	t.Cleanup(func() { conn.Close() })

	storageName := cfg.Storages[0].Name

	// Note: These tests will fail with PermissionDenied because they're not called via
	// Praefect's backchannel. This is the expected security behavior - the RPC should
	// only be callable from Praefect. Integration tests cover the full backchannel flow.

	t.Run("rejects call without backchannel", func(t *testing.T) {
		_, err := client.SignalPostReceiveReady(ctx, &gitalypb.SignalPostReceiveReadyRequest{
			StorageName:   storageName,
			TransactionId: 12345,
		})
		testhelper.RequireGrpcCode(t, err, codes.PermissionDenied)
		require.Contains(t, err.Error(), "can only be called via Praefect backchannel")
	})

	// Note: SignalPostReceiveReady checks for a Praefect backchannel before it
	// validates the request, so without a backchannel every call is rejected with
	// PermissionDenied regardless of its arguments. The transaction_id == 0 guard
	// is only reachable via a real backchannel and is covered by integration tests.

	t.Run("backchannel check precedes request validation", func(t *testing.T) {
		// A zero transaction ID would be rejected by the arg validation, but the
		// backchannel guard fires first, so we still get PermissionDenied here.
		_, err := client.SignalPostReceiveReady(ctx, &gitalypb.SignalPostReceiveReadyRequest{
			StorageName:   storageName,
			TransactionId: 0,
		})
		testhelper.RequireGrpcCode(t, err, codes.PermissionDenied)
	})
}
