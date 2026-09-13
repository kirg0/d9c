package docker

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	composeWebDir    = "/srv/web"
	composeWebConfig = "/srv/web/docker-compose.yml"
	composeSudoProbe = "docker version --format '{{.Server.Version}}'"

	// Deployments whose labels lack the config file / working directory.
	composeListNoConfig  = `[{"Id":"abc","Names":["/web"],"Image":"nginx","State":"running","Labels":{"com.docker.compose.project":"web","com.docker.compose.project.working_dir":"/srv/web"}}]`
	composeListNoWorkdir = `[{"Id":"abc","Names":["/web"],"Image":"nginx","State":"running","Labels":{"com.docker.compose.project":"web"}}]`
)

// composeSSHCmd is the command runComposeSSH* builds for mockContainerList's project.
func composeSSHCmd(action string) string {
	return buildComposeCmd("docker compose", "web", composeWebDir, composeWebConfig, action)
}

// newComposeSSHBackend wires a mock daemon (serving the given container list)
// to an in-process SSH server scripted by handler.
func newComposeSSHBackend(t *testing.T, list string, handler sshExecHandler) (*dockerBackend, *sshTestServer) {
	t.Helper()
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if m, p := route(r); m == "GET" && p == "/containers/json" {
			jsonOK(w, list)
			return
		}
		jsonErr(w, http.StatusNotFound, "unexpected")
	})
	srv := newSSHTestServer(t, handler)
	b.sshClient = srv.dial(t)
	return b, srv
}

func TestComposeSSHOpsRequireSSH(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, _ *http.Request) { jsonOK(w, mockContainerList) })

	streams := map[string]func() (<-chan string, func(), error){
		"up":      func() (<-chan string, func(), error) { return b.ComposeUp(composeWebDir) },
		"pull":    func() (<-chan string, func(), error) { return b.ComposePull(composeWebDir) },
		"down":    func() (<-chan string, func(), error) { return b.ComposeDown(composeWebDir) },
		"create":  func() (<-chan string, func(), error) { return b.CreateComposeFile("/srv/new", "services: {}") },
		"restore": func() (<-chan string, func(), error) { return b.RestoreComposeProject(composeWebDir, "x.tar.gz") },
	}
	for name, op := range streams {
		if _, _, err := op(); err == nil || !strings.Contains(err.Error(), "SSH") {
			t.Errorf("%s without SSH: err = %v", name, err)
		}
	}
	if _, err := b.ComposeConfig(composeWebDir); err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Errorf("config without SSH: err = %v", err)
	}
	if _, err := b.BackupComposeProject(composeWebDir); err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Errorf("backup without SSH: err = %v", err)
	}
	if _, _, err := b.ReadComposeFile(composeWebDir); err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Errorf("read without SSH: err = %v", err)
	}
	if err := b.WriteComposeFile(composeWebDir, "x"); err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Errorf("write without SSH: err = %v", err)
	}
}

func TestComposeConfigOverSSH(t *testing.T) {
	b, srv := newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		composeSSHCmd("config"): sshReply("services:\n  web: {}\n", "", 0),
	}))
	out, err := b.ComposeConfig(composeWebDir)
	if err != nil || !strings.Contains(out, "web: {}") {
		t.Fatalf("config = %q, %v", out, err)
	}
	if got := srv.commands(); len(got) != 1 || got[0] != composeSSHCmd("config") {
		t.Errorf("commands = %q", got)
	}

	// Hosts where docker needs sudo: the plain run fails, sudo succeeds.
	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		composeSSHCmd("config"):           sshReply("", "permission denied", 1),
		"sudo " + composeSSHCmd("config"): sshReply("services: {}\n", "", 0),
	}))
	if out, err := b.ComposeConfig(composeWebDir); err != nil || !strings.Contains(out, "services: {}") {
		t.Errorf("sudo fallback = %q, %v", out, err)
	}

	// Both fail: the plain error is reported.
	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		composeSSHCmd("config"): sshReply("", "permission denied", 1),
	}))
	if _, err := b.ComposeConfig(composeWebDir); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("config error = %v", err)
	}

	b, _ = newComposeSSHBackend(t, `[]`, sshScript(nil))
	if _, err := b.ComposeConfig(composeWebDir); err == nil || !strings.Contains(err.Error(), "no containers found") {
		t.Errorf("unknown deployment error = %v", err)
	}
}

