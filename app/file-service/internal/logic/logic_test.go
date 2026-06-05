package logic

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"io"
	"testing"
	"time"

	"github.com/maomeng/aim/app/file-service/internal/model"
	"github.com/maomeng/aim/app/file-service/internal/repo"
	"github.com/maomeng/aim/app/file-service/internal/svc"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/pkg/snowflake"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSvcCtx() *svc.ServiceContext {
	sn, _ := snowflake.NewNode(1)
	return &svc.ServiceContext{
		Snowflake: sn,
		MinIO:     &mockMinIO{},
		FileRepo:  &mockFileRepo{},
	}
}

// ========== GetUploadURL Tests ==========

func TestGetUploadURL_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewGetUploadURLLogic(context.Background(), svcCtx)

	resp, err := logic.GetUploadURL(&filepb.GetUploadURLReq{
		Name:      "test.jpg",
		MimeType:  "image/jpeg",
		Size:      1024,
		Purpose:   filepb.FilePurpose_FILE_PURPOSE_MESSAGE,
		Access:    filepb.FileAccess_FILE_ACCESS_CONVERSATION,
		ExpiresIn: 3600,
	})
	require.NoError(t, err)
	assert.Greater(t, resp.FileId, int64(0))
	assert.NotEmpty(t, resp.UploadUrl)
	assert.NotEmpty(t, resp.Key)
	assert.Greater(t, resp.ExpiresAt, int64(0))
}

func TestGetUploadURL_MissingName(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewGetUploadURLLogic(context.Background(), svcCtx)

	_, err := logic.GetUploadURL(&filepb.GetUploadURLReq{Size: 1024})
	assert.Error(t, err)
}

// ========== ConfirmUpload Tests ==========

func TestConfirmUpload_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewConfirmUploadLogic(context.Background(), svcCtx)

	resp, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{FileId: 1, UploaderId: 1001})
	require.NoError(t, err)
	assert.NotNil(t, resp.File)
	assert.Equal(t, int64(1), resp.File.FileId)
}

func TestConfirmUpload_WithMD5(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FileRepo = &mockFileRepo{trackUpdate: true}
	logic := NewConfirmUploadLogic(context.Background(), svcCtx)

	md5val := "d41d8cd98f00b204e9800998ecf8427e"
	resp, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{
		FileId:     1,
		UploaderId: 1001,
		Md5:        &md5val,
	})
	require.NoError(t, err)
	assert.Equal(t, md5val, resp.File.Md5)
}

func TestConfirmUpload_MD5Mismatch(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FileRepo = &mockFileRepo{md5: "abc123"}
	logic := NewConfirmUploadLogic(context.Background(), svcCtx)

	md5val := "d41d8cd98f00b204e9800998ecf8427e"
	_, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{
		FileId:     1,
		UploaderId: 1001,
		Md5:        &md5val,
	})
	assert.Error(t, err)
}

func TestConfirmUpload_SameMD5(t *testing.T) {
	svcCtx := newTestSvcCtx()
	md5val := "d41d8cd98f00b204e9800998ecf8427e"
	svcCtx.FileRepo = &mockFileRepo{md5: md5val}
	logic := NewConfirmUploadLogic(context.Background(), svcCtx)

	resp, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{
		FileId:     1,
		UploaderId: 1001,
		Md5:        &md5val,
	})
	require.NoError(t, err)
	assert.Equal(t, md5val, resp.File.Md5)
}

func TestConfirmUpload_NotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FileRepo = &mockFileRepo{notFound: true}
	logic := NewConfirmUploadLogic(context.Background(), svcCtx)

	_, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{FileId: 999, UploaderId: 1001})
	assert.Error(t, err)
}

func TestConfirmUpload_NotUploader(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewConfirmUploadLogic(context.Background(), svcCtx)

	_, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{FileId: 1, UploaderId: 9999})
	assert.Error(t, err)
}

// ========== GetDownloadURL Tests ==========

func TestGetDownloadURL_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewGetDownloadURLLogic(context.Background(), svcCtx)

	resp, err := logic.GetDownloadURL(&filepb.GetDownloadURLReq{FileId: 1, UserId: 1001, ExpiresIn: 3600})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.DownloadUrl)
	assert.NotNil(t, resp.File)
}

func TestGetDownloadURL_AccessDenied(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewGetDownloadURLLogic(context.Background(), svcCtx)

	_, err := logic.GetDownloadURL(&filepb.GetDownloadURLReq{FileId: 1, UserId: 9999, ExpiresIn: 3600})
	assert.Error(t, err)
}

