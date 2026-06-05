//go:build integration

package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/maomeng/aim/app/file-service/internal/config"
	"github.com/maomeng/aim/app/file-service/internal/logic"
	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/conf"
)

func newFileSvcCtx(t *testing.T) *svc.ServiceContext {
	t.Helper()
	var c config.Config
	conf.MustLoad("../etc/file.yaml", &c)
	c.Telemetry.Endpoint = ""

	if raw := etcdRawConfig("file.rpc"); raw != nil {
		configcenter.MergeRemote(&c, raw)
	}

	return svc.NewServiceContext(c)
}

func TestFileUploadGetInfo(t *testing.T) {
	svcCtx := newFileSvcCtx(t)
	ctx := context.Background()

	uploaderID := svcCtx.Snowflake.Generate()

	// Step 1: Get upload URL
	getUploadLogic := logic.NewGetUploadURLLogic(ctx, svcCtx)
	uploadResp, err := getUploadLogic.GetUploadURL(&filepb.GetUploadURLReq{
		Name:       "test.txt",
		MimeType:   "text/plain",
		Size:       12,
		UploaderId: uploaderID,
		Purpose:    filepb.FilePurpose_FILE_PURPOSE_MESSAGE,
		Access:     filepb.FileAccess_FILE_ACCESS_PRIVATE,
		ExpiresIn:  3600,
	})
	require.NoError(t, err)
	require.Greater(t, uploadResp.FileId, int64(0))
	require.NotEmpty(t, uploadResp.UploadUrl)
	fileID := uploadResp.FileId

	// Step 2: PUT to the upload URL
	content := "hello world"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadResp.UploadUrl, bytes.NewReader([]byte(content)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/plain")
	httpResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	httpResp.Body.Close()
	require.Equal(t, http.StatusOK, httpResp.StatusCode)

	// Step 3: Confirm upload
	confirmLogic := logic.NewConfirmUploadLogic(ctx, svcCtx)
	confirmResp, err := confirmLogic.ConfirmUpload(&filepb.ConfirmUploadReq{
		FileId:     fileID,
		UploaderId: uploaderID,
	})
	require.NoError(t, err)
	assert.Equal(t, fileID, confirmResp.File.FileId)

	// Step 4: Get file info
	getInfoLogic := logic.NewGetFileInfoLogic(ctx, svcCtx)
	infoResp, err := getInfoLogic.GetFileInfo(&filepb.GetFileInfoReq{
		FileId: fileID,
		UserId: uploaderID,
	})
	require.NoError(t, err)
	assert.Equal(t, fileID, infoResp.File.FileId)
	assert.Equal(t, "test.txt", infoResp.File.Name)
	assert.Equal(t, int64(12), infoResp.File.Size)

	// Step 5: Get download URL
	getDownloadLogic := logic.NewGetDownloadURLLogic(ctx, svcCtx)
	downloadResp, err := getDownloadLogic.GetDownloadURL(&filepb.GetDownloadURLReq{
		FileId:    fileID,
		UserId:    uploaderID,
		ExpiresIn: 3600,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, downloadResp.DownloadUrl)

	// Verify content via download URL
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadResp.DownloadUrl, nil)
	require.NoError(t, err)
	dlHttpResp, err := http.DefaultClient.Do(dlReq)
	require.NoError(t, err)
	defer dlHttpResp.Body.Close()
	dlBody, err := io.ReadAll(dlHttpResp.Body)
	require.NoError(t, err)
	assert.Equal(t, content, string(dlBody))
}

func TestFileBatchGetInfo(t *testing.T) {
	svcCtx := newFileSvcCtx(t)
	ctx := context.Background()

	uploaderID := svcCtx.Snowflake.Generate()
	var fileIDs []int64

	for i := 0; i < 2; i++ {
		getUploadLogic := logic.NewGetUploadURLLogic(ctx, svcCtx)
		uploadResp, err := getUploadLogic.GetUploadURL(&filepb.GetUploadURLReq{
			Name:       "batch.txt",
			MimeType:   "text/plain",
			Size:       4,
			UploaderId: uploaderID,
			Purpose:    filepb.FilePurpose_FILE_PURPOSE_MESSAGE,
			Access:     filepb.FileAccess_FILE_ACCESS_PRIVATE,
			ExpiresIn:  3600,
		})
		require.NoError(t, err)
		fileIDs = append(fileIDs, uploadResp.FileId)
	}

	batchLogic := logic.NewBatchGetFileInfoLogic(ctx, svcCtx)
	batchResp, err := batchLogic.BatchGetFileInfo(&filepb.BatchGetFileInfoReq{
		FileIds: fileIDs,
		UserId:  uploaderID,
	})
	require.NoError(t, err)
	assert.Equal(t, len(fileIDs), len(batchResp.Files))
}

func TestFileDelete(t *testing.T) {
	svcCtx := newFileSvcCtx(t)
	ctx := context.Background()

	uploaderID := svcCtx.Snowflake.Generate()

	// Create a file record
	getUploadLogic := logic.NewGetUploadURLLogic(ctx, svcCtx)
	uploadResp, err := getUploadLogic.GetUploadURL(&filepb.GetUploadURLReq{
		Name:       "delete.txt",
		MimeType:   "text/plain",
		Size:       4,
		UploaderId: uploaderID,
		Purpose:    filepb.FilePurpose_FILE_PURPOSE_MESSAGE,
		Access:     filepb.FileAccess_FILE_ACCESS_PRIVATE,
		ExpiresIn:  3600,
	})
	require.NoError(t, err)
	fileID := uploadResp.FileId

	// Delete
	deleteLogic := logic.NewDeleteFileLogic(ctx, svcCtx)
	_, err = deleteLogic.DeleteFile(&filepb.DeleteFileReq{
		FileId: fileID,
		UserId: uploaderID,
	})
	require.NoError(t, err)
}
