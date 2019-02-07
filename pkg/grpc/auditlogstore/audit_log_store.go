package auditlogstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/square/p2/pkg/audit"
	audit_log_protos "github.com/square/p2/pkg/grpc/auditlogstore/protos"
	"github.com/square/p2/pkg/logging"
	"github.com/square/p2/pkg/store/consul/transaction"
	"github.com/square/p2/pkg/util"

	grpccontext "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type AuditLogStore interface {
	List() (map[audit.ID]audit.AuditLog, error)
	Delete(
		ctx context.Context,
		id audit.ID,
	) error
}

type store struct {
	auditLogStore AuditLogStore
	logger        logging.Logger
	txner         transaction.Txner
}

func New(auditLogStore AuditLogStore, logger logging.Logger, txner transaction.Txner) audit_log_protos.P2AuditLogStoreServer {
	return store{
		auditLogStore: auditLogStore,
		logger:        logger,
		txner:         txner,
	}
}

// Note this function only returns at most 65 of the records. We may want to create a ListN function in the future.
func (s store) List(_ grpccontext.Context, _ *audit_log_protos.ListRequest) (*audit_log_protos.ListResponse, error) {
	records, err := s.auditLogStore.List()
	if err != nil {
		return nil, grpc.Errorf(codes.Unavailable, "error listing audit log records: %s", err)
	}

	ret := make(map[string]*audit_log_protos.AuditLog)
	// we want to limit the records we send over grpc to 65 because p2-audit-log only processes 64 records at a time. p2-audit-log also logs if the count is > 64 to warn if we are logging records faster than we can process them
	// Also if the size of the tree grows greater than 1024 * 1024 * 4 (https://github.com/grpc/grpc-go/blob/master/clientconn.go#L98) then we won't be able to send the records to p2-audit-log for processing and this tree will grow without bound at a fast rate.
	count := 0
	for id, al := range records {
		if count > 64 {
			break
		}
		protoRecord := rawAuditLogToProtoAuditLog(al)
		ret[id.String()] = &protoRecord
		count += 1
	}
	return &audit_log_protos.ListResponse{
		AuditLogs: ret,
	}, nil
}

func (s store) Delete(ctx grpccontext.Context, req *audit_log_protos.DeleteRequest) (*audit_log_protos.DeleteResponse, error) {
	if len(req.GetAuditLogIds()) == 0 {
		return nil, grpc.Errorf(codes.InvalidArgument, "no audit log IDs were specified for deletion")
	}

	// This is due to a constraint of consul transactions. They're limited to 64 operations per transaction.
	if len(req.GetAuditLogIds()) > 64 {
		return nil, grpc.Errorf(codes.InvalidArgument, "no more than 64 audit log records may be deleted at a time, but request was made for %d", len(req.GetAuditLogIds()))
	}

	ctx, cancelFunc := transaction.New(ctx)
	defer cancelFunc()
	var err error
	for _, id := range req.GetAuditLogIds() {
		err = s.auditLogStore.Delete(ctx, audit.ID(id))
		if err != nil {
			return nil, grpc.Errorf(codes.Unavailable, "error queueing up audit log deletions in a transaction: %s", err)
		}
	}

	err = transaction.MustCommit(ctx, s.txner)
	if err != nil {
		return nil, grpc.Errorf(codes.Unavailable, "error committing audit log deletion transaction: %s", err)
	}

	return new(audit_log_protos.DeleteResponse), nil
}

func rawAuditLogToProtoAuditLog(al audit.AuditLog) audit_log_protos.AuditLog {
	return audit_log_protos.AuditLog{
		EventType:     al.EventType.String(),
		EventDetails:  string(*al.EventDetails),
		Timestamp:     al.Timestamp.Format(time.RFC3339),
		SchemaVersion: int64(al.SchemaVersion.Int()),
	}
}

func ProtoAuditLogToRawAuditLog(protoAL audit_log_protos.AuditLog) (audit.AuditLog, error) {
	msg := json.RawMessage([]byte(protoAL.EventDetails))
	timestamp, err := time.Parse(time.RFC3339, protoAL.Timestamp)
	if err != nil {
		return audit.AuditLog{}, util.Errorf("could not parse timestamp %q as RFC3339: %s", protoAL.Timestamp, err)
	}

	return audit.AuditLog{
		EventType:     audit.EventType(protoAL.EventType),
		EventDetails:  &msg,
		Timestamp:     timestamp,
		SchemaVersion: audit.SchemaVersion(int(protoAL.SchemaVersion)),
	}, nil
}
