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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

const (
	testFileID        = "0194f0ca-0000-7000-8000-000000000001"
	testOtherFileID   = "0194f0ca-0000-7000-8000-000000000002"
	testMissingFileID = "0194f0ca-0000-7000-8000-000000000003"
	testUploaderID    = "0194f0cb-0000-7000-8000-000000000001"
	testOtherUserID   = "0194f0cb-0000-7000-8000-000000000002"
)

func newTestSvcCtx() *svc.ServiceContext {
	return &svc.ServiceContext{
		MinIO:    &mockMinIO{},
		FileRepo: &mockFileRepo{},
	}
}

func TestGetUploadURL_MissingName(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewGetUploadURLLogic(t.Context(), svcCtx)

	_, err := logic.GetUploadURL(&filepb.GetUploadURLReq{Size: 1024, UploaderId: testUploaderID})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ========== ConfirmUpload Tests ==========

func TestConfirmUpload_WithMD5(t *testing.T) {
	svcCtx := newTestSvcCtx()
	repository := &mockFileRepo{}
	svcCtx.FileRepo = repository
	logic := NewConfirmUploadLogic(t.Context(), svcCtx)

	md5val := "d41d8cd98f00b204e9800998ecf8427e"
	_, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{
		FileId:     testFileID,
		UploaderId: testUploaderID,
		Md5:        &md5val,
	})
	require.NoError(t, err)
	info, err := NewGetFileInfoLogic(t.Context(), svcCtx).GetFileInfo(&filepb.GetFileInfoReq{FileId: testFileID, UserId: testUploaderID})
	require.NoError(t, err)
	assert.Equal(t, md5val, info.File.Md5)
}

