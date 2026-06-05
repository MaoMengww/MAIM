package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	FileUploadTotal = metrics.NewCounterVec("file_upload_total",
		"Total file uploads", "category")
	FileStorageBytes = metrics.NewGaugeVec("file_storage_bytes",
		"Total file storage in bytes", "category")
)

func PurposeCategory(purpose int32) string {
	switch purpose {
	case 1:
		return "message"
	case 2:
		return "avatar"
	case 3:
		return "document"
	case 4:
		return "media"
	default:
		return "file"
	}
}
