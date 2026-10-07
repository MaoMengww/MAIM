//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"testing"

	"github.com/maomeng/aim/app/file-service/internal/config"
	"github.com/maomeng/aim/app/file-service/internal/logic"
	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	pkgconfig "github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/identity"
	minioclient "github.com/maomeng/aim/pkg/minio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/conf"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newFileSvcCtx(t *testing.T) *svc.ServiceContext {
	t.Helper()
	var c config.Config
	pkgconfig.SetLocalDefaults()
	conf.MustLoad("../etc/file.yaml", &c, conf.UseEnv())
	c.Telemetry.Endpoint = ""

	svcCtx := svc.NewServiceContext(c)
	t.Cleanup(func() { require.NoError(t, svcCtx.DB.Close()) })
	return svcCtx
}

func newEntityID(t *testing.T) string {
	t.Helper()
	id, err := identity.New()
	require.NoError(t, err)
	return id
}

func putObject(t *testing.T, ctx context.Context, uploadURL, content string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewBufferString(content))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestFileUploadGetInfo(t *testing.T) {
	svcCtx := newFileSvcCtx(t)
	ctx := t.Context()
	uploaderID := newEntityID(t)
	otherUserID := newEntityID(t)
	content := "hello world"

	getUploadLogic := logic.NewGetUploadURLLogic(ctx, svcCtx)
	uploadResp, err := getUploadLogic.GetUploadURL(&filepb.GetUploadURLReq{
		Name: "test.txt", MimeType: "text/plain", Size: 12,
		UploaderId: uploaderID, Purpose: filepb.FilePurpose_FILE_PURPOSE_MESSAGE,
		Access: filepb.FileAccess_FILE_ACCESS_PRIVATE, ExpiresIn: 3600,
	})
	require.NoError(t, err)
	fileID := uploadResp.FileId
	require.NoError(t, identity.Validate(fileID))
	t.Cleanup(func() {
		_, err := logic.NewDeleteFileLogic(context.Background(), svcCtx).DeleteFile(&filepb.DeleteFileReq{FileId: fileID, UserId: uploaderID})
		require.NoError(t, err)
	})

	confirmLogic := logic.NewConfirmUploadLogic(ctx, svcCtx)
	_, err = confirmLogic.ConfirmUpload(&filepb.ConfirmUploadReq{FileId: fileID, UploaderId: uploaderID})
	assert.Equal(t, codes.NotFound, status.Code(err))
	getDownloadLogic := logic.NewGetDownloadURLLogic(ctx, svcCtx)
	_, err = getDownloadLogic.GetDownloadURL(&filepb.GetDownloadURLReq{FileId: fileID, UserId: uploaderID})
	assert.Equal(t, codes.NotFound, status.Code(err))

	putObject(t, ctx, uploadResp.UploadUrl, content)
	_, err = confirmLogic.ConfirmUpload(&filepb.ConfirmUploadReq{FileId: fileID, UploaderId: otherUserID})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	sum := md5.Sum([]byte(content))
	checksum := hex.EncodeToString(sum[:])
	confirmResp, err := confirmLogic.ConfirmUpload(&filepb.ConfirmUploadReq{FileId: fileID, UploaderId: uploaderID, Md5: &checksum})
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), confirmResp.File.Size)

	getInfoLogic := logic.NewGetFileInfoLogic(ctx, svcCtx)
	infoResp, err := getInfoLogic.GetFileInfo(&filepb.GetFileInfoReq{FileId: fileID, UserId: uploaderID})
	require.NoError(t, err)
	assert.Equal(t, checksum, infoResp.File.Md5)
	assert.Equal(t, int64(len(content)), infoResp.File.Size)
	_, err = getInfoLogic.GetFileInfo(&filepb.GetFileInfoReq{FileId: fileID, UserId: otherUserID})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = getDownloadLogic.GetDownloadURL(&filepb.GetDownloadURLReq{FileId: fileID, UserId: otherUserID})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	unsignedReq, err := http.NewRequestWithContext(ctx, http.MethodGet, svcCtx.MinIO.PublicURL(uploadResp.Key), nil)
	require.NoError(t, err)
	unsignedResp, err := http.DefaultClient.Do(unsignedReq)
	require.NoError(t, err)
	unsignedResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, unsignedResp.StatusCode)

	downloadResp, err := getDownloadLogic.GetDownloadURL(&filepb.GetDownloadURLReq{FileId: fileID, UserId: uploaderID, ExpiresIn: 3600})
	require.NoError(t, err)
	assert.Equal(t, fileID, downloadResp.File.FileId)
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadResp.DownloadUrl, nil)
	require.NoError(t, err)
	dlResp, err := http.DefaultClient.Do(dlReq)
	require.NoError(t, err)
	defer dlResp.Body.Close()
	require.Equal(t, http.StatusOK, dlResp.StatusCode)
	dlBody, err := io.ReadAll(dlResp.Body)
	require.NoError(t, err)
	assert.Equal(t, content, string(dlBody))
}