func TestComposeStreamsOverSSH(t *testing.T) {
	b, srv := newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		composeSudoProbe:       sshReply("27.4.0\n", "", 0),
		composeSSHCmd("up -d"): sshReply("Container web-1 Started\n", "Network web_default Created\n", 0),
		composeSSHCmd("pull"):  sshReply("", "pull access denied\n", 18),
	}))

	ch, stop, err := b.ComposeUp(composeWebDir)
	up := drainStream(t, ch, stop, err)
	if !strings.Contains(up, "Container web-1 Started") || !strings.Contains(up, "Network web_default Created") {
		t.Errorf("up output should merge stdout and stderr: %q", up)
	}
	if strings.Contains(up, "error:") {
		t.Errorf("successful up must not report an error: %q", up)
	}

	ch, stop, err = b.ComposePull(composeWebDir)
	if pull := drainStream(t, ch, stop, err); !strings.Contains(pull, "pull access denied") || !strings.Contains(pull, "error:") {
		t.Errorf("failed pull should end with an error line: %q", pull)
	}

	probes := 0
	for _, c := range srv.commands() {
		if c == composeSudoProbe {
			probes++
		}
	}
	if probes != 1 {
		t.Errorf("sudo probe ran %d times, want 1 (cached)", probes)
	}

	// A host that needs sudo runs the compose command under sudo.
	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		composeSudoProbe:                sshReply("", "permission denied", 1),
		"sudo " + composeSudoProbe:      sshReply("27.4.0\n", "", 0),
		"sudo " + composeSSHCmd("down"): sshReply("Container web-1 Removed\n", "", 0),
	}))
	ch, stop, err = b.ComposeDown(composeWebDir)
	if down := drainStream(t, ch, stop, err); !strings.Contains(down, "Removed") {
		t.Errorf("sudo down output = %q", down)
	}
}

