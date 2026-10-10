// Package architecture contains the local, read-only project inventory used
// by the stage 1 architecture workflow.  It intentionally has no provider or
// Agent integration: a scan only reads files and local Git metadata.
package architecture

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/pelletier/go-toml/v2"
)

type ScanConfig struct {
	ProjectID       string
	RootPath        string
	ExcludePatterns []string
}

type ignoreRule struct {
	pattern string
	negate  bool
	base    string
}

var (
	secretNamePattern = regexp.MustCompile(`(?i)^(?:\.env(?:\..*)?|credentials(?:\..*)?|secrets?(?:\..*)?|.*\.(?:pem|key|p12|pfx|jks|keystore|secret))$`)
	binaryExtension   = regexp.MustCompile(`(?i)\.(?:exe|dll|so|dylib|bin|class|jar|pyc|pdb|obj|o|a|lib|wasm|zip|7z|rar|gz|tgz|tar|iso|png|jpe?g|gif|webp|ico|bmp|pdf|mp3|mp4|mov|avi|woff2?|ttf|eot)$`)
	languageByExt     = map[string]string{
		".ts": "typescript", ".tsx": "typescript", ".mts": "typescript", ".cts": "typescript",
		".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
		".go": "go", ".py": "python", ".pyi": "python", ".cs": "csharp",
	}
	manifestNames = map[string]bool{
		"package.json": true, "go.mod": true, "pyproject.toml": true, "setup.py": true,
		"requirements.txt": true, "requirements-dev.txt": true,
		"dockerfile": true, "docker-compose.yml": true, "docker-compose.yaml": true,
	}
	defaultIgnorePatterns     = []string{".git", ".git/", "node_modules/", "vendor/", ".venv/", "venv/", "__pycache__/", ".run-data/", "dist/", "build/", "out/", "coverage/", "release/", "bin/", "obj/", "*.pyc"}
	defaultIgnoredDirectories = map[string]bool{
		".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
		"__pycache__": true, ".run-data": true, "dist": true, "build": true, "out": true,
		"coverage": true, "release": true, "bin": true, "obj": true,
	}
	pythonRequirementPattern  = regexp.MustCompile(`(?i)^([a-z0-9][a-z0-9._-]*)(?:\[[^\]]+\])?\s*((?:===|==|!=|~=|>=|<=|>|<)\s*[^;\s]+)?\s*(?:;.*)?$`)
	pythonQuotedStringPattern = regexp.MustCompile(`(?s)["']([^"']+)["']`)
	pythonInstallListPattern  = regexp.MustCompile(`(?is)(?:install_requires|extras_require)\s*=\s*\[([^\]]*)\]`)
)

