package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests replay the Windows packaging commands against a module proxy
// that publishes a new github.com/tc-hib/go-winres release. They fail while
// those commands install and run whatever version @latest (or any other
// floating selector) currently resolves to.

const (
	releaseVersion = "1.2.3"
	sumDBVerifier  = "localhost.localdev/sumdb+00000c67+AcTrnkbUA+TU4heY3hkjiSES/DSQniBqIeQ/YppAUtK6"
	attackerMark   = "attacker-controlled-archive"
	exfilName      = ".release-tool-saw-env"
	uploadArgsName = ".release-tool-upload-args"
)

// placeholderEnv is the stand-in for secrets.ENV_FILE. The names are the ones
// cmd/upload reads. The values are placeholders.
const placeholderEnv = "CDN_SPACE=placeholder-space\nCDN_REGION=placeholder-region\nCDN_KEY=placeholder-cdn-key\nCDN_SECRET=placeholder-cdn-secret\nCDN_BASEURL=https://placeholder.example/cdn\nSPACE_PATH=/placeholder/\n"

func TestProductionWindowsReleaseDoesNotRunPublisherTool(t *testing.T) {
	workflow, err := os.ReadFile(".github/workflows/chauffeur-upload.yml")
	if err != nil {
		t.Fatal(err)
	}
	proxy := startPublisherProxy(t, publisherVersions(string(workflow)))
	if err := publisherToolRuns(t, proxy); err != nil {
		t.Fatalf("publisher release was not executable: %v", err)
	}

	job, wfEnv, err := windowsReleaseJobBytes(workflow)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil {
		return
	}

	root := copyRepo(t)
	gopath := t.TempDir()
	base := releaseEnv(proxy, gopath, wfEnv, false)
	attack := releaseEnv(proxy, gopath, wfEnv, true)
	vars := condVars{event: "push", matrixGOOS: "windows", runnerOS: "Linux"}

	var uploadBefore []byte
	winresRan := false
	for _, step := range job.Steps {
		if strings.TrimSpace(step.Run) == "" || !condTrue(step.If, vars) {
			continue
		}
		script := substituteActions(step.Run)
		env := append([]string{}, base...)
		if scriptInstallsWinres(script) {
			env = append([]string{}, attack...)
		}
		for k, v := range step.Env {
			v = substituteActions(v)
			if strings.Contains(v, "secrets.ENV_FILE") {
				v = placeholderEnv
			}
			env = setEnv(env, k, v)
		}
		if strings.TrimSpace(step.Name) == "Configure build" {
			env = setEnv(env, "ENV_FILE", placeholderEnv)
		}

		out, err := runBash(root, script, env)
		if scriptInstallsWinres(script) {
			winresRan = true
			if releaseEffects(root, uploadBefore) == "" {
				if err != nil && testing.Verbose() {
					t.Logf("windows resource step stopped before the publisher tool ran: %v\n%s", err, out)
				}
				return
			}
			finishRelease(t, root, job.Steps, vars, base, step.Name)
			effects := releaseEffects(root, uploadBefore)
			t.Fatalf("windows packaging ran the publisher's go-winres\n%s\nstep output:\n%s", effects, out)
		}
		if err != nil {
			t.Fatalf("step %q failed before go-winres: %v\n%s", step.Name, err, out)
		}
		if strings.Contains(script, "dist/host/upload") && strings.Contains(script, "go build") {
			uploadBefore, _ = os.ReadFile(filepath.Join(root, "dist/host/upload"))
		}
	}
	if !winresRan {
		if effects := releaseEffects(root, uploadBefore); effects != "" {
			t.Fatal(effects)
		}
	}
}

