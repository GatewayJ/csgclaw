package local

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListReturnsSkillDirectoriesWithDescriptions(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "alpha", "SKILL.md"), "---\ndescription: First skill\n---\n# Alpha\n")
	mustWriteFile(t, filepath.Join(root, "beta", "SKILL.md"), "# Beta\n")
	mustWriteFile(t, filepath.Join(root, "notes.md"), "ignore")
	if err := os.MkdirAll(filepath.Join(root, "gamma"), 0o755); err != nil {
		t.Fatalf("MkdirAll(gamma) error = %v", err)
	}

	got, err := List(root)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(got))
	}
	if got[0].Name != "alpha" || got[0].Description != "First skill" {
		t.Fatalf("first skill = %+v, want alpha with description", got[0])
	}
	if got[1].Name != "beta" || got[1].Description != "" {
		t.Fatalf("second skill = %+v, want beta without description", got[1])
	}
}

func TestListParsesBlockScalarDescription(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "alpha", "SKILL.md"), "---\ndescription: >\n  line one\n  line two\n---\n# Alpha\n")

	got, err := List(root)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(got))
	}
	if got[0].Description != "line one line two" {
		t.Fatalf("description = %q, want %q", got[0].Description, "line one line two")
	}
}

func TestListIncludesRemoteMetadata(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "alpha", "SKILL.md"), "# Alpha\n")
	if err := WriteRemoteMetadata(root, "alpha", RemoteMetadata{
		RemoteSource: "https://opencsg-stg.example.test",
		RemotePath:   "owner/alpha",
	}); err != nil {
		t.Fatalf("WriteRemoteMetadata() error = %v", err)
	}

	got, err := List(root)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 ||
		got[0].RemoteSource != "https://opencsg-stg.example.test" ||
		got[0].RemotePath != "owner/alpha" {
		t.Fatalf("List() = %+v, want alpha with remote metadata", got)
	}
	if rel, err := filepath.Rel(root, remoteMetadataDir(root)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("remote metadata dir %q should not be inside skills root %q", remoteMetadataDir(root), root)
	}
}

func TestDeleteRemovesSkillDirectory(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "alpha", "SKILL.md"), "# Alpha\n")
	mustWriteFile(t, filepath.Join(root, "alpha", "scripts", "run.sh"), "#!/bin/sh\necho hi\n")
	if err := WriteRemoteMetadata(root, "alpha", RemoteMetadata{RemoteSource: "https://opencsg-stg.example.test", RemotePath: "owner/alpha"}); err != nil {
		t.Fatalf("WriteRemoteMetadata() error = %v", err)
	}

	if err := Delete(root, "alpha"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "alpha")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alpha still exists, err = %v", err)
	}
	if _, err := os.Stat(remoteMetadataPath(root, "alpha")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alpha remote metadata still exists, err = %v", err)
	}
}

func TestDeleteRejectsUnsafeName(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "alpha", "SKILL.md"), "# Alpha\n")

	if err := Delete(root, "../alpha"); err == nil {
		t.Fatalf("Delete() error = nil, want invalid skill name")
	}
	if _, err := os.Stat(filepath.Join(root, "alpha", "SKILL.md")); err != nil {
		t.Fatalf("alpha skill unexpectedly changed: %v", err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func TestInstallArchive(t *testing.T) {
	root := t.TempDir()
	if err := WriteRemoteMetadata(root, "alpha", RemoteMetadata{RemoteSource: "https://opencsg-stg.example.test", RemotePath: "owner/alpha"}); err != nil {
		t.Fatalf("WriteRemoteMetadata() error = %v", err)
	}

	got, err := InstallArchive(root, "alpha.zip", mustZip(t, map[string]string{
		"alpha/SKILL.md":       "---\ndescription: First skill\n---\n# Alpha\n",
		"alpha/scripts/run.sh": "#!/bin/sh\necho hi\n",
	}))
	if err != nil {
		t.Fatalf("InstallArchive() error = %v", err)
	}
	if got.Name != "alpha" || got.Description != "First skill" {
		t.Fatalf("InstallArchive() = %+v, want alpha summary", got)
	}
	if _, err := os.Stat(filepath.Join(root, "alpha", "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md missing: %v", err)
	}
	listed, err := List(root)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].RemoteSource != "" || listed[0].RemotePath != "" {
		t.Fatalf("List() = %+v, want local install without stale remote metadata", listed)
	}
}

func TestInstallArchiveRejectsDuplicate(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "alpha", "SKILL.md"), "# Alpha\n")

	_, err := InstallArchive(root, "alpha.zip", mustZip(t, map[string]string{
		"alpha/SKILL.md": "# Alpha\n",
	}))
	if !errors.Is(err, ErrSkillAlreadyExists) {
		t.Fatalf("InstallArchive() error = %v, want ErrSkillAlreadyExists", err)
	}
}

func TestInstallArchiveRejectsSystemSkillName(t *testing.T) {
	root := t.TempDir()

	_, err := InstallArchive(root, "skill-installer.zip", mustZip(t, map[string]string{
		"skill-installer/SKILL.md": "# Skill Installer\n",
	}))
	if !errors.Is(err, ErrSkillAlreadyExists) {
		t.Fatalf("InstallArchive() error = %v, want ErrSkillAlreadyExists", err)
	}
}

func TestInstallArchiveRejectsInvalidShape(t *testing.T) {
	root := t.TempDir()

	_, err := InstallArchive(root, "invalid.zip", mustZip(t, map[string]string{
		"alpha/SKILL.md": "# Alpha\n",
		"beta/SKILL.md":  "# Beta\n",
	}))
	if !errors.Is(err, ErrSkillArchiveInvalid) {
		t.Fatalf("InstallArchive() error = %v, want ErrSkillArchiveInvalid", err)
	}
}

func TestInstallArchiveRejectsZipSlip(t *testing.T) {
	root := t.TempDir()

	_, err := InstallArchive(root, "escape.zip", mustZip(t, map[string]string{
		"../escape/SKILL.md": "# Escape\n",
	}))
	if !errors.Is(err, ErrSkillArchiveUnsafe) {
		t.Fatalf("InstallArchive() error = %v, want ErrSkillArchiveUnsafe", err)
	}
}

func mustZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) error = %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) error = %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return buf.Bytes()
}