// Scan builds a deterministic snapshot.  Paths are slash-normalized and
// sorted, so repeated scans with no filesystem changes produce the same hash.
func Scan(ctx context.Context, cfg ScanConfig) (model.ArchitectureSnapshot, error) {
	root, err := filepath.Abs(filepath.Clean(strings.TrimSpace(cfg.RootPath)))
	if err != nil {
		return model.ArchitectureSnapshot{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return model.ArchitectureSnapshot{}, err
	}
	if !info.IsDir() {
		return model.ArchitectureSnapshot{}, fmt.Errorf("project root must be a directory")
	}

	rules := loadIgnoreRules(root, cfg.ExcludePatterns)
	gitRevision, dirtyPaths := gitMetadata(ctx, root)
	gitStatus := gitStatusMap(ctx, root)
	files := make([]model.ArchitectureFile, 0, 128)
	coverage := model.ArchitectureCoverage{ByLanguage: map[string]int{}, IgnoredReasons: map[string]int{}}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Permission-denied children are recorded in coverage by the caller's
			// logs but do not cause a valid partial scan to include unsafe data.
			if errors.Is(walkErr, os.ErrPermission) {
				coverage.IgnoredFiles++
				coverage.IgnoredReasons["permission"]++
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		// Never follow links.  This also prevents a symlink to a parent or an
		// unrelated checkout from crossing the project boundary.
		if entry.Type()&os.ModeSymlink != 0 {
			coverage.OutsideLinks++
			coverage.IgnoredFiles++
			coverage.IgnoredReasons["symlink"]++
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ignored, reason := shouldIgnore(rules, rel, entry.IsDir()); ignored {
			coverage.IgnoredFiles++
			coverage.IgnoredReasons[reason]++
			if entry.IsDir() {
				if hasNegationUnder(rules, rel) {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		coverage.TotalFiles++
		if !entry.Type().IsRegular() {
			coverage.BinaryFiles++
			coverage.IgnoredFiles++
			coverage.IgnoredReasons["non_regular"]++
			return nil
		}
		if secretNamePattern.MatchString(filepath.Base(rel)) {
			coverage.SecretFiles++
			coverage.IgnoredFiles++
			coverage.IgnoredReasons["secret"]++
			return nil
		}
		if binaryExtension.MatchString(filepath.Base(rel)) {
			coverage.BinaryFiles++
			coverage.IgnoredFiles++
			coverage.IgnoredReasons["binary"]++
			return nil
		}
		item, include, itemErr := inventoryFile(path, rel, gitStatus[rel])
		if itemErr != nil {
			return itemErr
		}
		if !include {
			coverage.BinaryFiles++
			coverage.IgnoredFiles++
			coverage.IgnoredReasons["binary"]++
			return nil
		}
		files = append(files, item)
		coverage.IncludedFiles++
		language := item.Language
		if language == "" {
			language = "unknown"
			coverage.UnknownFiles++
		}
		coverage.ByLanguage[language]++
		return nil
	})
	if err != nil {
		return model.ArchitectureSnapshot{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	allowedPaths := make(map[string]bool, len(files))
	for _, file := range files {
		allowedPaths[file.Path] = true
	}
	filteredDirty := dirtyPaths[:0]
	for _, path := range dirtyPaths {
		if allowedPaths[path] {
			filteredDirty = append(filteredDirty, path)
		}
	}
	dirtyPaths = filteredDirty
	modules := detectModules(root, files)
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].RootPath == modules[j].RootPath {
			if modules[i].Language == modules[j].Language {
				if modules[i].Name == modules[j].Name {
					left := strings.Join(modules[i].DependencyFiles, "\x00") + "\x00" + strings.Join(modules[i].EvidencePaths, "\x00")
					right := strings.Join(modules[j].DependencyFiles, "\x00") + "\x00" + strings.Join(modules[j].EvidencePaths, "\x00")
					return left < right
				}
				return modules[i].Name < modules[j].Name
			}
			return modules[i].Language < modules[j].Language
		}
		return modules[i].RootPath < modules[j].RootPath
	})
	for _, module := range modules {
		if module.Unknown {
			coverage.UnknownFiles++
		}
	}
	coverage.UnknownFiles = 0
	for _, file := range files {
		if file.Language == "" {
			coverage.UnknownFiles++
		}
	}
	digest := snapshotHash(files, modules, gitRevision, dirtyPaths)
	return model.ArchitectureSnapshot{
		ProjectID:       cfg.ProjectID,
		Status:          "complete",
		ContentHash:     digest,
		GitRevision:     gitRevision,
		GitDirty:        len(dirtyPaths) > 0,
		GitChangedPaths: dirtyPaths,
		FileCount:       len(files),
		ModuleCount:     len(modules),
		UnknownCount:    coverage.UnknownFiles,
		Coverage:        coverage,
		Files:           files,
		Modules:         modules,
	}, nil
}

func hasNegationUnder(rules []ignoreRule, rel string) bool {
	for _, rule := range rules {
		if !rule.negate {
			continue
		}
		if rule.base == rel || strings.HasPrefix(rule.base, rel+"/") {
			return true
		}
		pattern := strings.TrimPrefix(strings.TrimSuffix(rule.pattern, "/"), "./")
		if rule.base != "" {
			if rel != rule.base && !strings.HasPrefix(rel, rule.base+"/") {
				continue
			}
		}
		if strings.HasPrefix(pattern, rel+"/") || strings.Contains(pattern, "**") {
			return true
		}
	}
	return false
}

func inventoryFile(path, rel, gitStatus string) (model.ArchitectureFile, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return model.ArchitectureFile{}, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return model.ArchitectureFile{}, false, err
	}
	defer file.Close()
	hasher := sha256.New()
	buffer := make([]byte, 32*1024)
	var first []byte
	lineCount := 0
	lastByte := byte(0)
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			if len(first) < 8192 {
				want := 8192 - len(first)
				if want > n {
					want = n
				}
				first = append(first, buffer[:want]...)
			}
			_, _ = hasher.Write(buffer[:n])
			for _, value := range buffer[:n] {
				if value == '\n' {
					lineCount++
				}
				lastByte = value
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return model.ArchitectureFile{}, false, readErr
			}
			break
		}
	}
	if len(first) > 0 && lastByte != '\n' {
		lineCount++
	}
	if bytesBinary(first) {
		return model.ArchitectureFile{}, false, nil
	}
	language := languageByExt[strings.ToLower(filepath.Ext(rel))]
	base := strings.ToLower(filepath.Base(rel))
	detection := "extension"
	if manifestNames[base] || strings.HasSuffix(base, ".csproj") || strings.HasPrefix(base, "dockerfile") {
		detection = "manifest"
	}
	return model.ArchitectureFile{
		Path:        rel,
		Language:    language,
		Size:        info.Size(),
		LineStart:   1,
		LineEnd:     lineCount,
		LineCount:   lineCount,
		ContentHash: hex.EncodeToString(hasher.Sum(nil)),
		GitStatus:   gitStatus,
		EntryPoint:  isEntryPoint(rel),
		Detection:   detection,
	}, true, nil
}