func TestWindowsHelperScriptsDoNotRunPublisherTool(t *testing.T) {
	sh, err := os.ReadFile("build-windows.sh")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := os.ReadFile("build-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	proxy := startPublisherProxy(t, publisherVersions(string(sh), string(ps)))
	if err := publisherToolRuns(t, proxy); err != nil {
		t.Fatalf("publisher release was not executable: %v", err)
	}
	t.Run("build-windows.sh", func(t *testing.T) {
		runHelperScript(t, proxy, "build-windows.sh", []string{"bash", "build-windows.sh", releaseVersion})
	})
	t.Run("build-windows.ps1", func(t *testing.T) {
		pwsh, err := exec.LookPath("pwsh")
		if err != nil {
			t.Fatal("pwsh is not installed, so build-windows.ps1 was not executed")
		}
		runHelperScript(t, proxy, "build-windows.ps1", []string{pwsh, "-File", "build-windows.ps1", releaseVersion})
	})
}

func runHelperScript(t *testing.T, proxy string, name string, argv []string) {
	t.Helper()
	root := copyRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(placeholderEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	gopath := t.TempDir()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = releaseEnv(proxy, gopath, map[string]string{}, true)
	out, err := cmd.CombinedOutput()
	effects := releaseEffects(root, nil)
	if effects != "" {
		t.Fatalf("%s ran the publisher's go-winres\n%s\noutput:\n%s", name, effects, out)
	}
	if err != nil && testing.Verbose() {
		t.Logf("%s exited: %v\n%s", name, err, out)
	}
}

func finishRelease(t *testing.T, root string, steps []wfStep, vars condVars, base []string, after string) {
	t.Helper()
	seen := false
	for _, step := range steps {
		if !seen {
			if step.Name == after {
				seen = true
			}
			continue
		}
		if strings.TrimSpace(step.Run) == "" || !condTrue(step.If, vars) {
			continue
		}
		script := substituteActions(step.Run)
		if strings.Contains(script, "./dist/host/upload") {
			body, err := os.ReadFile(filepath.Join(root, "dist/host/upload"))
			if err != nil || !bytes.HasPrefix(body, []byte("#!")) {
				return
			}
		}
		env := append([]string{}, base...)
		for k, v := range step.Env {
			env = setEnv(env, k, substituteActions(v))
		}
		out, err := runBash(root, script, env)
		if err != nil {
			t.Logf("later step %q: %v\n%s", step.Name, err, out)
			return
		}
	}
}

func releaseEffects(root string, uploadBefore []byte) string {
	var parts []string
	exfil, err := os.ReadFile(filepath.Join(root, exfilName))
	if err == nil && bytes.Equal(exfil, []byte(placeholderEnv)) {
		parts = append(parts, "the tool read the workspace environment file written from ENV_FILE")
	}
	upload := filepath.Join(root, "dist/host/upload")
	if body, err := os.ReadFile(upload); err == nil && bytes.HasPrefix(body, []byte("#!")) {
		sum := sha256.Sum256(body)
		before := sha256.Sum256(uploadBefore)
		if uploadBefore == nil || sum != before {
			parts = append(parts, "the tool replaced dist/host/upload")
		}
	}
	archive := filepath.Join(root, "artifacts", "chauffeur", "windows-amd64", releaseVersion, "archive", "default")
	if body, err := os.ReadFile(archive); err == nil && string(body) == attackerMark {
		parts = append(parts, "the upload step published attacker bytes for tools/chauffeur/windows-amd64/"+releaseVersion+"/archive/")
	}
	if args, err := os.ReadFile(filepath.Join(root, uploadArgsName)); err == nil {
		text := string(args)
		if strings.Contains(text, "public-read") && strings.Contains(text, "tools/chauffeur/windows-amd64/") {
			parts = append(parts, "those bytes were passed to the public-read upload for the chauffeur windows path")
		}
	}
	return strings.Join(parts, "; ")
}

func publisherToolRuns(t *testing.T, proxy string) error {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(placeholderEnv), 0o644); err != nil {
		return err
	}
	gopath := t.TempDir()
	env := releaseEnv(proxy, gopath, nil, true)
	cmd := exec.Command("go", "install", "github.com/tc-hib/go-winres@latest")
	cmd.Dir = root
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go install @latest: %v\n%s", err, out)
	}
	bin := filepath.Join(gopath, "bin", "go-winres")
	run := exec.Command(bin)
	run.Dir = root
	run.Env = env
	if out, err := run.CombinedOutput(); err != nil {
		return fmt.Errorf("go-winres: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(root, exfilName))
	if err != nil {
		return fmt.Errorf("installed tool did not read .env: %w", err)
	}
	if !bytes.Equal(got, []byte(placeholderEnv)) {
		return fmt.Errorf("installed tool did not copy the environment file")
	}
	return nil
}

func startPublisherProxy(t *testing.T, versions []string) string {
	t.Helper()
	args := append([]string{"run", "."}, versions...)
	cmd := exec.Command(toolchainGo(), args...)
	cmd.Dir = filepath.Join("testdata", "winresproxy")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("publisher proxy did not start: %v", err)
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "http://") {
		t.Fatalf("publisher proxy address %q", line)
	}
	return line
}

func toolchainGo() string {
	cmd := exec.Command("go", "env", "GOROOT")
	out, err := cmd.Output()
	if err != nil {
		return "go"
	}
	return filepath.Join(strings.TrimSpace(string(out)), "bin", "go")
}

func releaseEnv(proxy, gopath string, extra map[string]string, publisher bool) []string {
	goBin := toolchainGo()
	modcache, cache := os.Getenv("GOMODCACHE"), os.Getenv("GOCACHE")
	if modcache == "" || cache == "" {
		modcache, cache = defaultGoDirs()
	}
	env := []string{
		"PATH=" + filepath.Dir(goBin) + ":" + filepath.Join(gopath, "bin") + ":/usr/bin:/bin",
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.TempDir(),
		"GOTOOLCHAIN=local",
		"GO111MODULE=on",
		"GOPATH=" + gopath,
		"GOMODCACHE=" + modcache,
		"GOCACHE=" + cache,
		"GIT_TERMINAL_PROMPT=0",
		"TOOL_NAME=chauffeur",
	}
	if publisher {
		env = append(env,
			"GOPROXY="+proxy,
			"GOSUMDB="+sumDBVerifier+" "+proxy,
			"GONOSUMDB=",
			"GONOPROXY=",
			"GOPRIVATE=",
		)
	} else {
		env = append(env, "GOPROXY=https://proxy.golang.org,direct", "GOSUMDB=sum.golang.org")
	}
	for k, v := range extra {
		env = setEnv(env, k, v)
	}
	return env
}

func defaultGoDirs() (string, string) {
	out, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		return "", ""
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return "", ""
	}
	return fields[0], fields[1]
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	next := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		next = append(next, e)
	}
	return append(next, prefix+value)
}

