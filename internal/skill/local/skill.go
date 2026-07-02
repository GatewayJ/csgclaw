package local

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"csgclaw/internal/config"

	"gopkg.in/yaml.v3"
)

const skillFileName = "SKILL.md"

var ErrSkillInvalid = errors.New("skill directory must contain SKILL.md")

type SkillSummary struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	RemoteSource string `json:"remoteSource,omitempty"`
	RemotePath   string `json:"remotePath,omitempty"`
}

type RemoteMetadata struct {
	RemoteSource string `json:"remote_source,omitempty"`
	RemotePath   string `json:"remote_path"`
}

func SkillsRoot() (string, error) {
	dir, err := config.DefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "skills"), nil
}

func List(root string) ([]SkillSummary, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("skills root is required")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	items := make([]SkillSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(root, entry.Name(), skillFileName)
		info, err := os.Stat(skillPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat skill file %q: %w", skillPath, err)
		}
		if info.IsDir() {
			continue
		}
		description, err := skillDescription(skillPath)
		if err != nil {
			return nil, err
		}
		remoteMetadata := readRemoteMetadata(root, entry.Name())
		items = append(items, SkillSummary{
			Name:         entry.Name(),
			Description:  description,
			RemoteSource: remoteMetadata.RemoteSource,
			RemotePath:   remoteMetadata.RemotePath,
		})
	}
	slices.SortFunc(items, func(left, right SkillSummary) int {
		return strings.Compare(left.Name, right.Name)
	})
	return items, nil
}

func Delete(root, name string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("skills root is required")
	}
	cleanName, err := NormalizeName(name)
	if err != nil {
		return err
	}
	skillDir, err := ResolveDir(root, cleanName)
	if err != nil {
		return err
	}
	if err := deleteRemoteMetadata(root, cleanName); err != nil {
		return err
	}
	return os.RemoveAll(skillDir)
}

func WriteRemoteMetadata(root, name string, metadata RemoteMetadata) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("skills root is required")
	}
	cleanName, err := NormalizeName(name)
	if err != nil {
		return err
	}
	remotePath := strings.TrimSpace(metadata.RemotePath)
	if remotePath == "" {
		return deleteRemoteMetadata(root, cleanName)
	}
	metadata.RemoteSource = strings.TrimSpace(metadata.RemoteSource)
	metadata.RemotePath = remotePath
	dir := remoteMetadataDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create remote skill metadata dir: %w", err)
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal remote skill metadata: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(remoteMetadataPath(root, cleanName), data, 0o644); err != nil {
		return fmt.Errorf("write remote skill metadata: %w", err)
	}
	return nil
}

func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("skill name is required")
	}
	cleanName := filepath.Clean(name)
	if cleanName == "." || cleanName == ".." || cleanName != filepath.Base(cleanName) {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	return cleanName, nil
}

func ResolveDir(root, name string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("skills root is required")
	}
	cleanName, err := NormalizeName(name)
	if err != nil {
		return "", err
	}
	skillDir := filepath.Join(root, cleanName)
	info, err := os.Stat(skillDir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", ErrSkillInvalid
	}
	skillFile := filepath.Join(skillDir, skillFileName)
	fileInfo, err := os.Lstat(skillFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrSkillInvalid
		}
		return "", err
	}
	if !fileInfo.Mode().IsRegular() || fileInfo.IsDir() || fileInfo.Mode()&fs.ModeSymlink != 0 {
		return "", ErrSkillInvalid
	}
	return skillDir, nil
}

func skillDescription(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read skill file %q: %w", path, err)
	}
	frontmatter, ok := extractFrontmatter(data)
	if !ok {
		return "", nil
	}
	var meta struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(frontmatter, &meta); err != nil {
		return "", fmt.Errorf("parse skill frontmatter %q: %w", path, err)
	}
	return strings.TrimSpace(meta.Description), nil
}

func readRemoteMetadata(root, name string) RemoteMetadata {
	cleanName, err := NormalizeName(name)
	if err != nil {
		return RemoteMetadata{}
	}
	data, err := os.ReadFile(remoteMetadataPath(root, cleanName))
	if err != nil {
		return RemoteMetadata{}
	}
	var metadata RemoteMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return RemoteMetadata{}
	}
	metadata.RemoteSource = strings.TrimSpace(metadata.RemoteSource)
	metadata.RemotePath = strings.TrimSpace(metadata.RemotePath)
	return metadata
}

func deleteRemoteMetadata(root, name string) error {
	cleanName, err := NormalizeName(name)
	if err != nil {
		return err
	}
	if err := os.Remove(remoteMetadataPath(root, cleanName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete remote skill metadata: %w", err)
	}
	return nil
}

func remoteMetadataDir(root string) string {
	root = filepath.Clean(root)
	return filepath.Join(filepath.Dir(root), "."+filepath.Base(root)+"-remote-skills")
}

func remoteMetadataPath(root, name string) string {
	return filepath.Join(remoteMetadataDir(root), name+".json")
}

func extractFrontmatter(data []byte) ([]byte, bool) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, false
	}
	rest := data[len("---\n"):]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		end = bytes.Index(rest, []byte("\n---\r\n"))
	}
	if end < 0 {
		return nil, false
	}
	return rest[:end], true
}