func bytesBinary(data []byte) bool {
	for _, value := range data {
		if value == 0 {
			return true
		}
	}
	return false
}

func isEntryPoint(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	return base == "main.go" || base == "main.py" || base == "program.cs" ||
		base == "index.ts" || base == "index.tsx" || base == "index.js" || base == "main.ts" || base == "main.js"
}

func snapshotHash(files []model.ArchitectureFile, modules []model.ArchitectureModule, revision string, dirty []string) string {
	type identity struct {
		Path, Language, Hash string
		Size, Lines          int64
	}
	ids := make([]identity, 0, len(files))
	for _, file := range files {
		ids = append(ids, identity{file.Path, file.Language, file.ContentHash, file.Size, int64(file.LineCount)})
	}
	payload, _ := json.Marshal(struct {
		Files []identity
		Mods  []model.ArchitectureModule
		Git   string
		Dirty []string
	}{ids, modules, revision, dirty})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func loadIgnoreRules(root string, excludes []string) []ignoreRule {
	rules := []ignoreRule{}
	// Git applies nested .gitignore rules relative to their containing
	// directory. Load rule files from ordinary source directories before
	// scanning, while treating the default dependency/build directories as
	// permanently excluded. Walking those trees just to find nested rules is
	// both wasteful and contrary to the scan boundary.
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path != root && entry.IsDir() && defaultIgnoredDirectories[strings.ToLower(entry.Name())] {
			return filepath.SkipDir
		}
		if path != root && entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || strings.ToLower(entry.Name()) != ".gitignore" {
			return nil
		}
		base, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return nil
		}
		if base == "." {
			base = ""
		} else {
			base = filepath.ToSlash(base)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(strings.TrimSuffix(scanner.Text(), "\r"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			rule := parseIgnoreRule(line)
			rule.base = base
			rules = append(rules, rule)
		}
		_ = file.Close()
		return nil
	})
	for _, pattern := range excludes {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" {
			rule := parseIgnoreRule(pattern)
			rule.base = ""
			rules = append(rules, rule)
		}
	}
	// These defaults prevent common dependencies, generated outputs, secrets,
	// and package caches from entering a project snapshot even when a project
	// has no .gitignore yet.
	for _, pattern := range defaultIgnorePatterns {
		rule := parseIgnoreRule(pattern)
		rule.base = ""
		rules = append(rules, rule)
	}
	return rules
}

