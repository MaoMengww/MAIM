// Package knowledgecontract defines the public bounds shared by knowledge callers.
package knowledgecontract

const (
	DefaultSearchLimit       = 5
	MaxSearchLimit           = 50
	DefaultListLimit         = 20
	MaxListLimit             = 100
	DefaultDocumentListLimit = 20
	MaxDocumentListLimit     = 100
	DefaultChunkReadLimit    = 20
	MaxChunkReadLimit        = 100
	// Reading offsets are zero-based rows in the filtered result; chunk-index
	// bounds are inclusive. Negative values and explicit limits above the max
	// are invalid; zero limits select the defaults above.
	// MaxReadingResponseBytes bounds serialized document/chunk page output.
	// Whole records are returned; an oversized first record is an explicit error.
	MaxReadingResponseBytes = 65536
)
