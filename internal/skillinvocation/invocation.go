package skillinvocation

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxSkillContentBytes = 256 * 1024
	maxSupportingFiles   = 50
)

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type Invocation struct {
	Slug        string
	Instruction string
}

type BuildOptions struct {
	WorkspaceRoot   string
	Slug            string
	Instruction     string
	RuntimeSkillDir string
}

func ParseSlash(content string) (Invocation, bool) {
	text := strings.TrimSpace(content)
	if !strings.HasPrefix(text, "/") {
		return Invocation{}, false
	}

	slug, instruction := splitSlashCommand(strings.TrimPrefix(text, "/"))
	if !validSlug(slug) {
		return Invocation{}, false
	}
	return Invocation{
		Slug:        slug,
		Instruction: strings.TrimSpace(instruction),
	}, true
}

func BuildMessage(opts BuildOptions) (string, error) {
	root := strings.TrimSpace(opts.WorkspaceRoot)
	slug := strings.TrimSpace(opts.Slug)
	if root == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	if !validSlug(slug) {
		return "", fmt.Errorf("invalid skill slug %q", slug)
	}

	skillRel := path.Join("skills", slug)
	skillDir := filepath.Join(root, filepath.FromSlash(skillRel))
	if err := ensureDir(skillDir); err != nil {
		return "", err
	}

	skillContent, err := readSkillFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return "", err
	}

	supportingFiles, err := listSupportingFiles(skillDir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[IMPORTANT: The user has invoked the %q skill. Follow the skill instructions below.]\n\n", slug)
	b.WriteString(skillContent)
	if !strings.HasSuffix(skillContent, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n[Skill directory: %s]\n", skillRel)
	b.WriteString("Resolve relative paths in this skill against that directory in the agent workspace.")
	if runtimeDir := strings.TrimSpace(opts.RuntimeSkillDir); runtimeDir != "" {
		fmt.Fprintf(&b, "\n[Runtime skill directory: %s]", runtimeDir)
	}
	b.WriteString("\n")

	if len(supportingFiles) > 0 {
		b.WriteString("\n[This skill has supporting files:]\n")
		for _, file := range supportingFiles {
			fmt.Fprintf(&b, "- %s\n", file)
		}
	}

	instruction := strings.TrimSpace(opts.Instruction)
	if instruction == "" {
		instruction = "(no additional instruction)"
	}
	fmt.Fprintf(&b, "\nThe user has provided the following instruction alongside the skill invocation: %s", instruction)
	return b.String(), nil
}

func splitSlashCommand(rest string) (string, string) {
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	for idx, r := range rest {
		if unicode.IsSpace(r) {
			return rest[:idx], rest[idx:]
		}
	}
	return rest, ""
}

func validSlug(slug string) bool {
	slug = strings.TrimSpace(slug)
	return slug != "" && !strings.Contains(slug, "/") && slugPattern.MatchString(slug)
}

func ensureDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill directory is a symlink")
	}
	if !info.IsDir() {
		return fmt.Errorf("skill path is not a directory")
	}
	return nil
}

func readSkillFile(filePath string) (string, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("SKILL.md is a symlink")
	}
	if info.IsDir() {
		return "", fmt.Errorf("SKILL.md is a directory")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxSkillContentBytes+1))
	if err != nil {
		return "", fmt.Errorf("read SKILL.md: %w", err)
	}
	if len(data) > maxSkillContentBytes {
		return "", fmt.Errorf("SKILL.md exceeds %d bytes", maxSkillContentBytes)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("SKILL.md must be utf-8 text")
	}
	return string(data), nil
}

func listSupportingFiles(skillDir string) ([]string, error) {
	var files []string
	stop := errors.New("supporting file limit reached")
	err := filepath.WalkDir(skillDir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == skillDir {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skillDir, current)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "SKILL.md" {
			return nil
		}
		files = append(files, rel)
		if len(files) >= maxSupportingFiles {
			return stop
		}
		return nil
	})
	if errors.Is(err, stop) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
