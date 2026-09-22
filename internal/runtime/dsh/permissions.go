package dsh

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	larkextension "csgclaw/internal/runtimeextension/larkcli"
)

func unattendedLarkCLIPermission(proc *process, turn *activeTurn, request permissionRequestParams) (string, bool) {
	if proc == nil || turn == nil {
		return "", false
	}
	toolCallID := strings.TrimSpace(request.ToolCall.ToolCallID)
	proc.mu.Lock()
	active := proc.active[request.SessionID]
	extensionLoaded := proc.extensionDigests[larkextension.Name] != ""
	trustedExecutable := proc.extensionExecutables[larkextension.Name]
	tool, found := turn.tools[toolCallID]
	proc.mu.Unlock()
	if active != turn || !extensionLoaded || trustedExecutable == "" || !found || !strings.EqualFold(strings.TrimSpace(tool.Kind), "exec_command") {
		return "", false
	}
	payload, ok := tool.Payload.(map[string]any)
	if !ok {
		return "", false
	}
	rawInput, ok := payload["rawInput"].(map[string]any)
	if !ok {
		return "", false
	}
	command, _ := rawInput["command"].(string)
	if !larkextension.AllowsUnattendedConfigCommand(command, func(requested string) bool {
		return processExecutableMatches(proc, requested, trustedExecutable)
	}) {
		return "", false
	}
	for _, option := range request.Options {
		if strings.EqualFold(strings.TrimSpace(option.Kind), "allow_once") {
			return option.OptionID, true
		}
	}
	return "", false
}

func processExecutableMatches(proc *process, requested, trusted string) bool {
	requestedPath, err := resolveProcessExecutable(proc, requested)
	if err != nil {
		return false
	}
	requestedInfo, err := os.Stat(requestedPath)
	if err != nil || requestedInfo.IsDir() {
		return false
	}
	trustedInfo, err := os.Stat(filepath.Clean(trusted))
	return err == nil && !trustedInfo.IsDir() && os.SameFile(requestedInfo, trustedInfo)
}

func resolveProcessExecutable(proc *process, requested string) (string, error) {
	if proc == nil {
		return "", os.ErrNotExist
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", os.ErrNotExist
	}
	workspace := strings.TrimSpace(proc.workspace)
	if workspace == "" {
		return "", os.ErrNotExist
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(requested) || containsPathSeparator(requested) {
		candidate := requested
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workspace, candidate)
		}
		return executableCandidate(candidate)
	}
	pathValue := processEnvironmentValue(proc.environment, "PATH")
	if pathValue == "" {
		return "", os.ErrNotExist
	}
	for _, directory := range filepath.SplitList(pathValue) {
		directory = strings.Trim(strings.TrimSpace(directory), `"`)
		if directory == "" {
			directory = workspace
		} else if !filepath.IsAbs(directory) {
			directory = filepath.Join(workspace, directory)
		}
		for _, name := range processExecutableNames(requested, proc.environment) {
			candidate, candidateErr := executableCandidate(filepath.Join(directory, name))
			if candidateErr == nil {
				return candidate, nil
			}
		}
	}
	return "", os.ErrNotExist
}

func executableCandidate(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", os.ErrNotExist
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("file is not executable")
	}
	return path, nil
}

func processExecutableNames(name string, environment []string) []string {
	if runtime.GOOS != "windows" || filepath.Ext(name) != "" {
		return []string{name}
	}
	extensions := processEnvironmentValue(environment, "PATHEXT")
	if extensions == "" {
		extensions = ".COM;.EXE;.BAT;.CMD"
	}
	var names []string
	for _, extension := range strings.Split(extensions, ";") {
		extension = strings.TrimSpace(extension)
		if extension != "" {
			names = append(names, name+extension)
		}
	}
	return names
}

func processEnvironmentValue(environment []string, key string) string {
	value := ""
	for _, entry := range environment {
		name, candidate, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(strings.TrimSpace(name), key) {
			value = candidate
		}
	}
	return value
}

func containsPathSeparator(path string) bool {
	if strings.ContainsRune(path, filepath.Separator) {
		return true
	}
	return runtime.GOOS == "windows" && strings.Contains(path, "/")
}
