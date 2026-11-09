package logic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maomeng/aim/app/file-service/internal/metrics"
	"github.com/maomeng/aim/app/file-service/internal/model"
	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxBotOutputBytes = 65536
	botOutputGrantTTL = 90 * 24 * time.Hour
)

// RegisterBotOutputLogic writes a bot-owned Run result into the file domain's
// own contract. The trusted runtime supplies exact accepted Run identity and
// bytes; this layer independently rechecks current participation and execution
// lifecycle authority before creating a formal, independently authorized file.
type RegisterBotOutputLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRegisterBotOutputLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RegisterBotOutputLogic {
	return &RegisterBotOutputLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *RegisterBotOutputLogic) RegisterBotOutput(in *filepb.RegisterBotOutputReq) (*filepb.RegisterBotOutputResp, error) {
	access, bot := resourcegrant.Incoming(l.ctx)
	if !bot {
		return nil, status.Error(codes.Unauthenticated, "bot resource identity required")
	}
	if identity.Validate(in.GetRunId()) != nil || in.GetGeneration() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "accepted Run identity and generation required")
	}
	if in.GetName() != "result.csv" || !strings.HasPrefix(strings.ToLower(in.GetMimeType()), "text/csv") {
		return nil, status.Error(codes.InvalidArgument, "only the fixed result.csv artifact is supported")
	}
	data := in.GetContent()
	if len(data) == 0 || len(data) > maxBotOutputBytes || !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return nil, status.Error(codes.InvalidArgument, "result artifact must be bounded UTF-8 text")
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if in.GetSha256() != digest {
		return nil, status.Error(codes.InvalidArgument, "result artifact digest does not match its bytes")
	}
	// Authorize before writing any bytes. The transaction below repeats every
	// check under locks; this preflight prevents unauthorized object writes.
	if _, _, _, err := l.checkRun(l.svcCtx.DB.WithContext(l.ctx), in, access, false); err != nil {
		return nil, err
	}
	// Deterministic storage identity makes retries overwrite the same bytes and
	// never expose a host path or a temporary container location.
	key := "files/output/" + in.GetRunId() + "/" + strconv.FormatInt(in.GetGeneration(), 10) + "/" + digest + ".csv"
	upload, err := l.svcCtx.MinIO.Upload(l.ctx, key, bytes.NewReader(data), int64(len(data)), in.GetMimeType())
	if err != nil {
		l.Errorf("bot output upload failed: run_id=%s generation=%d", in.GetRunId(), in.GetGeneration())
		return nil, grpcError(ErrUploadFailed)
	}

	var file *model.File
	var grantID string
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "file-output:"+in.GetRunId()+":"+strconv.FormatInt(in.GetGeneration(), 10)).Error; err != nil {
			return err
		}
		run, subject, memberID, err := l.checkRun(tx, in, access, true)
		if err != nil {
			return err
		}
		var existing struct{ FileID string }
		err = tx.Table("file.output_registrations").Where("run_id = ? AND generation = ?", in.GetRunId(), in.GetGeneration()).Take(&existing).Error
		if err == nil {
			var prior model.File
			if err := tx.Where("id = ?", existing.FileID).Take(&prior).Error; err != nil {
				return err
			}
			var priorGrant struct{ ID string }
			if err := tx.Table(resourcegrant.Table("file")).Select("id").Where("resource_id = ? AND owner_id = ? AND subject_id = ? AND revoked_at IS NULL AND expires_at > ?", prior.ID, access.BotID, subject, time.Now()).Take(&priorGrant).Error; err != nil {
				return status.Error(codes.PermissionDenied, "registered output grant is no longer current")
			}
			file, grantID = &prior, priorGrant.ID
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		now := time.Now()
		expiresAt := now.Add(botOutputGrantTTL)
		record := &model.File{
			ID: id, Name: in.GetName(), Key: upload.Key, Size: int64(len(data)), MimeType: in.GetMimeType(),
			Ext: ".csv", Purpose: int32(filepb.FilePurpose_FILE_PURPOSE_DOCUMENT),
			Access: int32(filepb.FileAccess_FILE_ACCESS_PRIVATE), UploaderID: access.BotID,
			Bucket: l.svcCtx.MinIO.Bucket(), CreatedAt: now,
		}
		if access.Visibility != "personal" {
			record.Access = int32(filepb.FileAccess_FILE_ACCESS_CONVERSATION)
		}
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		newGrantID, err := identity.New()
		if err != nil {
			return err
		}
		grant := resourcegrant.Grant{
			ID: newGrantID, ResourceType: "file", ResourceID: record.ID,
			BotID: run.BotID, ConvID: run.ConvID, BindingID: run.BindingID,
			OwnerID: access.BotID, SubjectID: subject, SubjectMemberID: memberID,
			Scope: access.Visibility, CanRead: true, CanShare: access.Visibility != "personal",
			ExpiresAt: expiresAt, BotAuthorizationVersion: run.BotAuthorizationVersion,
			BindingAuthorizationVersion: run.BindingAuthorizationVersion, CreatedAt: now,
		}
		if err := tx.Table(resourcegrant.Table("file")).Create(&grant).Error; err != nil {
			return err
		}
		grantID = newGrantID
		if err := tx.Create(&resourcegrant.OutputRegistration{
			RunID: in.GetRunId(), Generation: in.GetGeneration(), BotID: run.BotID,
			ConvID: run.ConvID, BindingID: run.BindingID, FileID: record.ID,
			CreatedAt: now,
		}).Error; err != nil {
			return err
		}
		file = record
		return nil
	})
	if err != nil {
		return nil, err
	}
	metrics.FileUploadTotal.Inc("bot_output")
	metrics.FileStorageBytes.Add(float64(file.Size), "bot_output")
	l.Infof("bot output registered: run_id=%s generation=%d file_id=%s", in.GetRunId(), in.GetGeneration(), file.ID)
	return &filepb.RegisterBotOutputResp{File: toProtoFileInfo(file), GrantId: grantID}, nil
}

