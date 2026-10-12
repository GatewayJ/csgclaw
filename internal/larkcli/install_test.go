package larkcli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func skillArchive(t *testing.T, name string, kind byte) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	body := []byte("---\nname: lark-test\ndescription: Test skill\n---\nOfficial test instructions\n")
	if kind != tar.TypeReg {
		body = nil
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Typeflag: kind, Size: int64(len(body)), Linkname: "/tmp/unrelated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func TestDownloadChecksIntegrityAndSize(t *testing.T) {
	body := []byte("official archive")
	sum := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	installer := Installer{Client: server.Client()}
	for _, tc := range []struct {
		name, digest string
		limit        int64
		ok           bool
	}{{"valid", hex.EncodeToString(sum[:]), 1024, true}, {"checksum", "invalid", 1024, false}, {"size", hex.EncodeToString(sum[:]), 2, false}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := installer.download(context.Background(), server.URL, tc.digest, tc.limit)
			if (err == nil) != tc.ok {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
func TestSkillsArchiveRejectsTraversalAndSymlinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
	}{{"repo/skills/lark-test/../../outside", tar.TypeReg}, {"repo/skills/lark-test/SKILL.md", tar.TypeSymlink}} {
		if err := installSkills(t.TempDir(), skillArchive(t, tc.name, tc.kind)); err == nil {
			t.Fatalf("accepted %s", tc.name)
		}
	}
	root := t.TempDir()
	if err := installSkills(root, skillArchive(t, "repo/skills/lark-test/SKILL.md", tar.TypeReg)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "lark-test", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}
func TestInstalledPackageDetectsModifiedSkills(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell executable fixture")
	}
	root := t.TempDir()
	p := Package{Executable: filepath.Join(root, "bin", "lark-cli"), SkillsDir: filepath.Join(root, "skills"), Version: Version}
	if err := os.MkdirAll(filepath.Dir(p.Executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Executable, []byte("#!/bin/sh\nprintf 'lark-cli "+Version+"\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := installSkills(p.SkillsDir, skillArchive(t, "repo/skills/lark-test/SKILL.md", tar.TypeReg)); err != nil {
		t.Fatal(err)
	}
	if err := writePackageManifest(p); err != nil {
		t.Fatal(err)
	}
	if err := verifyPackage(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.SkillsDir, "lark-test", "SKILL.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyPackage(context.Background(), p); err == nil {
		t.Fatal("modified Skills were accepted")
	}
}
func TestPinnedOfficialPackage(t *testing.T) {
	if os.Getenv("CSGCLAW_TEST_OFFICIAL_CLI") != "1" {
		t.Skip("official package download opt-in")
	}
	root := t.TempDir()
	installer := Installer{Root: root}
	p, err := installer.Install(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPackage(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	second, err := installer.Install(context.Background(), nil)
	if err != nil || second != p {
		t.Fatalf("repeat=%+v error=%v", second, err)
	}
}