func TestCreateComposeFileOverSSH(t *testing.T) {
	const content = "services:\n  app:\n    image: nginx\n"
	var tee stdinCapture
	b, _ := newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		"mkdir -p '/srv/new'":                           sshReply("", "", 0),
		"tee '/srv/new/docker-compose.yaml' >/dev/null": tee.handler(0),
		composeSudoProbe:                                sshReply("27\n", "", 0),
		"docker compose --project-directory '/srv/new' -f '/srv/new/docker-compose.yaml' up -d": sshReply("Container app-1 Started\n", "", 0),
	}))

	if _, _, err := b.CreateComposeFile("/", content); err == nil {
		t.Error("an empty target directory must be rejected")
	}
	ch, stop, err := b.CreateComposeFile(`\srv\new\`, content)
	if out := drainStream(t, ch, stop, err); !strings.Contains(out, "Started") {
		t.Errorf("create output = %q", out)
	}
	if tee.get() != content {
		t.Errorf("written compose file = %q, want %q", tee.get(), content)
	}

	// Permission-restricted location: mkdir and tee fall back to sudo.
	var sudoTee stdinCapture
	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		"mkdir -p '/opt/app'":      sshReply("", "Permission denied", 1),
		"sudo mkdir -p '/opt/app'": sshReply("", "", 0),
		"tee '/opt/app/docker-compose.yaml' >/dev/null": func(_ string, in io.Reader, _, errw io.Writer) int {
			_, _ = io.Copy(io.Discard, in)
			_, _ = io.WriteString(errw, "tee: Permission denied")
			return 1
		},
		"sudo tee '/opt/app/docker-compose.yaml' >/dev/null": sudoTee.handler(0),
		composeSudoProbe: sshReply("27\n", "", 0),
		"docker compose --project-directory '/opt/app' -f '/opt/app/docker-compose.yaml' up -d": sshReply("ok\n", "", 0),
	}))
	ch, stop, err = b.CreateComposeFile("/opt/app", content)
	drainStream(t, ch, stop, err)
	if sudoTee.get() != content {
		t.Errorf("sudo tee content = %q", sudoTee.get())
	}

	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		"mkdir -p '/root/x'": sshReply("", "Permission denied", 1),
	}))
	if _, _, err := b.CreateComposeFile("/root/x", content); err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("mkdir failure = %v", err)
	}
}

func TestReadWriteComposeFileOverSSH(t *testing.T) {
	quoted := "'" + composeWebConfig + "'"
	var written stdinCapture
	b, _ := newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		"cat " + quoted:                 sshReply("services: {}\n", "", 0),
		"tee " + quoted + " >/dev/null": written.handler(0),
	}))
	path, content, err := b.ReadComposeFile(composeWebDir)
	if err != nil || path != composeWebConfig || content != "services: {}\n" {
		t.Errorf("read = %q, %q, %v", path, content, err)
	}
	if err := b.WriteComposeFile(composeWebDir, "services:\n  web: {}\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if written.get() != "services:\n  web: {}\n" {
		t.Errorf("written = %q", written.get())
	}

	var sudoWritten stdinCapture
	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		"cat " + quoted:      sshReply("", "cat: Permission denied", 1),
		"sudo cat " + quoted: sshReply("services: {}\n", "", 0),
		"tee " + quoted + " >/dev/null": func(_ string, in io.Reader, _, errw io.Writer) int {
			_, _ = io.Copy(io.Discard, in)
			_, _ = io.WriteString(errw, "tee: Permission denied")
			return 1
		},
		"sudo tee " + quoted + " >/dev/null": sudoWritten.handler(0),
	}))
	if _, content, err := b.ReadComposeFile(composeWebDir); err != nil || content != "services: {}\n" {
		t.Errorf("sudo read = %q, %v", content, err)
	}
	if err := b.WriteComposeFile(composeWebDir, "new"); err != nil || sudoWritten.get() != "new" {
		t.Errorf("sudo write = %q, %v", sudoWritten.get(), err)
	}

	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		"cat " + quoted: sshReply("", "cat: No such file or directory", 1),
	}))
	if _, _, err := b.ReadComposeFile(composeWebDir); err == nil || !strings.Contains(err.Error(), "No such file") {
		t.Errorf("read failure = %v", err)
	}

	b, _ = newComposeSSHBackend(t, composeListNoConfig, sshScript(nil))
	if _, _, err := b.ReadComposeFile(composeWebDir); err == nil || !strings.Contains(err.Error(), "no recorded compose file") {
		t.Errorf("read without config label = %v", err)
	}
	if err := b.WriteComposeFile(composeWebDir, "x"); err == nil {
		t.Error("write without config label should fail")
	}
}

func TestBackupComposeProjectOverSSH(t *testing.T) {
	t.Chdir(t.TempDir())
	const tarCmd = "tar czf - -C '/srv/web' ."

	b, _ := newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		tarCmd: sshReply("TARGZ-BYTES", "", 0),
	}))
	local, err := b.BackupComposeProject(composeWebDir)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !strings.HasPrefix(local, BackupFilePrefix(composeDisplayName("web", composeWebDir))) {
		t.Errorf("archive name = %q", local)
	}
	if data, _ := os.ReadFile(local); string(data) != "TARGZ-BYTES" {
		t.Errorf("archive content = %q", data)
	}

	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		tarCmd:           sshReply("", "tar: Permission denied", 2),
		"sudo " + tarCmd: sshReply("SUDO-TAR", "", 0),
	}))
	local, err = b.BackupComposeProject(composeWebDir)
	if data, _ := os.ReadFile(local); err != nil || string(data) != "SUDO-TAR" {
		t.Errorf("sudo backup = %q, %v", data, err)
	}

	// A failed backup must not leave a partial archive behind. Use a fresh
	// directory: archives are named per second, so an earlier backup in the same
	// second would share the name.
	t.Chdir(t.TempDir())
	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		tarCmd: sshReply("partial", "tar: Permission denied", 2),
	}))
	if _, err := b.BackupComposeProject(composeWebDir); err == nil || !strings.Contains(err.Error(), "unexpected command") {
		t.Errorf("failed backup error = %v, want the sudo attempt's stderr", err)
	}
	if left, _ := os.ReadDir("."); len(left) != 0 {
		t.Errorf("failed backup left files behind: %v", left)
	}

	b, _ = newComposeSSHBackend(t, composeListNoWorkdir, sshScript(nil))
	if _, err := b.BackupComposeProject("web"); err == nil || !strings.Contains(err.Error(), "no working directory") {
		t.Errorf("backup without working dir = %v", err)
	}
}

func TestRestoreComposeProjectOverSSH(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(dir, "web-20260101-000000.tar.gz")
	if err := os.WriteFile(backup, []byte("ARCHIVE"), 0o600); err != nil {
		t.Fatal(err)
	}
	const extractCmd = "tar xzf - -C '/srv/web'"

	var extracted stdinCapture
	b, _ := newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		composeSudoProbe:       sshReply("27\n", "", 0),
		extractCmd:             extracted.handler(0),
		composeSSHCmd("up -d"): sshReply("Container web-1 Started\n", "", 0),
	}))
	ch, stop, err := b.RestoreComposeProject(composeWebDir, backup)
	out := drainStream(t, ch, stop, err)
	for _, want := range []string{"extracting", "starting project", "Container web-1 Started"} {
		if !strings.Contains(out, want) {
			t.Errorf("restore output missing %q: %q", want, out)
		}
	}
	if extracted.get() != "ARCHIVE" {
		t.Errorf("uploaded archive = %q", extracted.get())
	}

	b, _ = newComposeSSHBackend(t, mockContainerList, sshScript(map[string]sshExecHandler{
		composeSudoProbe: sshReply("27\n", "", 0),
		extractCmd: func(_ string, in io.Reader, _, errw io.Writer) int {
			_, _ = io.Copy(io.Discard, in)
			_, _ = io.WriteString(errw, "tar: invalid archive")
			return 2
		},
	}))
	ch, stop, err = b.RestoreComposeProject(composeWebDir, backup)
	out = drainStream(t, ch, stop, err)
	if !strings.Contains(out, "error: tar: invalid archive") || strings.Contains(out, "starting project") {
		t.Errorf("failed extract should stop before up: %q", out)
	}

	if _, _, err := b.RestoreComposeProject(composeWebDir, filepath.Join(dir, "missing.tar.gz")); err == nil ||
		!strings.Contains(err.Error(), "open backup") {
		t.Errorf("missing archive error = %v", err)
	}

	b, _ = newComposeSSHBackend(t, composeListNoWorkdir, sshScript(nil))
	if _, _, err := b.RestoreComposeProject("web", backup); err == nil || !strings.Contains(err.Error(), "no working directory") {
		t.Errorf("restore without working dir = %v", err)
	}
}
