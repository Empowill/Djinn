package backup

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"

	backupv1 "github.com/empowill/djinn/gen/go/backup/v1"
	"github.com/empowill/djinn/gen/go/backup/v1/backupv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Handler serves BackupService on the database db of the data folder home: the djinn that holds the database
// copies it.
func Handler(db *store.Store, home, version string) (string, http.Handler) {
	return backupv1connect.NewBackupServiceHandler(&service{db: db, home: home, version: version},
		connect.WithInterceptors(plan.Validate))
}

type service struct {
	backupv1connect.UnimplementedBackupServiceHandler
	db      *store.Store
	home    string
	version string
}

func (s *service) Create(
	ctx context.Context, req *connect.Request[backupv1.BackupServiceCreateRequest],
) (*connect.Response[backupv1.BackupServiceCreateResponse], error) {
	file := req.Msg.GetFile()
	if file == "" {
		var err error
		if file, err = DefaultFile(time.Now()); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
	}
	if _, err := zipFile(file); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	res, err := Create(ctx, s.home, file, s.version, s.db.Snapshot)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(res), nil
}