func TestConfirmUpload_MD5Mismatch(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FileRepo = &mockFileRepo{md5: "abc123"}
	logic := NewConfirmUploadLogic(t.Context(), svcCtx)

	md5val := "d41d8cd98f00b204e9800998ecf8427e"
	_, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{
		FileId:     testFileID,
		UploaderId: testUploaderID,
		Md5:        &md5val,
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestConfirmUpload_SameMD5(t *testing.T) {
	svcCtx := newTestSvcCtx()
	md5val := "d41d8cd98f00b204e9800998ecf8427e"
	svcCtx.FileRepo = &mockFileRepo{md5: md5val}
	logic := NewConfirmUploadLogic(t.Context(), svcCtx)

	_, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{
		FileId:     testFileID,
		UploaderId: testUploaderID,
		Md5:        &md5val,
	})
	require.NoError(t, err)
	differentMD5 := "5d41402abc4b2a76b9719d911017c592"
	_, err = logic.ConfirmUpload(&filepb.ConfirmUploadReq{FileId: testFileID, UploaderId: testUploaderID, Md5: &differentMD5})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	info, err := NewGetFileInfoLogic(t.Context(), svcCtx).GetFileInfo(&filepb.GetFileInfoReq{FileId: testFileID, UserId: testUploaderID})
	require.NoError(t, err)
	assert.Equal(t, md5val, info.File.Md5)
}

func TestConfirmUpload_NotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FileRepo = &mockFileRepo{notFound: true}
	logic := NewConfirmUploadLogic(t.Context(), svcCtx)

	_, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{FileId: testMissingFileID, UploaderId: testUploaderID})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestConfirmUpload_NotUploader(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewConfirmUploadLogic(t.Context(), svcCtx)

	_, err := logic.ConfirmUpload(&filepb.ConfirmUploadReq{FileId: testFileID, UploaderId: testOtherUserID})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// ========== GetDownloadURL Tests ==========

func TestGetDownloadURL_AccessDenied(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewGetDownloadURLLogic(t.Context(), svcCtx)

	_, err := logic.GetDownloadURL(&filepb.GetDownloadURLReq{FileId: testFileID, UserId: testOtherUserID, ExpiresIn: 3600})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// ========== GetFileInfo Tests ==========

func TestGetFileInfo_NotFound(t *testing.T) {
	svcCtx := newTestSvcCtx()
	svcCtx.FileRepo = &mockFileRepo{notFound: true}
	logic := NewGetFileInfoLogic(t.Context(), svcCtx)

	_, err := logic.GetFileInfo(&filepb.GetFileInfoReq{FileId: testMissingFileID, UserId: testUploaderID})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ========== BatchGetFileInfo Tests ==========

func TestBatchGetFileInfo_AccessFilter(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewBatchGetFileInfoLogic(t.Context(), svcCtx)

	resp, err := logic.BatchGetFileInfo(&filepb.BatchGetFileInfoReq{
		FileIds: []string{testFileID, testOtherFileID},
		UserId:  testOtherUserID, // not the uploader
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Files)
}

// ========== DeleteFile Tests ==========

func TestDeleteFile_NotUploader(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewDeleteFileLogic(t.Context(), svcCtx)

	_, err := logic.DeleteFile(&filepb.DeleteFileReq{FileId: testFileID, UserId: testOtherUserID})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// ========== BatchDeleteFiles Tests ==========

// ========== UploadAvatar Tests ==========

func TestUploadAvatar_EmptyData(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewUploadAvatarLogic(t.Context(), svcCtx)

	_, err := logic.UploadAvatar(&filepb.UploadAvatarReq{UserId: testUploaderID})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUploadAvatar_BadMimeType(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewUploadAvatarLogic(t.Context(), svcCtx)

	_, err := logic.UploadAvatar(&filepb.UploadAvatarReq{
		Data:     []byte("data"),
		UserId:   testUploaderID,
		MimeType: "application/pdf",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ========== Helper Tests ==========

func TestExtFromName(t *testing.T) {
	assert.Equal(t, ".jpg", extFromName("photo.jpg"))
	assert.Equal(t, ".png", extFromName("screenshot.PNG"))
	assert.Equal(t, "", extFromName("noext"))
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
	decoded, format, err := image.Decode(bytes.NewReader(thumb))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)
	assert.Equal(t, 100, decoded.Bounds().Dx())
	assert.Equal(t, 66, decoded.Bounds().Dy())
}

func TestGenerateThumbnail_BadData(t *testing.T) {
	_, err := generateThumbnail([]byte("not an image"), 100, 100)
	assert.Error(t, err)
}

func TestUploadAvatar_WithRealImage(t *testing.T) {
	svcCtx := newTestSvcCtx()
	logic := NewUploadAvatarLogic(t.Context(), svcCtx)

	img := image.NewRGBA(image.Rect(0, 0, 150, 120))
	var imgBuf bytes.Buffer
	require.NoError(t, jpeg.Encode(&imgBuf, img, &jpeg.Options{Quality: 90}))

	resp, err := logic.UploadAvatar(&filepb.UploadAvatarReq{
		Data:     imgBuf.Bytes(),
		UserId:   testUploaderID,
		MimeType: "image/jpeg",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(150), resp.Width)
	assert.Equal(t, int32(120), resp.Height)
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
	notFound bool
	access   int32
	md5      string
}

func (m *mockFileRepo) Create(ctx context.Context, f *model.File) error { return nil }

func (m *mockFileRepo) GetByID(ctx context.Context, id string) (*model.File, error) {
	if m.notFound {
		return nil, gorm.ErrRecordNotFound
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
		UploaderID: testUploaderID, Bucket: "aim", CreatedAt: time.Now(),
	}, nil
}

func (m *mockFileRepo) BatchGetByIDs(ctx context.Context, ids []string) ([]model.File, error) {
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
			UploaderID: testUploaderID, Bucket: "aim", CreatedAt: time.Now(),
		})
	}
	return result, nil
}

func (m *mockFileRepo) Delete(ctx context.Context, id string) error         { return nil }
func (m *mockFileRepo) BatchDelete(ctx context.Context, ids []string) error { return nil }
func (m *mockFileRepo) Update(ctx context.Context, f *model.File) error {
	m.md5 = f.Md5
	return nil
}

type mockMinIO struct{}

func (m *mockMinIO) Bucket() string                         { return "aim" }
func (m *mockMinIO) EnsureBucket(ctx context.Context) error { return nil }
func (m *mockMinIO) Upload(ctx context.Context, objectName string, reader io.Reader, size int64, contentType string) (*svc.UploadInfo, error) {
	return &svc.UploadInfo{Size: size, Key: objectName}, nil
}
func (m *mockMinIO) Delete(ctx context.Context, objectName string) error { return nil }
func (m *mockMinIO) Stat(ctx context.Context, objectName string) (int64, string, error) {
	return 1024, "", nil
}
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
