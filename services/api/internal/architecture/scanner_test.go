package architecture

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func writeFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestScanInventoryAndSafetyBoundaries(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".gitignore", "ignored/\n!ignored/keep.ts\n*.generated.ts\n")
	writeFixture(t, root, "nested/.gitignore", "nested-secret.ts\n!keep.ts\n")
	writeFixture(t, root, "src/main.ts", "export const main = true\n")
	writeFixture(t, root, "src/client.js", "module.exports = {}\n")
	writeFixture(t, root, "cmd/app/main.go", "package main\nfunc main() {}\n")
	writeFixture(t, root, "python/main.py", "print('ok')\n")
	writeFixture(t, root, "dotnet/Program.cs", "using System; class Program { static void Main() {} }\n")
	writeFixture(t, root, "package.json", `{"name":"fixture","version":"1.2.3","main":"src/main.ts","dependencies":{"gin":"1.0.0"}}`)
	writeFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.23\n\nrequire (\n\texample.test/lib v0.2.0\n\texample.test/other v1.4.0 // indirect\n)\n")
	writeFixture(t, root, "pyproject.toml", "[project]\nname='fixture'\ndependencies=['requests>=2.0']\n")
	writeFixture(t, root, "requirements-dev.txt", "pytest>=8.0\n-r requirements.txt\n")
	writeFixture(t, root, "setup.py", "from setuptools import setup\nsetup(name='fixture', version='1.2.3', install_requires=['flask>=3.0'])\n")
	writeFixture(t, root, "dotnet/app.csproj", `<Project><ItemGroup><PackageReference Include="Newtonsoft.Json" Version="13.0.3" /></ItemGroup></Project>`)
	writeFixture(t, root, "Dockerfile", "FROM scratch\n")
	writeFixture(t, root, "services/one/package.json", `{"name":"one","dependencies":{"one-lib":"1.0.0"}}`)
	writeFixture(t, root, "services/one/Dockerfile", "FROM scratch\n")
	writeFixture(t, root, "services/one/deploy/docker-compose.yml", "services:\n  one:\n    image: one\n")
	writeFixture(t, root, "services/two/package.json", `{"name":"two","dependencies":{"two-lib":"2.0.0"}}`)
	writeFixture(t, root, "services/two/docker-compose.yml", "services:\n  two:\n    image: two\n")
	writeFixture(t, root, ".env", "TOKEN=do-not-index\n")
	writeFixture(t, root, "ignored/no.ts", "ignored\n")
	writeFixture(t, root, "ignored/keep.ts", "kept by negation\n")
	writeFixture(t, root, "nested/nested-secret.ts", "ignored\n")
	writeFixture(t, root, "nested/keep.ts", "kept nested\n")
	writeFixture(t, root, "a.generated.ts", "ignored by project rule\n")
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "dep"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "node_modules/dep/index.js", "dependency\n")
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0, 1, 2}, 0o640); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeFixture(t, outside, "outside.ts", "outside\n")
	if err := os.Symlink(filepath.Join(outside, "outside.ts"), filepath.Join(root, "linked.ts")); err != nil {
		t.Logf("symlink unavailable: %v", err)
	}

	first, err := Scan(context.Background(), ScanConfig{ProjectID: "project", RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, file := range first.Files {
		paths[file.Path] = true
	}
	for _, expected := range []string{"src/main.ts", "src/client.js", "cmd/app/main.go", "python/main.py", "dotnet/Program.cs", "ignored/keep.ts", "nested/keep.ts"} {
		if !paths[expected] {
			t.Fatalf("expected included path %q in %#v", expected, paths)
		}
	}
	for _, excluded := range []string{".env", "ignored/no.ts", "nested/nested-secret.ts", "a.generated.ts", "node_modules/dep/index.js", "binary.bin", "linked.ts"} {
		if paths[excluded] {
			t.Fatalf("unexpected excluded path %q", excluded)
		}
	}
	if first.Coverage.SecretFiles == 0 || first.Coverage.BinaryFiles == 0 {
		t.Fatalf("safety coverage was not recorded: %+v", first.Coverage)
	}
	if first.Coverage.ByLanguage["typescript"] == 0 || first.Coverage.ByLanguage["javascript"] == 0 || first.Coverage.ByLanguage["go"] == 0 || first.Coverage.ByLanguage["python"] == 0 || first.Coverage.ByLanguage["csharp"] == 0 {
		t.Fatalf("language coverage: %+v", first.Coverage.ByLanguage)
	}
	if len(first.Modules) < 6 {
		t.Fatalf("expected manifest modules, got %+v", first.Modules)
	}
	var goModule, pythonModule, setupModule model.ArchitectureModule
	for _, module := range first.Modules {
		if contains(module.DependencyFiles, "go.mod") {
			goModule = module
		}
		if contains(module.DependencyFiles, "pyproject.toml") {
			pythonModule = module
		}
		if contains(module.DependencyFiles, "setup.py") {
			setupModule = module
		}
	}
	if goModule.DependencyVersions["example.test/lib"] != "v0.2.0" || goModule.DependencyVersions["example.test/other"] != "v1.4.0" {
		t.Fatalf("go dependency inventory: %+v", goModule.DependencyVersions)
	}
	if pythonModule.DependencyVersions["requests"] != ">=2.0" {
		t.Fatalf("pyproject dependency inventory: %+v", pythonModule.DependencyVersions)
	}
	if _, ok := pythonModule.DependencyVersions["dependencies"]; ok {
		t.Fatalf("pyproject key was treated as dependency: %+v", pythonModule.DependencyVersions)
	}
	if setupModule.DependencyVersions["flask"] != ">=3.0" {
		t.Fatalf("setup.py dependency inventory: %+v", setupModule.DependencyVersions)
	}
	if _, ok := setupModule.DependencyVersions["name"]; ok {
		t.Fatalf("setup.py name was treated as dependency: %+v", setupModule.DependencyVersions)
	}
	if _, ok := setupModule.DependencyVersions["version"]; ok {
		t.Fatalf("setup.py version was treated as dependency: %+v", setupModule.DependencyVersions)
	}
	var oneModule, twoModule model.ArchitectureModule
	for _, module := range first.Modules {
		if contains(module.DependencyFiles, "services/one/package.json") {
			oneModule = module
		}
		if contains(module.DependencyFiles, "services/two/package.json") {
			twoModule = module
		}
		if module.RootPath == "." && (!contains(module.ContainerFiles, "Dockerfile") || contains(module.ContainerFiles, "services/one/Dockerfile") || contains(module.ContainerFiles, "services/two/docker-compose.yml")) {
			t.Fatalf("root module container scope: %+v", module)
		}
	}
	if !contains(oneModule.ContainerFiles, "services/one/Dockerfile") || !contains(oneModule.ContainerFiles, "services/one/deploy/docker-compose.yml") || contains(oneModule.ContainerFiles, "services/two/docker-compose.yml") {
		t.Fatalf("module one container scope: %+v", oneModule)
	}
	if !contains(twoModule.ContainerFiles, "services/two/docker-compose.yml") || contains(twoModule.ContainerFiles, "services/one/Dockerfile") {
		t.Fatalf("module two container scope: %+v", twoModule)
	}
	moduleJSON, _ := json.Marshal(first.Modules)
	if !strings.Contains(string(moduleJSON), "Newtonsoft.Json") || !strings.Contains(string(moduleJSON), "example.test/lib") || !strings.Contains(string(moduleJSON), "requests") {
		t.Fatalf("dependency inventory: %s", moduleJSON)
	}
	second, err := Scan(context.Background(), ScanConfig{ProjectID: "project", RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentHash != second.ContentHash {
		t.Fatalf("stable scan hash changed: %s != %s", first.ContentHash, second.ContentHash)
	}
	writeFixture(t, root, ".env", "TOKEN=changed-secret\n")
	secretOnly, err := Scan(context.Background(), ScanConfig{ProjectID: "project", RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	if secretOnly.ContentHash != first.ContentHash {
		t.Fatal("excluded secret changed snapshot hash")
	}
	writeFixture(t, root, "src/main.ts", "export const main = false\n")
	changed, err := Scan(context.Background(), ScanConfig{ProjectID: "project", RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	if changed.ContentHash == first.ContentHash {
		t.Fatal("included content change was not detected")
	}
	if len(changed.GitChangedPaths) > 0 && !contains(changed.GitChangedPaths, "src/main.ts") {
		t.Fatalf("git changed paths: %v", changed.GitChangedPaths)
	}
	if err := os.Remove(filepath.Join(root, "src/client.js")); err != nil {
		t.Fatal(err)
	}
	deleted, err := Scan(context.Background(), ScanConfig{ProjectID: "project", RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.ContentHash == changed.ContentHash || containsFile(deleted.Files, "src/client.js") {
		t.Fatal("deletion was not represented")
	}
}

func TestIgnoreRulePreloadSkipsDefaultTrees(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/.gitignore", "generated.ts\n")
	for index := 0; index < 64; index++ {
		path := filepath.Join("node_modules", "deep", strings.Repeat("x", index+1))
		writeFixture(t, root, filepath.ToSlash(filepath.Join(path, ".gitignore")), "!keep.ts\n")
	}
	rules := loadIgnoreRules(root, nil)
	for _, rule := range rules {
		if strings.HasPrefix(rule.base, "node_modules/") || rule.base == "node_modules" {
			t.Fatalf("default excluded tree was preloaded: %+v", rule)
		}
	}
	if !containsRuleBase(rules, "src") {
		t.Fatalf("normal nested .gitignore was not preloaded: %+v", rules)
	}
}

func containsRuleBase(rules []ignoreRule, base string) bool {
	for _, rule := range rules {
		if rule.base == base {
			return true
		}
	}
	return false
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
func containsFile(values []model.ArchitectureFile, expected string) bool {
	for _, value := range values {
		if value.Path == expected {
			return true
		}
	}
	return false
}

func TestGitPorcelainStatusKeepsUnicodeAndSpacePaths(t *testing.T) {
	root := t.TempDir()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	writeFixture(t, root, "base.ts", "base\n")
	run := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, output)
		}
	}
	run("init", "-q")
	run("config", "user.email", "scanner@example.test")
	run("config", "user.name", "scanner")
	run("add", ".")
	run("commit", "-qm", "initial")
	writeFixture(t, root, "你好 name.ts", "changed\n")
	writeFixture(t, root, "old.ts", "old\n")
	run("add", "old.ts")
	run("commit", "-qm", "old")
	run("mv", "old.ts", "renamed.ts")
	snapshot, err := Scan(context.Background(), ScanConfig{RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(snapshot.GitChangedPaths, "你好 name.ts") || !contains(snapshot.GitChangedPaths, "renamed.ts") {
		t.Fatalf("porcelain paths were lost: %v", snapshot.GitChangedPaths)
	}
}
