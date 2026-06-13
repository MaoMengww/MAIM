package consts

// ---- HTTP headers ----

const (
	HeaderRequestID    = "X-Request-ID"
	HeaderToken        = "Authorization"
	HeaderContentType  = "Content-Type"
	HeaderAIMSignature = "X-AIM-Signature"
	HeaderAIMTimestamp = "X-AIM-Timestamp"
)

// ---- Content types ----

const (
	ContentTypeJSON = "application/json"
)

// ---- gRPC metadata keys ----

const (
	MetadataKeyUserID    = "user-id"
	MetadataKeyDeviceID  = "device-id"
	MetadataKeyRequestID = "request-id"
)