type outputRun struct {
	BotID, ConvID, BindingID                                  string
	RequesterID, ControllerID, Visibility, Status, StopStatus string
	ControllerMemberID                                        *string
	ExecutionGeneration                                       int64
	AccessRevoked                                             bool
	BotAuthorizationVersion, BindingAuthorizationVersion      int64
}

// checkRun revalidates accepted Run identity, current participation, execution
// lifecycle and the output principal. Source snapshots need no renewed grant.
func (l *RegisterBotOutputLogic) checkRun(tx *gorm.DB, in *filepb.RegisterBotOutputReq, access resourcegrant.Access, lock bool) (outputRun, string, string, error) {
	var run outputRun
	query := tx.Table("bot.runs").Select("bot_id, conv_id, binding_id, requester_id, controller_id, controller_member_id, visibility, status, stop_status, execution_generation, access_revoked, bot_authorization_version, binding_authorization_version").Where("id = ?", in.GetRunId())
	if lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if err := query.Take(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return outputRun{}, "", "", status.Error(codes.PermissionDenied, "Run output authorization unavailable")
		}
		return outputRun{}, "", "", err
	}
	if run.BotID != access.BotID || run.ConvID != access.ConvID || run.BindingID != access.BindingID || run.RequesterID != access.RequesterID || run.Visibility != access.Visibility {
		return outputRun{}, "", "", status.Error(codes.PermissionDenied, "Run output identity does not match this execution")
	}
	if run.AccessRevoked || run.Status == "stopping" || run.Status == "stopped" || run.Status == "succeeded" || run.Status == "failed" || run.StopStatus != "none" {
		return outputRun{}, "", "", status.Error(codes.PermissionDenied, "Run output authorization or lifecycle revoked")
	}
	if in.GetWorkerGeneration() != run.ExecutionGeneration || run.ExecutionGeneration < 1 {
		return outputRun{}, "", "", status.Error(codes.Aborted, "output registration worker generation superseded")
	}
	var lease struct{ ID string }
	query = tx.Table("bot.run_outbox").Select("id").Where("run_id = ? AND kind = 'execute' AND execution_generation = ? AND completed_at IS NULL AND lease_until > NOW()", in.GetRunId(), run.ExecutionGeneration)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if err := query.Take(&lease).Error; err != nil {
		return outputRun{}, "", "", status.Error(codes.Aborted, "output registration worker lease expired or lost")
	}
	// The execution identity survives worker lease generations. Only the recorded
	// successfully cleaned execution may register its artifact after recovery.
	var execution struct{ ID string }
	query = tx.Table("bot.run_executions").Select("id").Where("run_id = ? AND generation = ? AND phase = 'cleaned' AND outcome = 'succeeded'", in.GetRunId(), in.GetGeneration())
	if lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if err := query.Take(&execution).Error; err != nil {
		return outputRun{}, "", "", status.Error(codes.PermissionDenied, "verified execution output identity unavailable")
	}
	var binding struct{ ID string }
	query = tx.Table("messaging.conv_bots c").Select("c.id").
		Joins("JOIN bot.bots b ON b.id = c.bot_id").
		Where("c.id = ? AND c.conv_id = ? AND c.bot_id = ? AND c.revoked_at IS NULL AND b.status = 'active' AND c.authorization_version = ? AND b.authorization_version = ?", run.BindingID, run.ConvID, run.BotID, run.BindingAuthorizationVersion, run.BotAuthorizationVersion).
		Where("COALESCE(c.bot_settings::jsonb->'enabled', 'true'::jsonb) = 'true'::jsonb")
	if lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE", Table: clause.Table{Name: "c, b", Raw: true}})
	}
	if err := query.Take(&binding).Error; err != nil {
		return outputRun{}, "", "", status.Error(codes.PermissionDenied, "output participation authorization unavailable")
	}
	var controller struct{ ID string }
	if err := tx.Table("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", run.ConvID, run.ControllerID).Take(&controller).Error; err != nil || run.ControllerMemberID == nil || controller.ID != *run.ControllerMemberID {
		return outputRun{}, "", "", status.Error(codes.PermissionDenied, "output controller is not a current conversation member")
	}
	subject := run.RequesterID
	if identity.Validate(subject) != nil {
		subject = run.ControllerID
	}
	var member struct{ ID string }
	if err := tx.Table("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", run.ConvID, subject).Take(&member).Error; err != nil {
		return outputRun{}, "", "", status.Error(codes.PermissionDenied, "output principal is not a current conversation member")
	}
	return run, subject, member.ID, nil
}
