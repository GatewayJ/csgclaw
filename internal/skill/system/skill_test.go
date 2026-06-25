package system

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNamesReadsEmbeddedDirectory(t *testing.T) {
	got, err := Names()
	if err != nil {
		t.Fatalf("Names() error = %v", err)
	}
	if !slicesContains(got, "skill-installer") {
		t.Fatalf("Names() = %#v, want embedded skill-installer", got)
	}
}

func TestResolveSource(t *testing.T) {
	source, err := ResolveSource("skill-installer")
	if err != nil {
		t.Fatalf("ResolveSource() error = %v", err)
	}
	data, err := source.FS.Open(source.RootPath + "/SKILL.md")
	if err != nil {
		t.Fatalf("Open(system skill) error = %v", err)
	}
	defer data.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(data); err != nil {
		t.Fatalf("ReadFrom(system skill) error = %v", err)
	}
	if !strings.Contains(buf.String(), "registry skill search") {
		t.Fatalf("system skill content = %q, want skill-installer instructions", buf.String())
	}
}

func TestListRejectsEmbeddedDirectoryWithoutSkillFile(t *testing.T) {
	_, err := listFS(fstest.MapFS{
		"embed/broken/README.md": {Data: []byte("# Broken\n")},
	}, "embed")
	if err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("listFS() error = %v, want missing SKILL.md error", err)
	}
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