func TestGetDownloadURL_PublicAccess(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FileRepo = &mockFileRepo{access: int32(filepb.FileAccess_FILE_ACCESS_PUBLIC)}
	logic := NewGetDownloadURLLogic(context.Background(), svcCtx)

	resp, err := logic.GetDownloadURL(&filepb.GetDownloadURLReq{FileId: 1, UserId: 9999, ExpiresIn: 3600})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.DownloadUrl)
}

// ========== GetFileInfo Tests ==========

func TestGetFileInfo_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewGetFileInfoLogic(context.Background(), svcCtx)

	resp, err := logic.GetFileInfo(&filepb.GetFileInfoReq{FileId: 1, UserId: 1001})
	require.NoError(t, err)
	assert.NotNil(t, resp.File)
}

func TestGetFileInfo_NotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FileRepo = &mockFileRepo{notFound: true}
	logic := NewGetFileInfoLogic(context.Background(), svcCtx)

	_, err := logic.GetFileInfo(&filepb.GetFileInfoReq{FileId: 999, UserId: 1001})
	assert.Error(t, err)
}

// ========== BatchGetFileInfo Tests ==========

func TestBatchGetFileInfo_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewBatchGetFileInfoLogic(context.Background(), svcCtx)

	resp, err := logic.BatchGetFileInfo(&filepb.BatchGetFileInfoReq{
		FileIds: []int64{1, 2},
		UserId:  1001,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Files, 2)
}

func TestBatchGetFileInfo_Empty(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewBatchGetFileInfoLogic(context.Background(), svcCtx)

	resp, err := logic.BatchGetFileInfo(&filepb.BatchGetFileInfoReq{FileIds: []int64{}, UserId: 1001})
	require.NoError(t, err)
	assert.Empty(t, resp.Files)
}