func parseIgnoreRule(pattern string) ignoreRule {
	negate := strings.HasPrefix(pattern, "!")
	if negate {
		pattern = strings.TrimPrefix(pattern, "!")
	}
	return ignoreRule{pattern: strings.TrimPrefix(filepath.ToSlash(pattern), "./"), negate: negate, base: ""}
}

func shouldIgnore(rules []ignoreRule, rel string, isDir bool) (bool, string) {
	ignored := false
	reason := "gitignore"
	for _, rule := range rules {
		candidate := rel
		if rule.base != "" {
			if rel != rule.base && !strings.HasPrefix(rel, rule.base+"/") {
				continue
			}
			candidate = strings.TrimPrefix(strings.TrimPrefix(rel, rule.base), "/")
		}
		pattern := strings.TrimSuffix(rule.pattern, "/")
		if pattern == "" {
			continue
		}
		matched := globMatch(pattern, candidate)
		if !matched && !strings.Contains(pattern, "/") {
			parts := strings.Split(candidate, "/")
			for index, part := range parts {
				if globMatch(pattern, part) {
					matched = true
					if index < len(parts)-1 && isDir {
						break
					}
				}
			}
		}
		if matched {
			ignored = !rule.negate
			if !rule.negate {
				reason = "gitignore"
			}
		}
	}
	return ignored, reason
}

func globMatch(pattern, value string) bool {
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "/")
	value = filepath.ToSlash(value)
	if strings.HasPrefix(pattern, "**/") && globMatch(strings.TrimPrefix(pattern, "**/"), value) {
		return true
	}
	quoted := regexp.QuoteMeta(pattern)
	quoted = strings.ReplaceAll(quoted, `\*\*`, `.*`)
	quoted = strings.ReplaceAll(quoted, `\*`, `[^/]*`)
	quoted = strings.ReplaceAll(quoted, `\?`, `[^/]`)
	re, err := regexp.Compile("^(?:" + quoted + ")$")
	return err == nil && re.MatchString(value)
}

func gitMetadata(ctx context.Context, root string) (string, []string) {
	revision := ""
	if output, err := gitCommand(ctx, root, "rev-parse", "HEAD"); err == nil {
		revision = strings.TrimSpace(string(output))
	}
	paths := gitStatusMap(ctx, root)
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return revision, result
}

