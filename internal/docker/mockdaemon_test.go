package docker

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"d9c/internal/config"
)

// apiVersionRe strips the /v1.xx prefix the SDK prepends after negotiation.
var apiVersionRe = regexp.MustCompile(`^/v[0-9.]+`)

// newMockBackend spins an httptest server that plays a Docker daemon and
// returns a real dockerBackend connected to it over tcp://.
func newMockBackend(t *testing.T, handler http.HandlerFunc) *dockerBackend {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("API-Version", "1.43")
			w.WriteHeader(http.StatusOK)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	host := "tcp://" + strings.TrimPrefix(srv.URL, "http://")
	b, err := New(&config.Config{Host: host})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(b.Close)
	db, ok := b.(*dockerBackend)
	if !ok {
		t.Fatalf("backend is %T, want *dockerBackend", b)
	}
	return db
}

// route matches a request against method + path (with the version prefix
// stripped).
func route(r *http.Request) (method, path string) {
	return r.Method, apiVersionRe.ReplaceAllString(r.URL.Path, "")
}

func jsonOK(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"message":%q}`, msg)
}

const mockContainerList = `[
  {"Id":"abcdefabcdefabcdefabcdef","Names":["/web"],"Image":"nginx:1.25",
   "Status":"Up 2 hours (healthy)","State":"running","Created":1720000000,
   "Ports":[{"PrivatePort":80,"PublicPort":8080,"Type":"tcp"},
            {"PrivatePort":80,"PublicPort":8080,"Type":"tcp"},
            {"PrivatePort":443,"Type":"tcp"}],
   "Labels":{"com.docker.compose.project":"web",
             "com.docker.compose.project.working_dir":"/srv/web",
             "com.docker.compose.project.config_files":"/srv/web/docker-compose.yml"},
   "NetworkSettings":{"Networks":{"bridge":{}}}},
  {"Id":"1234","Names":[],"Image":"redis","Status":"Exited (0) 1h ago","State":"exited","Created":1720000001}
]`

const mockContainerInspect = `{
  "Id":"abcdefabcdefabcdefabcdef","Name":"/web","Created":"2026-01-01T00:00:00Z",
  "State":{"Status":"running","StartedAt":"2026-01-01T00:00:01Z"},
  "Config":{"Tty":false,"Image":"nginx:1.25","Env":["A=1"]},
  "Mounts":[{"Type":"volume","Name":"pgdata","Source":"/var/lib/docker/volumes/pgdata","Destination":"/data"},
            {"Type":"bind","Source":"/srv/web","Destination":"/app"}],
  "NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"8080"}],"443/tcp":[]},
                     "Networks":{"bridge":{}}}
}`

// muxLogs writes one multiplexed stdout frame per line.
func muxLogs(w http.ResponseWriter, lines ...string) {
	for _, l := range lines {
		payload := []byte(l + "\n")
		header := make([]byte, 8)
		header[0] = 1 // stdout
		binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
		_, _ = w.Write(header)
		_, _ = w.Write(payload)
	}
}

func TestMockContainers(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "GET" && p == "/containers/json":
			jsonOK(w, mockContainerList)
		case m == "GET" && strings.HasSuffix(p, "/ttyctr/json"):
			jsonOK(w, `{"Id":"ttyctr","Name":"/tty","Config":{"Tty":true},"State":{"Status":"running"}}`)
		case m == "GET" && strings.HasSuffix(p, "/json"):
			jsonOK(w, mockContainerInspect)
		case m == "POST" && strings.HasSuffix(p, "/start"),
			m == "POST" && strings.HasSuffix(p, "/stop"),
			m == "POST" && strings.HasSuffix(p, "/restart"),
			m == "POST" && strings.HasSuffix(p, "/kill"):
			w.WriteHeader(http.StatusNoContent)
		case m == "DELETE" && strings.HasPrefix(p, "/containers/"):
			w.WriteHeader(http.StatusNoContent)
		case m == "GET" && strings.HasSuffix(p, "/logs"):
			if strings.Contains(p, "ttyctr") {
				fmt.Fprint(w, "raw tty line\n")
				return
			}
			muxLogs(w, "line one", "line two")
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	list, err := b.ListContainers(true)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d (%v), want 2", len(list), err)
	}
	if list[0].Name != "web" || list[0].Health != "healthy" || list[0].ID != "abcdefabcdef" {
		t.Errorf("container[0] = %+v", list[0])
	}
	if list[0].Ports != "8080->80/tcp, 443/tcp" {
		t.Errorf("ports = %q, want deduped list", list[0].Ports)
	}
	if list[1].Name != "" {
		t.Errorf("unnamed container should map to empty name, got %q", list[1].Name)
	}
	if _, err := b.ListContainers(false); err != nil {
		t.Errorf("running-only list: %v", err)
	}

	res, err := b.InspectContainer("abcdefabcdefabcdefabcdef")
	if err != nil || res.Name != "web" || !strings.Contains(res.RawYAML, "nginx:1.25") {
		t.Errorf("inspect = %+v / %v", res, err)
	}

	for name, op := range map[string]func(string) error{
		"start":   b.StartContainer,
		"stop":    b.StopContainer,
		"restart": b.RestartContainer,
	} {
		if err := op("abc"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := b.RemoveContainer("abc", true); err != nil {
		t.Errorf("rm: %v", err)
	}
	if err := b.KillContainer("abc", ""); err != nil { // empty signal defaults to SIGKILL
		t.Errorf("kill: %v", err)
	}

	// Non-TTY logs come through the multiplex-stripping path.
	ch, stop, err := b.ContainerLogs("abcdefabcdefabcdefabcdef", LogOptions{Tail: 10, Since: "1h"})
	lines := collect(t, ch, stop, err)
	if len(lines) != 2 || lines[0] != "line one" {
		t.Errorf("logs = %v", lines)
	}
	// TTY logs pass through untouched.
	ch, stop, err = b.ContainerLogs("ttyctr", LogOptions{})
	lines = collect(t, ch, stop, err)
	if len(lines) != 1 || lines[0] != "raw tty line" {
		t.Errorf("tty logs = %v", lines)
	}
}

func TestMockRunContainerPullRetry(t *testing.T) {
	var creates atomic.Int32
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "POST" && p == "/containers/create":
			if creates.Add(1) == 1 {
				jsonErr(w, http.StatusNotFound, "No such image: busybox:latest")
				return
			}
			w.WriteHeader(http.StatusCreated)
			jsonOK(w, `{"Id":"fresh1"}`)
		case m == "POST" && p == "/images/create": // pull
			jsonOK(w, `{"status":"Pulling from library/busybox"}`)
		case m == "POST" && strings.HasSuffix(p, "/start"):
			w.WriteHeader(http.StatusNoContent)
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	if err := b.RunContainer(RunOptions{}); err == nil {
		t.Error("empty image should fail")
	}
	if err := b.RunContainer(RunOptions{Image: "x", Ports: []string{"nope:spec:extra:parts"}}); err == nil {
		t.Error("bad port spec should fail")
	}
	if err := b.RunContainer(RunOptions{Image: "busybox:latest", Name: "bb", Ports: []string{"8080:80"}}); err != nil {
		t.Fatalf("run with pull retry: %v", err)
	}
	if creates.Load() != 2 {
		t.Errorf("creates = %d, want 2 (retry after pull)", creates.Load())
	}
}

func TestMockRunContainerFriendlyErrors(t *testing.T) {
	var mode atomic.Value
	mode.Store("conflict")
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if m, p := route(r); m == "POST" && p == "/containers/create" {
			switch mode.Load() {
			case "conflict":
				jsonErr(w, http.StatusConflict, `Conflict. The container name "/bb" is already in use`)
			default:
				jsonErr(w, http.StatusBadRequest, "invalid port specification")
			}
			return
		}
		jsonErr(w, http.StatusNotFound, "unexpected")
	})

	wantConflict := friendlyRunErr(fmt.Errorf("is already in use")).Error()
	if err := b.RunContainer(RunOptions{Image: "busybox"}); err == nil || err.Error() != wantConflict {
		t.Errorf("conflict err = %v, want friendly 'name is taken'", err)
	}
	mode.Store("port")
	wantPort := friendlyRunErr(fmt.Errorf("invalid port")).Error()
	if err := b.RunContainer(RunOptions{Image: "busybox"}); err == nil || err.Error() != wantPort {
		t.Errorf("port err = %v, want friendly port hint", err)
	}
}

func TestMockImages(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "GET" && p == "/images/json":
			jsonOK(w, `[
			  {"Id":"sha256:abcdefabcdefabcdef","RepoTags":["nginx:1.25"],"Size":1258291200,"Created":1720000000},
			  {"Id":"short","RepoTags":[],"Size":2048,"Created":1720000001}
			]`)
		case m == "GET" && strings.HasSuffix(p, "/tagged/json"):
			jsonOK(w, `{"Id":"sha256:aa","RepoTags":["nginx:1.25"]}`)
		case m == "GET" && strings.HasSuffix(p, "/untagged/json"):
			jsonOK(w, `{"Id":"sha256:bb","RepoTags":[]}`)
		case m == "DELETE" && strings.HasPrefix(p, "/images/"):
			jsonOK(w, `[{"Deleted":"sha256:aa"}]`)
		case m == "POST" && p == "/images/create":
			jsonOK(w, `{"status":"Downloading","id":"aa","progress":"[==> ]"}`)
		case m == "POST" && strings.HasSuffix(p, "/tag"):
			w.WriteHeader(http.StatusCreated)
		case m == "POST" && strings.HasSuffix(p, "/push"):
			jsonOK(w, `{"status":"Pushed","id":"layer1"}
{"error":"denied"}`)
		case m == "GET" && strings.HasSuffix(p, "/history"):
			jsonOK(w, `[{"Created":1720000000,"CreatedBy":"/bin/sh -c #(nop) CMD [\"nginx\"]","Size":1024},
			            {"Created":1720000001,"CreatedBy":"/bin/sh -c apt-get update","Size":2097152}]`)
		case m == "POST" && p == "/images/prune":
			jsonOK(w, `{"ImagesDeleted":[{"Deleted":"a"},{"Deleted":"b"}],"SpaceReclaimed":42}`)
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	imgs, err := b.ListImages()
	if err != nil || len(imgs) != 2 {
		t.Fatalf("images = %d (%v)", len(imgs), err)
	}
	if imgs[0].ID != "abcdefabcdef" || imgs[0].Tags != "nginx:1.25" || imgs[0].Size != "1.2 GB" {
		t.Errorf("image[0] = %+v", imgs[0])
	}
	if imgs[1].Tags != "<none>" || imgs[1].Size != "2.0 KB" {
		t.Errorf("image[1] = %+v", imgs[1])
	}

	if res, err := b.InspectImage("tagged"); err != nil || res.Name != "nginx:1.25" {
		t.Errorf("inspect tagged = %+v / %v", res, err)
	}
	if res, err := b.InspectImage("untagged"); err != nil || res.Name != "untagged" {
		t.Errorf("inspect untagged = %+v / %v", res, err)
	}

	if err := b.RemoveImage("aa", false); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := b.PullImage("nginx:1.25"); err != nil {
		t.Errorf("pull: %v", err)
	}
	if err := b.TagImage("aa", "bb:latest"); err != nil {
		t.Errorf("tag: %v", err)
	}

	ch, stop, err := b.PushImage("nginx:1.25", RegistryAuth{Registry: "reg", Username: "u", Password: "p"})
	lines := collect(t, ch, stop, err)
	if len(lines) != 2 || !strings.Contains(lines[0], "layer1: Pushed") || lines[1] != "error: denied" {
		t.Errorf("push lines = %v", lines)
	}

	res, err := b.ImageHistory("aa")
	if err != nil || !strings.Contains(res.RawYAML, "CMD [\"nginx\"]") || !strings.Contains(res.RawYAML, "apt-get update") {
		t.Errorf("history = %+v / %v", res, err)
	}
	if strings.Contains(res.RawYAML, "#(nop)") {
		t.Error("history should strip the #(nop) prefix")
	}

	n, err := b.PruneImages()
	if err != nil || n != 2 {
		t.Errorf("prune = %d / %v", n, err)
	}
}

func TestMockBuildImage(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if m, p := route(r); m == "POST" && p == "/build" {
			jsonOK(w, `{"stream":"Step 1/1 : FROM scratch\n"}
{"stream":"Successfully built cafe\n"}`)
			return
		}
		jsonErr(w, http.StatusNotFound, "unexpected")
	})

	if _, _, err := b.BuildImage(t.TempDir()+"\\ghost", "t"); err == nil {
		t.Error("missing context dir should fail")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "app.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, stop, err := b.BuildImage(dir, "demo:1")
	lines := collect(t, ch, stop, err)
	if len(lines) != 2 || !strings.Contains(lines[1], "Successfully built") {
		t.Errorf("build lines = %v", lines)
	}
}

func TestMockNetworks(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "GET" && p == "/networks":
			jsonOK(w, `[
			  {"Id":"0123456789abcdef","Name":"bridge","Driver":"bridge","Scope":"local","IPAM":{"Config":[{"Subnet":"172.17.0.0/16"}]}},
			  {"Id":"short","Name":"none","Driver":"null","Scope":"local","IPAM":{"Config":[]}}
			]`)
		case m == "GET" && strings.HasPrefix(p, "/networks/"):
			jsonOK(w, `{"Id":"0123456789abcdef","Name":"bridge","Driver":"bridge"}`)
		case m == "DELETE" && strings.HasPrefix(p, "/networks/"):
			w.WriteHeader(http.StatusNoContent)
		case m == "POST" && p == "/networks/create":
			w.WriteHeader(http.StatusCreated)
			jsonOK(w, `{"Id":"newnet"}`)
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	nets, err := b.ListNetworks()
	if err != nil || len(nets) != 2 {
		t.Fatalf("networks = %d (%v)", len(nets), err)
	}
	if nets[0].ID != "0123456789ab" || nets[0].Subnet != "172.17.0.0/16" {
		t.Errorf("network[0] = %+v", nets[0])
	}
	if nets[1].Subnet != "" {
		t.Errorf("network[1] should have no subnet, got %q", nets[1].Subnet)
	}
	if res, err := b.InspectNetwork("bridge"); err != nil || res.Name != "bridge" {
		t.Errorf("inspect = %+v / %v", res, err)
	}
	if err := b.RemoveNetwork("bridge"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := b.CreateNetwork(NetworkCreateOptions{}); err == nil {
		t.Error("empty name should fail")
	}
	if err := b.CreateNetwork(NetworkCreateOptions{Name: "n1"}); err != nil {
		t.Errorf("create: %v", err)
	}
	if err := b.CreateNetwork(NetworkCreateOptions{Name: "n2", Driver: "overlay", Subnet: "10.0.0.0/24", Gateway: "10.0.0.1"}); err != nil {
		t.Errorf("create with ipam: %v", err)
	}
}

func TestMockVolumes(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "GET" && p == "/volumes":
			jsonOK(w, `{"Volumes":[{"Name":"pgdata","Driver":"local","Mountpoint":"/x","CreatedAt":"2026-01-01T00:00:00.123456789Z"},
			                        {"Name":"tmp","Driver":"local","Mountpoint":"/y","CreatedAt":"2026-01-02"}]}`)
		case m == "GET" && strings.HasPrefix(p, "/volumes/"):
			jsonOK(w, `{"Name":"pgdata","Driver":"local","Mountpoint":"/x"}`)
		case m == "DELETE" && strings.HasPrefix(p, "/volumes/"):
			w.WriteHeader(http.StatusNoContent)
		case m == "POST" && p == "/volumes/create":
			w.WriteHeader(http.StatusCreated)
			jsonOK(w, `{"Name":"v1","Driver":"local","Mountpoint":"/z"}`)
		case m == "POST" && p == "/volumes/prune":
			jsonOK(w, `{"VolumesDeleted":["a","b","c"],"SpaceReclaimed":99}`)
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	vols, err := b.ListVolumes()
	if err != nil || len(vols) != 2 {
		t.Fatalf("volumes = %d (%v)", len(vols), err)
	}
	if vols[0].Created != "2026-01-01T00:00:00" {
		t.Errorf("created should trim nanoseconds, got %q", vols[0].Created)
	}
	if vols[1].Created != "2026-01-02" {
		t.Errorf("short created should pass through, got %q", vols[1].Created)
	}
	if res, err := b.InspectVolume("pgdata"); err != nil || res.Name != "pgdata" {
		t.Errorf("inspect = %+v / %v", res, err)
	}
	if err := b.RemoveVolume("pgdata"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := b.CreateVolume(VolumeCreateOptions{}); err == nil {
		t.Error("empty name should fail")
	}
	if err := b.CreateVolume(VolumeCreateOptions{Name: "v1"}); err != nil {
		t.Errorf("create: %v", err)
	}
	if n, err := b.PruneVolumes(); err != nil || n != 3 {
		t.Errorf("prune = %d / %v", n, err)
	}
}

func TestMockStats(t *testing.T) {
	var calls atomic.Int32
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if m, p := route(r); m == "GET" && strings.HasSuffix(p, "/stats") {
			total, system := 1000, 10000
			if calls.Add(1) > 1 {
				total, system = 4000, 20000
			}
			jsonOK(w, fmt.Sprintf(`{
			  "cpu_stats":{"cpu_usage":{"total_usage":%d},"system_cpu_usage":%d,"online_cpus":2},
			  "precpu_stats":{"cpu_usage":{"total_usage":0}},
			  "memory_stats":{"usage":2097152,"limit":4194304,"stats":{"inactive_file":1048576}},
			  "networks":{"eth0":{"rx_bytes":100,"tx_bytes":50},"eth1":{"rx_bytes":10,"tx_bytes":5}},
			  "blkio_stats":{"io_service_bytes_recursive":[{"op":"Read","value":300},{"op":"Write","value":200}]}
			}`, total, system))
			return
		}
		jsonErr(w, http.StatusNotFound, "unexpected")
	})

	// First batch has no cached sample: CPU% falls back to the zero-precpu
	// ratio (1000/10000 * 2 CPUs * 100 = 20).
	stats, err := b.ContainerStats([]string{"c1"})
	if err != nil || len(stats) != 1 {
		t.Fatalf("stats = %v / %v", stats, err)
	}
	s := stats["c1"]
	if s.CPUPerc != 20 {
		t.Errorf("first CPU%% = %v, want 20", s.CPUPerc)
	}
	if s.MemUsage != 1048576 || s.MemLimit != 4194304 || s.MemPerc != 25 {
		t.Errorf("mem = %+v", s)
	}
	if s.NetRx != 110 || s.NetTx != 55 || s.BlockRead != 300 || s.BlockWrite != 200 {
		t.Errorf("io = %+v", s)
	}

	// Second batch computes CPU% from the cached delta: 3000/10000*2*100 = 60.
	stats, err = b.ContainerStats([]string{"c1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := stats["c1"].CPUPerc; got != 60 {
		t.Errorf("second CPU%% = %v, want 60", got)
	}

	// A batch without c1 evicts its cached sample.
	if _, err := b.ContainerStats(nil); err != nil {
		t.Fatal(err)
	}
	b.statsMu.Lock()
	_, cached := b.statsPrev["c1"]
	b.statsMu.Unlock()
	if cached {
		t.Error("stale cpu sample should be evicted")
	}
}

func TestMockSystem(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "GET" && p == "/system/df":
			jsonOK(w, `{
			  "LayersSize":1073741824,
			  "Images":[{"Containers":1,"Size":100,"SharedSize":10},{"Containers":0,"Size":100,"SharedSize":10},{"Containers":0,"Size":5,"SharedSize":10}],
			  "Containers":[{"SizeRw":10,"State":"running"},{"SizeRw":5,"State":"exited"}],
			  "Volumes":[{"UsageData":{"Size":50,"RefCount":1}},{"UsageData":{"Size":30,"RefCount":0}},{"Name":"nil-usage"}],
			  "BuildCache":[{"Size":20,"InUse":true},{"Size":7,"InUse":false}]
			}`)
		case m == "POST" && p == "/containers/prune":
			jsonOK(w, `{"ContainersDeleted":["a"],"SpaceReclaimed":1}`)
		case m == "POST" && p == "/networks/prune":
			jsonOK(w, `{"NetworksDeleted":["n"]}`)
		case m == "POST" && p == "/images/prune":
			jsonOK(w, `{"ImagesDeleted":[{"Deleted":"i"}],"SpaceReclaimed":2}`)
		case m == "POST" && p == "/build/prune":
			jsonOK(w, `{"CachesDeleted":["c1","c2"],"SpaceReclaimed":3}`)
		case m == "GET" && p == "/info":
			jsonOK(w, `{"Containers":3,"ContainersRunning":2,"ContainersPaused":0,"ContainersStopped":1,
			            "Images":4,"ServerVersion":"27.0.1","Name":"mockhost","NCPU":8,"MemTotal":1024}`)
		case m == "GET" && p == "/version":
			jsonOK(w, `{"Version":"27.0.1","Platform":{"Name":"Docker Engine - Community"},"Components":[{"Name":"Engine"}]}`)
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	res, err := b.SystemDF()
	if err != nil {
		t.Fatalf("df: %v", err)
	}
	for _, want := range []string{"Images", "Containers", "Local Volumes", "Build Cache", "1.0 GB"} {
		if !strings.Contains(res.RawYAML, want) {
			t.Errorf("df output should contain %q:\n%s", want, res.RawYAML)
		}
	}

	summary, err := b.SystemPrune()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	for _, want := range []string{"1", "2", "prune:"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary should contain %q: %s", want, summary)
		}
	}

	info, err := b.Info()
	if err != nil || info.Version != "27.0.1" || info.Running != 2 || info.NCPU != 8 {
		t.Errorf("info = %+v / %v", info, err)
	}

	if err := b.Ping(); err != nil {
		t.Errorf("ping: %v", err)
	}
	if got := b.Runtime(); got != RuntimeDocker {
		t.Errorf("runtime = %v, want docker", got)
	}
}

func TestMockSystemPrunepartialFailure(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "POST" && p == "/containers/prune":
			jsonErr(w, http.StatusInternalServerError, "prune blew up")
		case m == "POST" && p == "/networks/prune":
			jsonOK(w, `{"NetworksDeleted":[]}`)
		case m == "POST" && p == "/images/prune":
			jsonOK(w, `{"ImagesDeleted":[],"SpaceReclaimed":0}`)
		case m == "POST" && p == "/build/prune":
			jsonOK(w, `{"CachesDeleted":[],"SpaceReclaimed":0}`)
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})
	summary, err := b.SystemPrune()
	if err == nil || !strings.Contains(err.Error(), "containers") {
		t.Errorf("err = %v, want first-stage failure", err)
	}
	if summary == "" {
		t.Error("partial summary should still be reported")
	}
}

func TestMockEvents(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if m, p := route(r); m == "GET" && p == "/events" {
			jsonOK(w, `{"Type":"container","Action":"start","Actor":{"ID":"abcdefabcdefabcdefabcdef","Attributes":{"name":"web"}},"scope":"local"}
{"Type":"network","Action":"connect","Actor":{"ID":"0123456789abcdef","Attributes":{"container":"abcdefabcdefabcdefabcdef"}},"scope":"local"}`)
			return
		}
		jsonErr(w, http.StatusNotFound, "unexpected")
	})

	ch, stop, err := b.Events()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	got := []string{<-ch, <-ch}
	if got[0] != "container start web (local)" {
		t.Errorf("event[0] = %q", got[0])
	}
	if got[1] != "network connect abcdefabcdef (local)" {
		t.Errorf("event[1] = %q", got[1])
	}
	stop()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed after stop — done
			}
		case <-deadline:
			t.Fatal("events channel did not close after stop")
		}
	}
}

func TestMockRuntimeDetection(t *testing.T) {
	podman := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if m, p := route(r); m == "GET" && p == "/version" {
			jsonOK(w, `{"Version":"5.0","Platform":{"Name":"Podman Engine"},"Components":[{"Name":"Podman Engine"}]}`)
			return
		}
		jsonErr(w, http.StatusNotFound, "unexpected")
	})
	if got := podman.Runtime(); got != RuntimePodman {
		t.Errorf("runtime = %v, want podman", got)
	}
	if got := podman.Runtime(); got != RuntimePodman { // cached second probe
		t.Errorf("cached runtime = %v, want podman", got)
	}
	if got := podman.engineCmd(); got != "podman" {
		t.Errorf("engineCmd = %q, want podman", got)
	}

	broken := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		jsonErr(w, http.StatusInternalServerError, "boom")
	})
	if got := broken.Runtime(); got != RuntimeUnknown {
		t.Errorf("runtime after failed probe = %v, want unknown", got)
	}
	if got := broken.engineCmd(); got != "docker" {
		t.Errorf("engineCmd fallback = %q, want docker", got)
	}
}

func TestMockCompose(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "GET" && p == "/containers/json":
			jsonOK(w, mockContainerList)
		case m == "GET" && strings.HasSuffix(p, "/json"):
			jsonOK(w, mockContainerInspect)
		case m == "POST" && strings.HasSuffix(p, "/start"),
			m == "POST" && strings.HasSuffix(p, "/stop"),
			m == "POST" && strings.HasSuffix(p, "/restart"),
			m == "POST" && strings.HasSuffix(p, "/pause"),
			m == "POST" && strings.HasSuffix(p, "/unpause"):
			w.WriteHeader(http.StatusNoContent)
		case m == "DELETE" && strings.HasPrefix(p, "/containers/"):
			w.WriteHeader(http.StatusNoContent)
		case m == "GET" && strings.HasSuffix(p, "/logs"):
			muxLogs(w, "compose log line")
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	projects, err := b.ListComposeProjects()
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects = %d (%v), want 1 (unlabeled containers skipped)", len(projects), err)
	}
	if projects[0].Project != "web" || projects[0].WorkingDir != "/srv/web" {
		t.Errorf("project = %+v", projects[0])
	}

	ctrs, err := b.ListComposeContainers("/srv/web")
	if err != nil || len(ctrs) != 2 {
		t.Errorf("compose containers = %d / %v", len(ctrs), err)
	}

	res, err := b.InspectComposeProject("/srv/web")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	for _, want := range []string{"project: web", "image: nginx:1.25", "0.0.0.0:8080->80/tcp", "pgdata"} {
		if !strings.Contains(res.RawYAML, want) {
			t.Errorf("inspect should contain %q:\n%s", want, res.RawYAML)
		}
	}

	for name, op := range map[string]func(string) error{
		"start":   b.ComposeStart,
		"stop":    b.ComposeStop,
		"restart": b.ComposeRestart,
		"pause":   b.ComposePause,
		"unpause": b.ComposeUnpause,
		"remove":  b.ComposeRemove,
	} {
		if err := op("/srv/web"); err != nil {
			t.Errorf("compose %s: %v", name, err)
		}
	}

	ch, stop, err := b.ComposeLogs("/srv/web", LogOptions{})
	lines := collect(t, ch, stop, err)
	if len(lines) == 0 {
		t.Error("compose logs should stream at least one line")
	}
}