func TestBatchGetFileInfo_AccessFilter(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewBatchGetFileInfoLogic(context.Background(), svcCtx)

	resp, err := logic.BatchGetFileInfo(&filepb.BatchGetFileInfoReq{
		FileIds: []int64{1, 2},
		UserId:  9999, // not the uploader
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Files)
}

// ========== DeleteFile Tests ==========

func TestDeleteFile_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewDeleteFileLogic(context.Background(), svcCtx)

	resp, err := logic.DeleteFile(&filepb.DeleteFileReq{FileId: 1, UserId: 1001})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestDeleteFile_NotUploader(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewDeleteFileLogic(context.Background(), svcCtx)

	_, err := logic.DeleteFile(&filepb.DeleteFileReq{FileId: 1, UserId: 9999})
	assert.Error(t, err)
}

// ========== BatchDeleteFiles Tests ==========

func TestBatchDeleteFiles_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewBatchDeleteFilesLogic(context.Background(), svcCtx)

	resp, err := logic.BatchDeleteFiles(&filepb.BatchDeleteFilesReq{
		FileIds: []int64{1, 2},
		UserId:  1001,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestBatchDeleteFiles_Empty(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewBatchDeleteFilesLogic(context.Background(), svcCtx)

	resp, err := logic.BatchDeleteFiles(&filepb.BatchDeleteFilesReq{FileIds: []int64{}, UserId: 1001})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

// ========== UploadAvatar Tests ==========

func TestUploadAvatar_Success(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewUploadAvatarLogic(context.Background(), svcCtx)

	resp, err := logic.UploadAvatar(&filepb.UploadAvatarReq{
		Data:     []byte("fake-image-data"),
		UserId:   1001,
		MimeType: "image/jpeg",
	})
	require.NoError(t, err)
	assert.Greater(t, resp.FileId, int64(0))
	assert.NotEmpty(t, resp.Url)
}

func TestUploadAvatar_EmptyData(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewUploadAvatarLogic(context.Background(), svcCtx)

	_, err := logic.UploadAvatar(&filepb.UploadAvatarReq{UserId: 1001})
	assert.Error(t, err)
}

func TestUploadAvatar_BadMimeType(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewUploadAvatarLogic(context.Background(), svcCtx)

	_, err := logic.UploadAvatar(&filepb.UploadAvatarReq{
		Data:     []byte("data"),
		UserId:   1001,
		MimeType: "application/pdf",
	})
	assert.Error(t, err)
}

// ========== Helper Tests ==========

func TestExtFromName(t *testing.T) {
	assert.Equal(t, ".jpg", extFromName("photo.jpg"))
	assert.Equal(t, ".png", extFromName("screenshot.PNG"))
	assert.Equal(t, "", extFromName("noext"))
}

func TestFormatObjectKey(t *testing.T) {
	key := formatObjectKey(12345, ".jpg")
	assert.Contains(t, key, "files/")
	assert.Contains(t, key, "12345.jpg")
}

func TestToProtoFileInfo(t *testing.T) {
	f := &model.File{
		ID: 1, Name: "test.jpg", Key: "key", Size: 1024,
		MimeType: "image/jpeg", Ext: ".jpg", Width: 100, Height: 200,
		Purpose:    int32(filepb.FilePurpose_FILE_PURPOSE_MESSAGE),
		Access:     int32(filepb.FileAccess_FILE_ACCESS_CONVERSATION),
		UploaderID: 1001, Bucket: "aim", CreatedAt: time.Now(),
	}
	info := toProtoFileInfo(f)
	assert.Equal(t, int64(1), info.FileId)
	assert.Equal(t, "test.jpg", info.Name)
	assert.Equal(t, int64(1024), info.Size)
	assert.Equal(t, info.Ext, ".jpg")
}

func TestGrpcError(t *testing.T) {
	assert.Nil(t, grpcError(nil))
	assert.Contains(t, grpcError(ErrInvalidParam).Error(), "invalid parameter")
	assert.Contains(t, grpcError(ErrFileNotFound).Error(), "file not found")
	assert.Contains(t, grpcError(ErrAccessDenied).Error(), "access denied")
	assert.Contains(t, grpcError(ErrNotUploader).Error(), "not the uploader")
	assert.Contains(t, grpcError(ErrMD5Mismatch).Error(), "md5 mismatch")
	assert.Contains(t, grpcError(ErrUnsupportedType).Error(), "unsupported")
	// non-BizError fallback
	assert.NotNil(t, grpcError(assert.AnError))
}

func TestMimeToExt(t *testing.T) {
	assert.Equal(t, ".jpg", mimeToExt("image/jpeg"))
	assert.Equal(t, ".png", mimeToExt("image/png"))
	assert.Equal(t, ".gif", mimeToExt("image/gif"))
	assert.Equal(t, ".webp", mimeToExt("image/webp"))
	assert.Equal(t, ".jpg", mimeToExt("unknown/type"))
}

func TestDecodeImageDimensions(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 80))
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	w, h := decodeImageDimensions(buf.Bytes())
	assert.Equal(t, int32(100), w)
	assert.Equal(t, int32(80), h)
}

func TestDecodeImageDimensions_BadData(t *testing.T) {
	w, h := decodeImageDimensions([]byte("not an image"))
	assert.Equal(t, int32(0), w)
	assert.Equal(t, int32(0), h)
}

func TestCalcThumbSize(t *testing.T) {
	// No resize needed
	w, h := calcThumbSize(100, 80, 200, 200)
	assert.Equal(t, int32(100), w)
	assert.Equal(t, int32(80), h)
	// Width constrained
	w, h = calcThumbSize(400, 200, 200, 200)
	assert.Equal(t, int32(200), w)
	assert.Equal(t, int32(100), h)
	// Height constrained
	w, h = calcThumbSize(200, 400, 200, 200)
	assert.Equal(t, int32(100), w)
	assert.Equal(t, int32(200), h)
}

func TestGenerateThumbnail(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 200))
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}))
	thumb, err := generateThumbnail(buf.Bytes(), 100, 100)
	require.NoError(t, err)
	assert.NotEmpty(t, thumb)
	// Verify it's a valid JPEG
	_, _, err = image.Decode(bytes.NewReader(thumb))
	assert.NoError(t, err)
}

func TestGenerateThumbnail_BadData(t *testing.T) {
	_, err := generateThumbnail([]byte("not an image"), 100, 100)
	assert.Error(t, err)
}