func runBash(dir, script string, env []string) (string, error) {
	cmd := exec.Command("bash")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func copyRepo(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == ".git" || strings.HasPrefix(path, ".git"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, path)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func scriptInstallsWinres(script string) bool {
	return strings.Contains(script, "go-winres") || strings.Contains(script, "tc-hib/go-winres")
}

func substituteActions(s string) string {
	s = strings.ReplaceAll(s, "${{ needs.resolve-version.outputs.simple_version }}", releaseVersion)
	s = strings.ReplaceAll(s, "${{ matrix.goos }}", "windows")
	s = strings.ReplaceAll(s, "${{ matrix.arch }}", "amd64")
	return s
}

type workflowFile struct {
	Env  map[string]string `yaml:"env"`
	Jobs map[string]wfJob  `yaml:"jobs"`
}

type wfJob struct {
	If          string   `yaml:"if"`
	Environment string   `yaml:"environment"`
	RunsOn      string   `yaml:"runs-on"`
	Strategy    wfMatrix `yaml:"strategy"`
	Steps       []wfStep `yaml:"steps"`
}

type wfMatrix struct {
	Matrix struct {
		Include []map[string]string `yaml:"include"`
	} `yaml:"matrix"`
}

type wfStep struct {
	Name string            `yaml:"name"`
	If   string            `yaml:"if"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
}

type condVars struct {
	event, matrixGOOS, runnerOS string
}

var winresSelector = regexp.MustCompile(`github\.com/tc-hib/go-winres(?:@([^\s"'` + "`" + `]+))?`)

// publisherVersions is the set of releases a floating selector resolves to.
// An exact module version written in the packaging command is not published,
// so installing that version cannot run this publisher's new code.
func publisherVersions(texts ...string) []string {
	exact := map[string]bool{}
	var selectors []string
	for _, text := range texts {
		for _, match := range winresSelector.FindAllStringSubmatch(text, -1) {
			sel := match[1]
			selectors = append(selectors, sel)
			if exactRelease(sel) {
				exact[sel] = true
			}
		}
	}
	set := map[string]bool{}
	add := func(v string) {
		if v != "" && !exact[v] {
			set[v] = true
		}
	}
	add("v0.9.9")
	for _, sel := range selectors {
		switch {
		case sel == "" || sel == "latest" || sel == "upgrade" || sel == "patch":
			add("v0.9.9")
		case strings.HasPrefix(sel, ">="):
			add(above(strings.TrimPrefix(sel, ">=")))
		case strings.HasPrefix(sel, ">"):
			add(above(strings.TrimPrefix(sel, ">")))
		case strings.HasPrefix(sel, "<="):
			add(below(strings.TrimPrefix(sel, "<=")))
		case strings.HasPrefix(sel, "<"):
			add(below(strings.TrimPrefix(sel, "<")))
		default:
			add(prefixRelease(sel))
		}
	}
	if len(set) == 0 {
		add("v0.8.8")
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	return out
}

func exactRelease(sel string) bool {
	if !strings.HasPrefix(sel, "v") || strings.ContainsAny(sel, "<>") {
		return false
	}
	base := sel
	if i := strings.IndexAny(sel, "-+"); i >= 0 {
		base = sel[:i]
	}
	parts := strings.Split(strings.TrimPrefix(base, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}
	return true
}

func prefixRelease(sel string) string {
	if !strings.HasPrefix(sel, "v") || strings.ContainsAny(sel, "<>") {
		return ""
	}
	base := sel
	if i := strings.IndexAny(sel, "-+"); i >= 0 {
		base = sel[:i]
	}
	parts := strings.Split(strings.TrimPrefix(base, "v"), ".")
	switch len(parts) {
	case 1:
		if parts[0] == "" || strings.Trim(parts[0], "0123456789") != "" {
			return ""
		}
		return "v" + parts[0] + ".99.99"
	case 2:
		if strings.Trim(parts[0], "0123456789") != "" || strings.Trim(parts[1], "0123456789") != "" {
			return ""
		}
		return "v" + parts[0] + "." + parts[1] + ".99"
	default:
		return ""
	}
}

func above(sel string) string {
	if prefixRelease(sel) != "" {
		return "v99.0.0"
	}
	return "v99.0.0"
}

func below(sel string) string {
	if !strings.HasPrefix(sel, "v") {
		return "v0.0.0"
	}
	base := sel
	if i := strings.IndexAny(sel, "-+"); i >= 0 {
		base = sel[:i]
	}
	parts := strings.Split(strings.TrimPrefix(base, "v"), ".")
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	if parts[2] == "0" {
		return "v0.0.0"
	}
	return "v" + parts[0] + "." + parts[1] + ".0"
}

func windowsReleaseJobBytes(data []byte) (*wfJob, map[string]string, error) {
	var wf workflowFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, nil, err
	}
	vars := condVars{event: "push", matrixGOOS: "windows", runnerOS: "Linux"}
	for _, job := range wf.Jobs {
		if !condTrue(job.If, vars) || !jobBuildsWindows(job) {
			continue
		}
		for _, step := range job.Steps {
			if condTrue(step.If, vars) && scriptInstallsWinres(step.Run) {
				found := job
				return &found, wf.Env, nil
			}
		}
	}
	return nil, wf.Env, nil
}

func jobBuildsWindows(job wfJob) bool {
	if strings.Contains(strings.ToLower(job.RunsOn), "windows") {
		return true
	}
	for _, entry := range job.Strategy.Matrix.Include {
		if entry["goos"] == "windows" || entry["os"] == "windows" {
			return true
		}
	}
	return false
}

func condTrue(cond string, v condVars) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return true
	}
	cond = strings.ReplaceAll(cond, "github.event_name", "'"+v.event+"'")
	cond = strings.ReplaceAll(cond, "matrix.goos", "'"+v.matrixGOOS+"'")
	cond = strings.ReplaceAll(cond, "runner.os", "'"+v.runnerOS+"'")
	for _, part := range strings.Split(cond, "&&") {
		part = strings.TrimSpace(part)
		switch {
		case strings.Contains(part, "!="):
			left, right, ok := cutOp(part, "!=")
			if !ok || unquote(left) == unquote(right) {
				return false
			}
		case strings.Contains(part, "=="):
			left, right, ok := cutOp(part, "==")
			if !ok || unquote(left) != unquote(right) {
				return false
			}
		default:
			return true
		}
	}
	return true
}

func cutOp(s, op string) (string, string, bool) {
	left, right, ok := strings.Cut(s, op)
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(left), strings.TrimSpace(right), true
}

func unquote(s string) string {
	return strings.Trim(s, "'\"")
}
