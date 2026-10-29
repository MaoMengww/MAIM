package skill_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io/fs"
	"strings"
	"testing"

	"github.com/maomeng/aim/app/bot-service/internal/skill"
)

func encodedArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func fixedArchiveFiles() map[string]string {
	return map[string]string{
		"SKILL.md":             "---\nname: fixed-csv\ndescription: Process authorized CSV exactly.\nlicense: MIT\n---\nUse the published fixed skill over the accepted CSV.",
		"scripts/worker.py":    "print('review required')\n",
		"execution.json":       `{"image_digest":"sha256:` + strings.Repeat("a", 64) + `","entry_point":"python3 -I -B /opt/aim/worker.py"}`,
		"references/format.md": "Exact result.csv bytes required.",
	}
}

func TestParseArchivePreservesExactUnreviewedScriptIdentity(t *testing.T) {
	files := fixedArchiveFiles()
	pkg, err := skill.ParseArchive(encodedArchive(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Scripts) != 1 || pkg.Scripts["worker.py"] != files["scripts/worker.py"] || pkg.ImageDigest != "sha256:"+strings.Repeat("a", 64) || pkg.EntryPoint != "python3 -I -B /opt/aim/worker.py" {
		t.Fatalf("archive identity was changed or omitted: %+v", pkg)
	}
	if len(pkg.Resources) != 1 || pkg.Resources["references/format.md"] != files["references/format.md"] {
		t.Fatal("script or execution manifest was exposed as an instruction resource")
	}
	original := pkg.Digest
	files["scripts/worker.py"] += "# changed\n"
	changed, err := skill.ParseArchive(encodedArchive(t, files))
	if err != nil || changed.Digest == original {
		t.Fatalf("script changes did not change package identity: %v", err)
	}
}

func TestParseArchiveRejectsUnboundedOrArbitraryExecutionExtensions(t *testing.T) {
	for _, scenario := range []string{"missing-manifest", "missing-worker", "mutable-image", "custom-entry", "extra-command", "other-script", "oversized-worker", "traversal", "manifest-trailing-json"} {
		t.Run(scenario, func(t *testing.T) {
			files := fixedArchiveFiles()
			switch scenario {
			case "missing-manifest":
				delete(files, "execution.json")
			case "missing-worker":
				delete(files, "scripts/worker.py")
			case "mutable-image":
				files["execution.json"] = strings.ReplaceAll(files["execution.json"], "sha256:"+strings.Repeat("a", 64), "python:latest")
			case "custom-entry":
				files["execution.json"] = strings.ReplaceAll(files["execution.json"], "python3 -I -B /opt/aim/worker.py", "sh -c anything")
			case "extra-command":
				files["execution.json"] = strings.TrimSuffix(files["execution.json"], "}") + `,"command":"curl example.invalid"}`
			case "other-script":
				files["scripts/setup.sh"] = "exit 0\n"
			case "oversized-worker":
				files["scripts/worker.py"] = strings.Repeat("x", 65537)
			case "traversal":
				files["scripts/../worker.py"] = "invalid\n"
			case "manifest-trailing-json":
				files["execution.json"] += `{}`
			}
			if _, err := skill.ParseArchive(encodedArchive(t, files)); err == nil {
				t.Fatal("unsafe execution extension was admitted")
			}
		})
	}
}

func TestParseArchiveKeepsInstructionPackagesNonExecutable(t *testing.T) {
	files := fixedArchiveFiles()
	delete(files, "scripts/worker.py")
	delete(files, "execution.json")
	pkg, err := skill.ParseArchive(encodedArchive(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Scripts) != 0 || pkg.ImageDigest != "" || pkg.EntryPoint != "" {
		t.Fatal("instruction package acquired a script identity")
	}
}

func TestParseArchiveRejectsSpecialFilesAndAllowsCanonicalDirectoryEntries(t *testing.T) {
	for _, name := range []string{"scripts/worker.py", "execution.json"} {
		for _, mode := range []fs.FileMode{fs.ModeSymlink | 0777, fs.ModeNamedPipe | 0600, fs.ModeDevice | 0600, fs.ModeSocket | 0600} {
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			header := &zip.FileHeader{Name: name}
			header.SetMode(mode)
			member, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := member.Write([]byte("untrusted")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := skill.ParseArchive(base64.StdEncoding.EncodeToString(buffer.Bytes())); err == nil {
				t.Fatalf("special file %s (%s) accepted", name, mode)
			}
		}
	}
	files := fixedArchiveFiles()
	files["scripts/"] = ""
	if _, err := skill.ParseArchive(encodedArchive(t, files)); err != nil {
		t.Fatalf("canonical directory entry rejected: %v", err)
	}
}