func gitStatusMap(ctx context.Context, root string) map[string]string {
	result := map[string]string{}
	// Porcelain paths are relative to the repository, even when -C selects a
	// subdirectory. Limit the pathspec and rebase paths to the scanned root.
	prefixOutput, err := gitCommand(ctx, root, "rev-parse", "--show-prefix")
	if err != nil {
		return result
	}
	prefix := strings.TrimRight(string(prefixOutput), "\r\n")
	output, err := gitCommand(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", ".")
	if err != nil {
		return result
	}
	for path, status := range parseGitStatusEntries(output) {
		if strings.HasPrefix(path, prefix) {
			result[strings.TrimPrefix(path, prefix)] = status
		}
	}
	return result
}

// parseGitStatusEntries consumes porcelain -z records without trimming the
// two status columns, so paths beginning with spaces and non-ASCII paths stay
// intact. Rename records carry the destination first and the old path second.
func parseGitStatusEntries(output []byte) map[string]string {
	result := map[string]string{}
	records := bytes.Split(output, []byte{0})
	for index := 0; index < len(records); index++ {
		record := string(records[index])
		if len(record) < 4 {
			continue
		}
		status := record[:2]
		path := record[3:]
		if path == "" {
			continue
		}
		result[filepath.ToSlash(path)] = status
		if strings.Contains(status, "R") || strings.Contains(status, "C") {
			// The following NUL record is the old path. It is useful for audit,
			// but only the destination belongs to the current snapshot identity.
			index++
		}
	}
	return result
}

func gitCommand(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	return command.Output()
}

func detectModules(root string, files []model.ArchitectureFile) []model.ArchitectureModule {
	byPath := map[string]model.ArchitectureFile{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	modules := []model.ArchitectureModule{}
	for rel, file := range byPath {
		base := strings.ToLower(filepath.Base(rel))
		if !manifestNames[base] && !strings.HasSuffix(base, ".csproj") {
			continue
		}
		module := model.ArchitectureModule{
			Name: filepath.Base(filepath.Dir(rel)), Language: file.Language, RootPath: filepath.ToSlash(filepath.Dir(rel)),
			Dependencies: []string{}, DependencyVersions: map[string]string{}, EntryPoints: []string{}, DependencyFiles: []string{rel}, ContainerFiles: []string{}, EvidencePaths: []string{rel},
		}
		if module.Name == "." || module.Name == "" {
			module.Name = filepath.Base(root)
		}
		if base == "package.json" {
			module.Language = "javascript"
			if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				var value struct {
					Name             string            `json:"name"`
					Version          string            `json:"version"`
					Main             string            `json:"main"`
					Module           string            `json:"module"`
					Dependencies     map[string]string `json:"dependencies"`
					DevDependencies  map[string]string `json:"devDependencies"`
					PeerDependencies map[string]string `json:"peerDependencies"`
				}
				if json.Unmarshal(data, &value) == nil {
					if value.Name != "" {
						module.Name = value.Name
					}
					module.Version = value.Version
					if value.Main != "" {
						module.EntryPoints = append(module.EntryPoints, filepath.ToSlash(filepath.Join(filepath.Dir(rel), value.Main)))
					}
					if value.Module != "" {
						module.EntryPoints = append(module.EntryPoints, filepath.ToSlash(filepath.Join(filepath.Dir(rel), value.Module)))
					}
					for name, version := range value.Dependencies {
						module.DependencyVersions[name] = version
					}
					for name, version := range value.DevDependencies {
						if _, ok := module.DependencyVersions[name]; !ok {
							module.DependencyVersions[name] = version
						}
					}
					for name, version := range value.PeerDependencies {
						if _, ok := module.DependencyVersions[name]; !ok {
							module.DependencyVersions[name] = version
						}
					}
				}
			}
		} else if base == "go.mod" {
			module.Language = "go"
			module.Name = filepath.Base(filepath.Dir(rel))
			if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				module.Version, module.DependencyVersions = parseGoManifest(data)
			}
		} else if base == "pyproject.toml" || strings.HasPrefix(base, "requirements") || base == "setup.py" {
			module.Language = "python"
			if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				switch {
				case base == "pyproject.toml":
					var manifest struct {
						Project struct {
							Name                 string              `toml:"name"`
							Version              string              `toml:"version"`
							Dependencies         []string            `toml:"dependencies"`
							OptionalDependencies map[string][]string `toml:"optional-dependencies"`
						} `toml:"project"`
					}
					if parseErr := toml.Unmarshal(data, &manifest); parseErr == nil {
						if manifest.Project.Name != "" {
							module.Name = manifest.Project.Name
						}
						module.Version = manifest.Project.Version
						for _, requirement := range manifest.Project.Dependencies {
							addPythonDependency(module.DependencyVersions, requirement)
						}
						for _, requirements := range manifest.Project.OptionalDependencies {
							for _, requirement := range requirements {
								addPythonDependency(module.DependencyVersions, requirement)
							}
						}
					}
				case strings.HasPrefix(base, "requirements"):
					for _, line := range strings.Split(string(data), "\n") {
						addPythonDependency(module.DependencyVersions, line)
					}
				case base == "setup.py":
					for _, list := range pythonInstallListPattern.FindAllStringSubmatch(string(data), -1) {
						for _, quoted := range pythonQuotedStringPattern.FindAllStringSubmatch(list[1], -1) {
							addPythonDependency(module.DependencyVersions, quoted[1])
						}
					}
				}
			}
		} else if strings.HasSuffix(base, ".csproj") {
			module.Language = "csharp"
			if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				packageRef := regexp.MustCompile(`(?i)<PackageReference\s+Include="([^"]+)"(?:\s+Version="([^"]+)")?`)
				for _, match := range packageRef.FindAllStringSubmatch(string(data), -1) {
					if len(match) > 1 {
						module.DependencyVersions[match[1]] = match[2]
					}
				}
			}
		}
		for name := range module.DependencyVersions {
			module.Dependencies = append(module.Dependencies, name)
		}
		sort.Strings(module.Dependencies)
		for path, candidate := range byPath {
			if filepath.ToSlash(filepath.Dir(path)) == module.RootPath && candidate.EntryPoint {
				module.EntryPoints = append(module.EntryPoints, path)
			}
			containerPath := filepath.ToSlash(filepath.Dir(path))
			withinModule := containerPath == module.RootPath || (module.RootPath != "." && strings.HasPrefix(containerPath, module.RootPath+"/"))
			if withinModule && (strings.HasPrefix(strings.ToLower(filepath.Base(path)), "dockerfile") || strings.HasPrefix(strings.ToLower(filepath.Base(path)), "docker-compose")) {
				module.ContainerFiles = append(module.ContainerFiles, path)
			}
		}
		sort.Strings(module.EntryPoints)
		sort.Strings(module.ContainerFiles)
		modules = append(modules, module)
	}
	if len(modules) == 0 {
		unknown := []string{}
		for _, file := range files {
			if file.Language == "" {
				unknown = append(unknown, file.Path)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			modules = append(modules, model.ArchitectureModule{Name: filepath.Base(root), RootPath: ".", Unknown: true, EvidencePaths: unknown})
		}
	}
	return modules
}

