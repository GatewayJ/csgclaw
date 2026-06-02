package slashcommand

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ElementName = "slash-command"

var commandNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var skillSlugPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

const (
	UseSkillCommandName  = "use-skill"
	maxSkillContentBytes = 256 * 1024
)

type Command struct {
	Name string
	Arg  string
	Body string
}

func Parse(content string) (Command, bool, error) {
	text := strings.TrimSpace(content)
	if !looksLikeSlashCommand(text) {
		return Command{}, false, nil
	}

	decoder := xml.NewDecoder(strings.NewReader(text))
	token, err := decoder.Token()
	if err != nil {
		return Command{}, false, fmt.Errorf("parse slash command: %w", err)
	}
	start, ok := token.(xml.StartElement)
	if !ok || start.Name.Space != "" || start.Name.Local != ElementName {
		return Command{}, false, fmt.Errorf("expected <%s> root", ElementName)
	}

	cmd, err := commandFromStart(start)
	if err != nil {
		return Command{}, false, err
	}

	var elementBody strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return Command{}, false, fmt.Errorf("parse slash command body: %w", err)
		}
		switch t := token.(type) {
		case xml.CharData:
			elementBody.Write([]byte(t))
		case xml.EndElement:
			if t.Name.Space != "" || t.Name.Local != ElementName {
				return Command{}, false, fmt.Errorf("unexpected closing element </%s>", t.Name.Local)
			}
			if strings.TrimSpace(elementBody.String()) != "" {
				return Command{}, false, fmt.Errorf("slash command element must be empty; put the user prompt after </%s>", ElementName)
			}
			end := int(decoder.InputOffset())
			if end < 0 || end > len(text) {
				return Command{}, false, fmt.Errorf("slash command offset out of range")
			}
			cmd.Body = strings.TrimSpace(text[end:])
			if err := validate(cmd); err != nil {
				return Command{}, false, err
			}
			return cmd, true, nil
		case xml.StartElement:
			return Command{}, false, fmt.Errorf("slash command element must be empty")
		case xml.Comment:
			return Command{}, false, fmt.Errorf("slash command element must be empty")
		case xml.ProcInst, xml.Directive:
			return Command{}, false, fmt.Errorf("slash command contains unsupported XML token")
		default:
			return Command{}, false, fmt.Errorf("slash command contains unsupported XML token")
		}
	}
}

func Normalize(content string) (string, bool, error) {
	cmd, ok, err := Parse(content)
	if err != nil || !ok {
		return "", ok, err
	}
	rendered, err := Render(cmd)
	if err != nil {
		return "", false, err
	}
	return rendered, true, nil
}

func ParseFeishuShorthand(content string) (Command, bool, error) {
	text := strings.TrimSpace(content)
	if !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		return Command{}, false, nil
	}
	slug, body := splitSlashCommand(strings.TrimPrefix(text, "/"))
	if !validSkillSlug(slug) {
		return Command{}, false, fmt.Errorf("invalid skill slug %q", slug)
	}
	return Command{
		Name: UseSkillCommandName,
		Arg:  slug,
		Body: strings.TrimSpace(body),
	}, true, nil
}

func NormalizeFeishuInput(content string) (string, bool, error) {
	cmd, ok, err := ParseFeishuShorthand(content)
	if err != nil || !ok {
		return "", ok, err
	}
	rendered, err := Render(cmd)
	if err != nil {
		return "", false, err
	}
	return rendered, true, nil
}

func RenderFeishuFallback(content string) string {
	cmd, ok, err := Parse(content)
	if err != nil || !ok || strings.TrimSpace(cmd.Name) != UseSkillCommandName || !validSkillSlug(cmd.Arg) {
		return content
	}
	if body := strings.TrimSpace(cmd.Body); body != "" {
		return "/" + strings.TrimSpace(cmd.Arg) + " " + body
	}
	return "/" + strings.TrimSpace(cmd.Arg)
}