func TestUploadAvatar_WithRealImage(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewUploadAvatarLogic(context.Background(), svcCtx)

	img := image.NewRGBA(image.Rect(0, 0, 150, 120))
	var imgBuf bytes.Buffer
	require.NoError(t, jpeg.Encode(&imgBuf, img, &jpeg.Options{Quality: 90}))

	resp, err := logic.UploadAvatar(&filepb.UploadAvatarReq{
		Data:     imgBuf.Bytes(),
		UserId:   1001,
		MimeType: "image/jpeg",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(150), resp.Width)
	assert.Equal(t, int32(120), resp.Height)
	assert.NotEmpty(t, resp.Url)
	assert.NotEmpty(t, resp.ThumbnailUrl)
}

func TestUploadAvatar_WithTargetSize(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewUploadAvatarLogic(context.Background(), svcCtx)

	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	var imgBuf bytes.Buffer
	require.NoError(t, jpeg.Encode(&imgBuf, img, &jpeg.Options{Quality: 90}))
	ts := int32(128)

	resp, err := logic.UploadAvatar(&filepb.UploadAvatarReq{
		Data:       imgBuf.Bytes(),
		UserId:     1001,
		MimeType:   "image/jpeg",
		TargetSize: &ts,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.ThumbnailUrl)
}

// ========== Resize Tests ==========

func TestResizeBilinear(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 80))
	dst := resizeBilinear(src, 50, 40)
	assert.Equal(t, 50, dst.Bounds().Dx())
	assert.Equal(t, 40, dst.Bounds().Dy())
}

func TestBilinearInterp(t *testing.T) {
	r := bilinearInterp(100, 200, 300, 400, 0.5, 0.5)
	assert.Equal(t, uint32(250), r)
	r = bilinearInterp(100, 100, 100, 100, 0.0, 0.0)
	assert.Equal(t, uint32(100), r)
}

// ========== Mock Implementations ==========

type mockFileRepo struct {
	notFound    bool
	access      int32
	md5         string
	trackUpdate bool
	updated     *model.File
}

func (m *mockFileRepo) Create(ctx context.Context, f *model.File) error { return nil }

func (m *mockFileRepo) GetByID(ctx context.Context, id int64) (*model.File, error) {
	if m.notFound {
		return nil, ErrFileNotFound
	}
	acc := m.access
	if acc == 0 {
		acc = int32(filepb.FileAccess_FILE_ACCESS_CONVERSATION)
	}
	return &model.File{
		ID: id, Name: "test.jpg", Key: "files/2026/01/01/test.jpg",
		Size: 1024, MimeType: "image/jpeg", Ext: ".jpg",
		Md5:        m.md5,
		Purpose:    int32(filepb.FilePurpose_FILE_PURPOSE_MESSAGE),
		Access:     acc,
		UploaderID: 1001, Bucket: "aim", CreatedAt: time.Now(),
	}, nil
}

func (m *mockFileRepo) BatchGetByIDs(ctx context.Context, ids []int64) ([]model.File, error) {
	if m.notFound {
		return nil, nil
	}
	acc := m.access
	if acc == 0 {
		acc = int32(filepb.FileAccess_FILE_ACCESS_CONVERSATION)
	}
	var result []model.File
	for _, id := range ids {
		result = append(result, model.File{
			ID: id, Name: "test.jpg", Key: "files/key",
			Size: 1024, MimeType: "image/jpeg", Ext: ".jpg",
			Purpose:    int32(filepb.FilePurpose_FILE_PURPOSE_MESSAGE),
			Access:     acc,
			UploaderID: 1001, Bucket: "aim", CreatedAt: time.Now(),
		})
	}
	return result, nil
}

func (m *mockFileRepo) Delete(ctx context.Context, id int64) error         { return nil }
func (m *mockFileRepo) BatchDelete(ctx context.Context, ids []int64) error { return nil }
func (m *mockFileRepo) Update(ctx context.Context, f *model.File) error {
	if m.trackUpdate {
		m.updated = f
	}
	return nil
}

type mockMinIO struct{}

func (m *mockMinIO) Bucket() string                         { return "aim" }
func (m *mockMinIO) EnsureBucket(ctx context.Context) error { return nil }
func (m *mockMinIO) Upload(ctx context.Context, objectName string, reader io.Reader, size int64, contentType string) (*svc.UploadInfo, error) {
	return &svc.UploadInfo{Size: size, Key: objectName}, nil
}
func (m *mockMinIO) Delete(ctx context.Context, objectName string) error { return nil }
func (m *mockMinIO) PresignedURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	return "https://minio.example.com/dl/" + objectName, nil
}
func (m *mockMinIO) PresignedPutURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	return "https://minio.example.com/up/" + objectName, nil
}
func (m *mockMinIO) SetPublicBucketPolicy(ctx context.Context) error { return nil }
func (m *mockMinIO) PublicURL(objectName string) string {
	return "https://minio.example.com/" + objectName
}

var _ repo.FileRepoInterface = (*mockFileRepo)(nil)
var _ svc.MinIOClient = (*mockMinIO)(nil)