func addPythonDependency(dependencies map[string]string, value string) {
	value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "[") {
		return
	}
	match := pythonRequirementPattern.FindStringSubmatch(value)
	if len(match) != 3 {
		return
	}
	name := strings.TrimSpace(match[1])
	if name == "" {
		return
	}
	dependencies[name] = strings.TrimSpace(match[2])
}

func parseGoManifest(data []byte) (string, map[string]string) {
	version := ""
	dependencies := map[string]string{}
	inRequireBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
		if line == "" {
			continue
		}
		if inRequireBlock {
			if line == ")" {
				inRequireBlock = false
				continue
			}
			addGoRequirement(dependencies, strings.Fields(line))
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "go" {
			version = fields[1]
			continue
		}
		if len(fields) >= 2 && fields[0] == "require" {
			if fields[len(fields)-1] == "(" {
				inRequireBlock = true
				if len(fields) > 2 {
					addGoRequirement(dependencies, fields[1:len(fields)-1])
				}
				continue
			}
			addGoRequirement(dependencies, fields[1:])
		}
	}
	return version, dependencies
}

func addGoRequirement(dependencies map[string]string, fields []string) {
	if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
		return
	}
	dependencies[fields[0]] = fields[1]
}

// ParseLineCount is a small helper used by Worker parity tests.
func ParseLineCount(value string) int {
	if value == "" {
		return 0
	}
	count := strings.Count(value, "\n")
	if !strings.HasSuffix(value, "\n") {
		count++
	}
	return count
}
