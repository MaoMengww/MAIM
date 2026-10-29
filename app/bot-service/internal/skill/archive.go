package skill

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxArchiveBytes      = 2 << 20
	maxPackageBytes      = 4 << 20
	maxMemberBytes       = 1 << 20
	maxArchiveMembers    = 64
	maxInstructionBytes  = 64 << 10
	maxDescriptionBytes  = 1024
	maxLicenseBytes      = 128
	maxResourceNameBytes = 200
)

var (
	skillNamePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	imageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	licensePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+\- ]{0,127}$`)
	scriptExtensions   = []string{".sh", ".bash", ".zsh", ".fish", ".py", ".pyc", ".js", ".mjs", ".cjs", ".ts", ".rb", ".pl", ".pm", ".php", ".lua", ".exe", ".dll", ".so", ".dylib", ".bat", ".cmd", ".ps1", ".jar", ".wasm"}
	// The instruction-only path does not interpret these package extensions.
	unsupportedFrontmatter = []string{"model", "models", "fork", "mcp", "mcp_servers", "mcpservers", "hooks", "commands", "command", "allowed-tools", "allowed_tools", "scripts"}
	allowedFrontmatter     = []string{"name", "description", "license"}
)

// ImportError is an author-facing diagnostic. It never reflects filesystem or
// database internals and is safe to return to the publisher.
type ImportError struct {
	Reason string
}

func (e *ImportError) Error() string { return e.Reason }

func invalid(reason string) error { return &ImportError{Reason: reason} }

// ParseArchive decodes an open Agent Skills package. The entire archive is
// bounded and digested; a rejected package never produces a usable row.
func ParseArchive(encoded string) (*Package, error) {
	if len(encoded) > maxArchiveBytes*4/3+16 {
		return nil, invalid("archive exceeds the 2 MiB encoded limit")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, invalid("archive must be base64-encoded zip content")
	}
	if len(raw) == 0 || len(raw) > maxArchiveBytes {
		return nil, invalid("archive must be non-empty and at most 2 MiB")
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, invalid("archive is not a readable zip file")
	}
	if len(reader.File) > maxArchiveMembers {
		return nil, invalid(fmt.Sprintf("archive has more than %d members", maxArchiveMembers))
	}
	files := make(map[string][]byte)
	total := int64(0)
	for _, member := range reader.File {
		name := member.Name
		if member.FileInfo().IsDir() {
			name = strings.TrimSuffix(name, "/")
		}
		if name == "" || len(name) > maxResourceNameBytes || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || path.Clean(name) != name || strings.HasPrefix(name, "../") || name == ".." {
			return nil, invalid(fmt.Sprintf("unsafe archive member path %q", name))
		}
		if member.FileInfo().IsDir() {
			continue
		}
		if !member.FileInfo().Mode().IsRegular() || member.FileInfo().Mode()&(ioModeSymlink|ioModeDevice|ioModeNamedPipe|ioModeSocket) != 0 {
			return nil, invalid(fmt.Sprintf("archive member %q is not a regular file", name))
		}
		if _, duplicate := files[name]; duplicate {
			return nil, invalid(fmt.Sprintf("archive member %q is duplicated", name))
		}
		if !allowedMember(name) {
			return nil, invalid(fmt.Sprintf("archive member %q is outside supported roots (SKILL.md, references/, assets/, scripts/worker.py, execution.json)", name))
		}
		if member.UncompressedSize64 > maxMemberBytes {
			return nil, invalid(fmt.Sprintf("archive member %q exceeds the 1 MiB limit", name))
		}
		total += int64(member.UncompressedSize64)
		if total > maxPackageBytes {
			return nil, invalid("archive expands beyond the 4 MiB package limit")
		}
		stream, err := member.Open()
		if err != nil {
			return nil, invalid(fmt.Sprintf("archive member %q cannot be read", name))
		}
		content, readErr := io.ReadAll(io.LimitReader(stream, maxMemberBytes+1))
		stream.Close()
		if readErr != nil || len(content) > maxMemberBytes {
			return nil, invalid(fmt.Sprintf("archive member %q exceeds the 1 MiB limit", name))
		}
		if !utf8.Valid(content) {
			return nil, invalid(fmt.Sprintf("archive member %q is not UTF-8 text; the instruction-only path cannot load binary assets", name))
		}
		files[name] = content
	}
	instructions, ok := files["SKILL.md"]
	if !ok {
		return nil, invalid("archive must contain SKILL.md at the package root")
	}
	meta, body, err := parseFrontmatter(string(instructions))
	if err != nil {
		return nil, err
	}
	pkg := &Package{Name: meta["name"], Description: meta["description"], License: meta["license"], Instructions: body, Resources: make(map[string]string)}
	if err := validateMetadata(pkg); err != nil {
		return nil, err
	}
	worker, hasWorker := files["scripts/worker.py"]
	manifest, hasManifest := files["execution.json"]
	if hasWorker != hasManifest {
		return nil, invalid("fixed scripts require both scripts/worker.py and execution.json")
	}
	if hasWorker {
		var execution struct {
			ImageDigest string `json:"image_digest"`
			EntryPoint  string `json:"entry_point"`
		}
		decoder := json.NewDecoder(bytes.NewReader(manifest))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&execution) != nil || decoder.Decode(new(any)) != io.EOF || len(worker) == 0 || len(worker) > 65536 ||
			!imageDigestPattern.MatchString(execution.ImageDigest) || execution.EntryPoint != "python3 -I -B /opt/aim/worker.py" {
			return nil, invalid("execution.json requires an immutable sha256 image and the fixed CSV entry; worker.py must contain 1-65536 bytes")
		}
		pkg.Scripts = map[string]string{"worker.py": string(worker)}
		pkg.ImageDigest, pkg.EntryPoint = execution.ImageDigest, execution.EntryPoint
	}
	for name, content := range files {
		if name != "SKILL.md" && name != "execution.json" && name != "scripts/worker.py" {
			pkg.Resources[name] = string(content)
		}
	}
	pkg.Digest, err = packageDigest(files)
	if err != nil {
		return nil, err
	}
	return pkg, nil
}

// ioMode* mirror the os.FileMode bits used by zip headers without importing os
// into the pure archive boundary.
const (
	ioModeSymlink   = 1 << 27
	ioModeDevice    = 1 << 26
	ioModeNamedPipe = 1 << 25
	ioModeSocket    = 1 << 24
)

func allowedMember(name string) bool {
	if name == "SKILL.md" || name == "execution.json" || name == "scripts/worker.py" {
		return true
	}
	if strings.HasPrefix(name, "scripts/") {
		return false
	}
	resource := strings.HasPrefix(name, "references/") || strings.HasPrefix(name, "assets/")
	if !resource {
		return false
	}
	remainder := strings.TrimPrefix(strings.TrimPrefix(name, "references/"), "assets/")
	if remainder == "" {
		return false
	}
	lower := strings.ToLower(name)
	for _, extension := range scriptExtensions {
		if strings.HasSuffix(lower, extension) {
			return false
		}
	}
	return true
}

func parseFrontmatter(document string) (map[string]string, string, error) {
	normalized := strings.ReplaceAll(document, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return nil, "", invalid("SKILL.md must start with YAML frontmatter delimited by ---")
	}
	rest := normalized[len("---\n"):]
	metadata := make(map[string]string)
	closed := false
	var body strings.Builder
	lines := strings.Split(rest, "\n")
	for index, line := range lines {
		if !closed {
			if strings.TrimSpace(line) == "---" {
				closed = true
				body.WriteString(strings.Join(lines[index+1:], "\n"))
				break
			}
			key, value, ok := strings.Cut(line, ":")
			key = strings.TrimSpace(key)
			if !ok || key == "" {
				return nil, "", invalid("frontmatter entries must be key: value lines")
			}
			if _, duplicate := metadata[key]; duplicate {
				return nil, "", invalid(fmt.Sprintf("frontmatter key %q is duplicated", key))
			}
			if slices.Contains(unsupportedFrontmatter, strings.ToLower(key)) {
				return nil, "", invalid(fmt.Sprintf("package extension %q is not supported by the instruction-only path", key))
			}
			if !slices.Contains(allowedFrontmatter, key) {
				return nil, "", invalid(fmt.Sprintf("frontmatter key %q is not part of the supported core metadata", key))
			}
			metadata[key] = unquote(strings.TrimSpace(value))
		}
	}
	if !closed {
		return nil, "", invalid("SKILL.md frontmatter is not closed")
	}
	for _, required := range []string{"name", "description", "license"} {
		if strings.TrimSpace(metadata[required]) == "" {
			return nil, "", invalid(fmt.Sprintf("frontmatter requires a non-empty %s", required))
		}
	}
	bodyText := strings.TrimSpace(body.String())
	if bodyText == "" {
		return nil, "", invalid("SKILL.md must contain instruction text after the frontmatter")
	}
	if len(bodyText) > maxInstructionBytes {
		return nil, "", invalid("SKILL.md instructions exceed the 64 KiB limit")
	}
	return metadata, bodyText, nil
}

func unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func validateMetadata(pkg *Package) error {
	if !skillNamePattern.MatchString(pkg.Name) {
		return invalid("skill name must be 1-64 lowercase letters, digits or hyphens and start with a letter or digit")
	}
	if strings.TrimSpace(pkg.Description) == "" || len(pkg.Description) > maxDescriptionBytes {
		return invalid("skill description must contain between 1 and 1024 bytes")
	}
	if !licensePattern.MatchString(pkg.License) {
		return invalid("skill license must be an explicit identifier of at most 128 characters")
	}
	return nil
}

func packageDigest(files map[string][]byte) (string, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	digest := sha256.New()
	for _, name := range names {
		sum := sha256.Sum256(files[name])
		digest.Write([]byte(name))
		digest.Write([]byte{0})
		digest.Write([]byte(hex.EncodeToString(sum[:])))
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func contentDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