func ExpandUseSkillPrompt(content, workspaceRoot string) (string, bool, error) {
	cmd, ok, err := Parse(content)
	if err != nil {
		return "", true, err
	}
	if !ok {
		return "", false, nil
	}
	if strings.TrimSpace(cmd.Name) != UseSkillCommandName {
		return "", false, nil
	}
	prompt, err := BuildUseSkillPrompt(BuildUseSkillPromptOptions{
		WorkspaceRoot: strings.TrimSpace(workspaceRoot),
		Slug:          strings.TrimSpace(cmd.Arg),
		Instruction:   strings.TrimSpace(cmd.Body),
	})
	if err != nil {
		return "", true, err
	}
	return prompt, true, nil
}

type BuildUseSkillPromptOptions struct {
	WorkspaceRoot string
	Slug          string
	Instruction   string
}

func BuildUseSkillPrompt(opts BuildUseSkillPromptOptions) (string, error) {
	root := strings.TrimSpace(opts.WorkspaceRoot)
	if root == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	slug := strings.TrimSpace(opts.Slug)
	if !validSkillSlug(slug) {
		return "", fmt.Errorf("invalid skill slug %q", opts.Slug)
	}
	skillRel := path.Join("skills", slug)
	skillFile := filepath.Join(root, filepath.FromSlash(skillRel), "SKILL.md")
	skillContent, err := readSkillFile(skillFile)
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
	b.WriteString("Resolve relative paths in this skill against that directory in the agent workspace.\n")

	instruction := strings.TrimSpace(opts.Instruction)
	if instruction == "" {
		instruction = "(no additional instruction)"
	}
	fmt.Fprintf(&b, "\nThe user has provided the following instruction alongside the skill invocation: %s", instruction)
	return b.String(), nil
}

func Render(cmd Command) (string, error) {
	cmd.Name = strings.TrimSpace(cmd.Name)
	cmd.Arg = strings.TrimSpace(cmd.Arg)
	cmd.Body = strings.TrimSpace(cmd.Body)
	if err := validate(cmd); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("<")
	b.WriteString(ElementName)
	b.WriteString(` name="`)
	b.WriteString(escapeXML(cmd.Name))
	b.WriteString(`"`)
	if cmd.Arg != "" {
		b.WriteString(` arg="`)
		b.WriteString(escapeXML(cmd.Arg))
		b.WriteString(`"`)
	}
	b.WriteString("></")
	b.WriteString(ElementName)
	b.WriteString(">")
	if cmd.Body != "" {
		b.WriteString(" ")
		b.WriteString(cmd.Body)
	}
	return b.String(), nil
}

func looksLikeSlashCommand(text string) bool {
	if !strings.HasPrefix(text, "<"+ElementName) {
		return false
	}
	if len(text) == len("<"+ElementName) {
		return true
	}
	next := text[len("<"+ElementName)]
	return next == ' ' || next == '	' || next == '\n' || next == '\r' || next == '>' || next == '/'
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

func commandFromStart(start xml.StartElement) (Command, error) {
	cmd := Command{}
	seen := map[string]struct{}{}
	for _, attr := range start.Attr {
		if attr.Name.Space != "" {
			return Command{}, fmt.Errorf("slash command attributes must not use namespaces")
		}
		name := attr.Name.Local
		if _, ok := seen[name]; ok {
			return Command{}, fmt.Errorf("duplicate slash command attribute %q", name)
		}
		seen[name] = struct{}{}
		switch name {
		case "name":
			cmd.Name = strings.TrimSpace(attr.Value)
		case "arg":
			cmd.Arg = strings.TrimSpace(attr.Value)
		default:
			return Command{}, fmt.Errorf("unsupported slash command attribute %q", name)
		}
	}
	return cmd, nil
}

func validate(cmd Command) error {
	name := strings.TrimSpace(cmd.Name)
	if !commandNamePattern.MatchString(name) {
		return fmt.Errorf("invalid slash command name %q", cmd.Name)
	}
	arg := strings.TrimSpace(cmd.Arg)
	if len(arg) > 256 {
		return fmt.Errorf("slash command arg exceeds 256 bytes")
	}
	if strings.ContainsAny(arg, "\r\n\t") {
		return fmt.Errorf("slash command arg must be a single token")
	}
	return nil
}

func validSkillSlug(slug string) bool {
	slug = strings.TrimSpace(slug)
	return slug != "" && slug != "." && slug != ".." && !strings.ContainsAny(slug, `/\`) && skillSlugPattern.MatchString(slug)
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

func escapeXML(value string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(value))
	return buf.String()
}