func TestFileBatchGetInfo(t *testing.T) {
	svcCtx := newFileSvcCtx(t)
	ctx := t.Context()
	uploaderID := newEntityID(t)
	otherUserID := newEntityID(t)
	var fileIDs []string
	t.Cleanup(func() {
		_, err := logic.NewBatchDeleteFilesLogic(context.Background(), svcCtx).BatchDeleteFiles(&filepb.BatchDeleteFilesReq{FileIds: fileIDs, UserId: uploaderID})
		require.NoError(t, err)
	})

	for i := range 2 {
		access := filepb.FileAccess_FILE_ACCESS_PRIVATE
		if i == 1 {
			access = filepb.FileAccess_FILE_ACCESS_PUBLIC
		}
		uploadResp, err := logic.NewGetUploadURLLogic(ctx, svcCtx).GetUploadURL(&filepb.GetUploadURLReq{
			Name: "batch.txt", MimeType: "text/plain", Size: 4,
			UploaderId: uploaderID, Purpose: filepb.FilePurpose_FILE_PURPOSE_MESSAGE,
			Access: access, ExpiresIn: 3600,
		})
		require.NoError(t, err)
		fileIDs = append(fileIDs, uploadResp.FileId)
		putObject(t, ctx, uploadResp.UploadUrl, "data")
		_, err = logic.NewConfirmUploadLogic(ctx, svcCtx).ConfirmUpload(&filepb.ConfirmUploadReq{FileId: uploadResp.FileId, UploaderId: uploaderID})
		require.NoError(t, err)
	}

	batchLogic := logic.NewBatchGetFileInfoLogic(ctx, svcCtx)
	batchResp, err := batchLogic.BatchGetFileInfo(&filepb.BatchGetFileInfoReq{FileIds: fileIDs, UserId: uploaderID})
	require.NoError(t, err)
	gotIDs := make([]string, 0, len(batchResp.Files))
	for _, f := range batchResp.Files {
		gotIDs = append(gotIDs, f.FileId)
	}
	assert.ElementsMatch(t, fileIDs, gotIDs)
	sharedResp, err := batchLogic.BatchGetFileInfo(&filepb.BatchGetFileInfoReq{FileIds: fileIDs, UserId: otherUserID})
	require.NoError(t, err)
	sharedIDs := make([]string, 0, len(sharedResp.Files))
	for _, f := range sharedResp.Files {
		sharedIDs = append(sharedIDs, f.FileId)
	}
	assert.Equal(t, []string{fileIDs[1]}, sharedIDs)
	publicDownload, err := logic.NewGetDownloadURLLogic(ctx, svcCtx).GetDownloadURL(&filepb.GetDownloadURLReq{FileId: fileIDs[1], UserId: otherUserID})
	require.NoError(t, err)
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, publicDownload.DownloadUrl, nil)
	require.NoError(t, err)
	dlResp, err := http.DefaultClient.Do(dlReq)
	require.NoError(t, err)
	defer dlResp.Body.Close()
	require.Equal(t, http.StatusOK, dlResp.StatusCode)
	dlBody, err := io.ReadAll(dlResp.Body)
	require.NoError(t, err)
	assert.Equal(t, "data", string(dlBody))
	_, err = logic.NewBatchDeleteFilesLogic(ctx, svcCtx).BatchDeleteFiles(&filepb.BatchDeleteFilesReq{FileIds: fileIDs, UserId: otherUserID})
	require.NoError(t, err)
	retained, err := batchLogic.BatchGetFileInfo(&filepb.BatchGetFileInfoReq{FileIds: fileIDs, UserId: uploaderID})
	require.NoError(t, err)
	gotIDs = gotIDs[:0]
	for _, f := range retained.Files {
		gotIDs = append(gotIDs, f.FileId)
	}
	assert.ElementsMatch(t, fileIDs, gotIDs)
}

func TestFileDelete(t *testing.T) {
	svcCtx := newFileSvcCtx(t)
	ctx := t.Context()
	uploaderID := newEntityID(t)
	otherUserID := newEntityID(t)
	uploadResp, err := logic.NewGetUploadURLLogic(ctx, svcCtx).GetUploadURL(&filepb.GetUploadURLReq{
		Name: "delete.txt", MimeType: "text/plain", Size: 4,
		UploaderId: uploaderID, Purpose: filepb.FilePurpose_FILE_PURPOSE_MESSAGE,
		Access: filepb.FileAccess_FILE_ACCESS_PRIVATE, ExpiresIn: 3600,
	})
	require.NoError(t, err)
	fileID := uploadResp.FileId
	t.Cleanup(func() {
		if _, err := svcCtx.FileRepo.GetByID(context.Background(), fileID); err == nil {
			_, err = logic.NewDeleteFileLogic(context.Background(), svcCtx).DeleteFile(&filepb.DeleteFileReq{FileId: fileID, UserId: uploaderID})
			require.NoError(t, err)
		}
	})
	putObject(t, ctx, uploadResp.UploadUrl, "data")
	_, err = logic.NewConfirmUploadLogic(ctx, svcCtx).ConfirmUpload(&filepb.ConfirmUploadReq{FileId: fileID, UploaderId: uploaderID})
	require.NoError(t, err)
	downloadResp, err := logic.NewGetDownloadURLLogic(ctx, svcCtx).GetDownloadURL(&filepb.GetDownloadURLReq{FileId: fileID, UserId: uploaderID})
	require.NoError(t, err)
	deleteLogic := logic.NewDeleteFileLogic(ctx, svcCtx)
	_, err = deleteLogic.DeleteFile(&filepb.DeleteFileReq{FileId: fileID, UserId: otherUserID})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = deleteLogic.DeleteFile(&filepb.DeleteFileReq{FileId: fileID, UserId: uploaderID})
	require.NoError(t, err)
	_, err = logic.NewGetFileInfoLogic(ctx, svcCtx).GetFileInfo(&filepb.GetFileInfoReq{FileId: fileID, UserId: uploaderID})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, _, err = svcCtx.MinIO.Stat(ctx, uploadResp.Key)
	assert.ErrorIs(t, err, minioclient.ErrObjectNotFound)
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadResp.DownloadUrl, nil)
	require.NoError(t, err)
	dlResp, err := http.DefaultClient.Do(dlReq)
	require.NoError(t, err)
	defer dlResp.Body.Close()
	assert.Equal(t, http.StatusNotFound, dlResp.StatusCode)
}
