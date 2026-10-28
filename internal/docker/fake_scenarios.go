package docker

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
)

// FakeScenario is a demo host with one realistic problem, for evaluating the
// agent skills on -demo-scenario without a live engine.
type FakeScenario struct {
	Name string
	// Skill is the agent skill the scenario exercises.
	Skill string
	// Summary says what is broken, for `d9c -demo-scenario` help and evals.
	Summary string
	// MaxTier is the write ceiling the scenario's fake data admits ("" = the
	// usual policy, i.e. read): writes there only touch the fake.
	MaxTier string
	build   func(now time.Time) *FakeBackend
}

// fakeScenarios is the scenario catalog, one per agent skill.
var fakeScenarios = []FakeScenario{
	{Name: "default", Skill: "d9c", Summary: "the regular demo data: three containers, three compose projects",
		build: func(time.Time) *FakeBackend { return NewFakeBackend() }},
	{Name: "oom", Skill: "d9c-troubleshoot", Summary: "worker is OOM-killed (exit 137) under a 256 MiB limit and keeps restarting",
		build: scenarioOOM},
	{Name: "restart-loop", Skill: "d9c-logs", Summary: "api restarts in a loop because its database container is down",
		build: scenarioRestartLoop},
	{Name: "staging", Skill: "d9c-lifecycle", Summary: "staging and production containers side by side, told apart by the env label",
		MaxTier: "destructive", build: scenarioStaging},
	{Name: "unhealthy-compose", Skill: "d9c-compose", Summary: "compose project shop deploys but shop-api stays unhealthy (pending migration)",
		MaxTier: "destructive", build: scenarioUnhealthyCompose},
	{Name: "stale-images", Skill: "d9c-images", Summary: "old application images and dangling layers no container uses",
		MaxTier: "destructive", build: scenarioStaleImages},
	{Name: "disk-full", Skill: "d9c-cleanup", Summary: "disk nearly full: 12 GB of unused images, 9 GB of build cache, an orphaned volume",
		MaxTier: "destructive", build: scenarioDiskFull},
	{Name: "fleet", Skill: "d9c-fleet", Summary: "a host near its memory limit with an unhealthy service",
		build: scenarioFleet},
	{Name: "insecure", Skill: "d9c-security-audit", Summary: "a privileged admin panel with the Docker socket, public ports, latest tags and env secrets",
		build: scenarioInsecure},
	{Name: "isolated-network", Skill: "d9c-net-storage", Summary: "frontend and backend sit on different networks, so frontend cannot resolve backend",
		build: scenarioIsolatedNetwork},
}

// FakeScenarios lists the demo scenarios.
func FakeScenarios() []FakeScenario { return slices.Clone(fakeScenarios) }

// LookupFakeScenario finds a scenario by name.
func LookupFakeScenario(name string) (FakeScenario, bool) {
	i := slices.IndexFunc(fakeScenarios, func(s FakeScenario) bool { return s.Name == name })
	if i < 0 {
		return FakeScenario{}, false
	}
	return fakeScenarios[i], true
}

// FakeScenarioNames lists the scenario names, for usage messages.
func FakeScenarioNames() string {
	names := make([]string, len(fakeScenarios))
	for i, s := range fakeScenarios {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

// NewFakeScenario returns a fresh demo backend for the named scenario.
func NewFakeScenario(name string) (*FakeBackend, error) {
	s, ok := LookupFakeScenario(name)
	if !ok {
		return nil, fmt.Errorf("unknown demo scenario %q (known: %s)", name, FakeScenarioNames())
	}
	return s.build(time.Now()), nil
}

const mib = 1024 * 1024

// ago formats t-d as RFC3339 in UTC, like the engine's timestamps.
func ago(now time.Time, d time.Duration) string { return now.Add(-d).UTC().Format(time.RFC3339) }

// logLines prefixes each message with a timestamp, the first one span ago and
// the rest evenly after it.
func logLines(now time.Time, span time.Duration, msgs ...string) []string {
	out := make([]string, len(msgs))
	step := span / time.Duration(max(len(msgs), 1))
	for i, m := range msgs {
		out[i] = ago(now, span-time.Duration(i)*step) + " " + m
	}
	return out
}

// detail builds a container's engine description in the Docker SDK's shape,
// so the fake serves the same inspect layout as a real Docker host.
type detail struct {
	id, name, image, user string
	status                string // the State.Status word
	exitCode, restarts    int
	oom                   bool
	errMsg                string
	health                *types.Health
	started, finished     string
	env                   []string
	labels                map[string]string
	memory, nanoCPUs      int64
	restartPolicy         container.RestartPolicyMode
	privileged            bool
	capAdd                []string
	mounts                []types.MountPoint
	binds                 []string
	ports                 nat.PortMap
	networkMode           string
	networks              []string
	healthcheck           *container.HealthConfig
}

func (d detail) json() types.ContainerJSON {
	nets := map[string]*dockernetwork.EndpointSettings{}
	for i, n := range d.networks {
		nets[n] = &dockernetwork.EndpointSettings{NetworkID: n, IPAddress: fmt.Sprintf("172.%d.0.%d", 18+i, 2+len(d.name)%200)}
	}
	mode := d.networkMode
	if mode == "" && len(d.networks) > 0 {
		mode = d.networks[0]
	}
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID: d.id, Name: "/" + d.name, Image: "sha256:" + d.id, RestartCount: d.restarts,
			Created: d.started,
			State: &types.ContainerState{
				Status: d.status, Running: d.status == "running", Restarting: d.status == "restarting",
				OOMKilled: d.oom, ExitCode: d.exitCode, Error: d.errMsg, StartedAt: d.started, FinishedAt: d.finished,
				Health: d.health,
			},
			HostConfig: &container.HostConfig{
				Binds: d.binds, NetworkMode: container.NetworkMode(mode), PortBindings: d.ports,
				RestartPolicy: container.RestartPolicy{Name: d.restartPolicy},
				Privileged:    d.privileged, CapAdd: d.capAdd,
				Resources: container.Resources{Memory: d.memory, NanoCPUs: d.nanoCPUs},
			},
		},
		Mounts: d.mounts,
		Config: &container.Config{
			Hostname: d.id, User: d.user, Env: d.env, Image: d.image, Labels: d.labels, Healthcheck: d.healthcheck,
		},
		NetworkSettings: &types.NetworkSettings{Networks: nets},
	}
}

// ctr is the list entry matching a detail.
func (d detail) ctr(now time.Time, status, ports, health string) Container {
	state := d.status
	created, _ := time.Parse(time.RFC3339, d.started)
	if created.IsZero() {
		created = now
	}
	return Container{ID: d.id, Name: d.name, Image: d.image, Status: status, State: state, Health: health,
		Ports: ports, Created: created, Labels: d.labels, Networks: d.networks}
}

// scenarioBackend builds a backend from container details plus the extras.
func scenarioBackend(ctrs []Container, details []detail) *FakeBackend {
	f := &FakeBackend{
		Containers:      ctrs,
		Images:          []Image{},
		Networks:        []Network{{ID: "f0a1b2c3d4e5", Name: "bridge", Driver: "bridge", Scope: "local", Subnet: "172.17.0.0/16"}},
		Volumes:         []Volume{},
		Composes:        []ComposeProject{},
		ComposeFiles:    map[string]string{},
		Details:         map[string]types.ContainerJSON{},
		LogsByContainer: map[string][]string{},
		Stats:           map[string]ContainerStats{},
		EventLines:      []string{},
	}
	for _, d := range details {
		f.Details[d.id] = d.json()
	}
	return f
}

func healthLog(now time.Time, status string, streak int, code int, output string, n int) *types.Health {
	h := &types.Health{Status: status, FailingStreak: streak}
	for i := n; i > 0; i-- {
		at := now.Add(-time.Duration(i) * 30 * time.Second)
		h.Log = append(h.Log, &types.HealthcheckResult{Start: at, End: at.Add(time.Second), ExitCode: code, Output: output})
	}
	return h
}

func scenarioOOM(now time.Time) *FakeBackend {
	worker := detail{id: "7c3e1f9a2b40", name: "worker", image: "acme/worker:2.3", status: "exited",
		exitCode: 137, oom: true, restarts: 4, started: ago(now, 4*time.Minute), finished: ago(now, 3*time.Minute),
		memory: 256 * mib, restartPolicy: container.RestartPolicyOnFailure,
		env:    []string{"QUEUE_URL=amqp://mq:5672", "BATCH_SIZE=5000", "PATH=/usr/local/bin:/usr/bin:/bin"},
		labels: map[string]string{"env": "prod", "app": "worker"}, networks: []string{"backend"}}
	mq := detail{id: "52d0aa61c7e3", name: "mq", image: "rabbitmq:3.13", status: "running", started: ago(now, 26*time.Hour),
		memory: 512 * mib, restartPolicy: container.RestartPolicyUnlessStopped, labels: map[string]string{"env": "prod"}, networks: []string{"backend"}}
	f := scenarioBackend([]Container{
		worker.ctr(now, "Exited (137) 3 minutes ago", "", ""),
		mq.ctr(now, "Up 26 hours", "5672/tcp", ""),
	}, []detail{worker, mq})
	f.Networks = append(f.Networks, Network{ID: "b4c4e2d1a9f0", Name: "backend", Driver: "bridge", Scope: "local", Subnet: "172.18.0.0/16"})
	f.Images = []Image{{ID: "e19a3c5d7b21", Tags: "acme/worker:2.3", Size: "212 MB", Created: now.Add(-50 * time.Hour)},
		{ID: "a7f3b9c1d2e4", Tags: "rabbitmq:3.13", Size: "254 MB", Created: now.Add(-900 * time.Hour)}}
	f.LogsByContainer[worker.id] = logLines(now, 4*time.Minute,
		"INFO  worker 2.3 starting, batch size 5000",
		"INFO  connected to amqp://mq:5672",
		"INFO  processing batch 1 (5000 messages), heap 96 MiB",
		"INFO  processing batch 2 (5000 messages), heap 171 MiB",
		"WARN  processing batch 3 (5000 messages), heap 238 MiB — close to the container limit",
		"INFO  processing batch 4 (5000 messages), heap 251 MiB")
	f.LogsByContainer[mq.id] = logLines(now, time.Hour, "INFO  accepting AMQP connection 172.18.0.7:40112 -> 172.18.0.3:5672")
	f.Stats[mq.id] = ContainerStats{ID: mq.id, CPUPerc: 1.8, MemUsage: 140 * mib, MemLimit: 512 * mib, MemPerc: 27.3}
	f.EventLines = []string{
		ago(now, 9*time.Minute) + " container start worker (local)",
		ago(now, 7*time.Minute) + " container oom worker (local)",
		ago(now, 7*time.Minute) + " container die worker (local)",
		ago(now, 6*time.Minute) + " container start worker (local)",
		ago(now, 3*time.Minute) + " container oom worker (local)",
		ago(now, 3*time.Minute) + " container die worker (local)",
	}
	f.HostInfo = &HostSummary{NCPU: 4, MemTotal: 8192 * mib}
	return f
}

func scenarioRestartLoop(now time.Time) *FakeBackend {
	api := detail{id: "a1b2c3d4e5f6", name: "api", image: "acme/api:4.1", status: "restarting", exitCode: 1, restarts: 37,
		started: ago(now, 5*time.Second), finished: ago(now, 8*time.Second), restartPolicy: container.RestartPolicyAlways,
		env:    []string{"DATABASE_URL=postgres://app@db:5432/app", "DB_PASSWORD=s3cr3t-demo", "PORT=8080"},
		labels: map[string]string{"env": "prod", "app": "api"}, networks: []string{"app-net"}, memory: 512 * mib}
	db := detail{id: "0d9e8f7a6b5c", name: "db", image: "postgres:16", status: "exited", exitCode: 0,
		started: ago(now, 3*time.Hour), finished: ago(now, 42*time.Minute), restartPolicy: container.RestartPolicyUnlessStopped,
		labels: map[string]string{"env": "prod", "app": "db"}, networks: []string{"app-net"},
		mounts: []types.MountPoint{{Type: mount.TypeVolume, Name: "pgdata", Source: "/var/lib/docker/volumes/pgdata/_data", Destination: "/var/lib/postgresql/data", RW: true}}}
	web := detail{id: "9ae942fd8fbc", name: "web", image: "nginx:1.25", status: "running", started: ago(now, 3*time.Hour),
		labels: map[string]string{"env": "prod", "app": "web"}, networks: []string{"app-net"}}
	f := scenarioBackend([]Container{
		api.ctr(now, "Restarting (1) 5 seconds ago", "", ""),
		db.ctr(now, "Exited (0) 42 minutes ago", "", ""),
		web.ctr(now, "Up 3 hours", "0.0.0.0:80->80/tcp", ""),
	}, []detail{api, db, web})
	f.Networks = append(f.Networks, Network{ID: "aabbccddeeff", Name: "app-net", Driver: "bridge", Scope: "local", Subnet: "172.20.0.0/16"})
	f.Volumes = []Volume{{Name: "pgdata", Driver: "local", Mountpoint: "/var/lib/docker/volumes/pgdata/_data", Created: ago(now, 900*time.Hour)}}
	var apiLogs []string
	for i := 0; i < 6; i++ {
		apiLogs = append(apiLogs,
			"INFO  api 4.1 starting on :8080",
			"INFO  connecting to postgres://app@db:5432/app",
			"ERROR dial tcp: lookup db: no such host",
			"FATAL could not connect to the database after 3 attempts, exiting")
	}
	f.LogsByContainer[api.id] = logLines(now, 40*time.Minute, apiLogs...)
	f.LogsByContainer[db.id] = logLines(now, 3*time.Hour,
		"LOG:  database system is ready to accept connections",
		"LOG:  received smart shutdown request",
		"LOG:  database system is shut down")
	f.LogsByContainer[web.id] = logLines(now, 30*time.Minute,
		`172.20.0.1 - - "GET /api/orders HTTP/1.1" 502 157`,
		`[error] connect() failed (111: Connection refused) while connecting to upstream, upstream: "http://172.20.0.5:8080/orders"`,
		`172.20.0.1 - - "GET /api/orders HTTP/1.1" 502 157`)
	f.Stats[web.id] = ContainerStats{ID: web.id, CPUPerc: 0.4, MemUsage: 12 * mib, MemLimit: 8192 * mib, MemPerc: 0.1}
	f.EventLines = []string{
		ago(now, 42*time.Minute) + " container stop db (local)",
		ago(now, 42*time.Minute) + " container die db (local)",
		ago(now, 41*time.Minute) + " container die api (local)",
		ago(now, 41*time.Minute) + " container restart api (local)",
		ago(now, 20*time.Minute) + " container die api (local)",
		ago(now, 20*time.Minute) + " container restart api (local)",
		ago(now, 10*time.Second) + " container die api (local)",
		ago(now, 5*time.Second) + " container restart api (local)",
	}
	return f
}

func scenarioStaging(now time.Time) *FakeBackend {
	mk := func(id, name, image, env, status string) detail {
		return detail{id: id, name: name, image: image, status: status, started: ago(now, 5*time.Hour),
			labels:   map[string]string{"env": env, "app": strings.TrimSuffix(strings.TrimSuffix(name, "-staging"), "-prod")},
			networks: []string{env + "-net"}, restartPolicy: container.RestartPolicyUnlessStopped}
	}
	ds := []detail{
		mk("51a0c0ffee01", "web-staging", "acme/web:5.0-rc1", "staging", "running"),
		mk("51a0c0ffee02", "api-staging", "acme/api:5.0-rc1", "staging", "running"),
		mk("51a0c0ffee03", "worker-staging", "acme/worker:5.0-rc1", "staging", "running"),
		mk("9f0d00000001", "web-prod", "acme/web:4.9", "prod", "running"),
		mk("9f0d00000002", "api-prod", "acme/api:4.9", "prod", "running"),
		mk("9f0d00000003", "worker-prod", "acme/worker:4.9", "prod", "running"),
		mk("51a0c0ffee04", "migrate-staging", "acme/api:5.0-rc1", "staging", "exited"),
	}
	ctrs := make([]Container, len(ds))
	for i, d := range ds {
		status := "Up 5 hours"
		if d.status == "exited" {
			status = "Exited (0) 5 hours ago"
		}
		ctrs[i] = d.ctr(now, status, "", "")
	}
	f := scenarioBackend(ctrs, ds)
	f.Networks = append(f.Networks,
		Network{ID: "57a9e0000001", Name: "staging-net", Driver: "bridge", Scope: "local", Subnet: "172.30.0.0/16"},
		Network{ID: "9f0d0e000001", Name: "prod-net", Driver: "bridge", Scope: "local", Subnet: "172.31.0.0/16"})
	for _, d := range ds {
		f.Stats[d.id] = ContainerStats{ID: d.id, CPUPerc: 1.1, MemUsage: 80 * mib, MemLimit: 8192 * mib, MemPerc: 1}
	}
	return f
}

func scenarioUnhealthyCompose(now time.Time) *FakeBackend {
	lbl := func(svc string) map[string]string {
		return map[string]string{composeProjectLabel: "shop", composeServiceLabel: svc,
			composeWorkdirLabel: "/srv/shop", composeConfigLabel: "/srv/shop/compose.yaml"}
	}
	web := detail{id: "5e0b00000001", name: "shop-web-1", image: "acme/shop-web:2.0", status: "running", started: ago(now, 6*time.Minute),
		labels: lbl("web"), networks: []string{"shop_default"},
		health: healthLog(now, "healthy", 0, 0, "ok", 3), healthcheck: &container.HealthConfig{Test: []string{"CMD", "wget", "-qO-", "http://localhost/"}}}
	api := detail{id: "5e0b00000002", name: "shop-api-1", image: "acme/shop-api:2.0", status: "running", started: ago(now, 6*time.Minute),
		labels: lbl("api"), networks: []string{"shop_default"}, env: []string{"DATABASE_URL=postgres://shop@db:5432/shop", "STRIPE_SECRET_KEY=sk_live_demo"},
		health:      healthLog(now, "unhealthy", 9, 1, `HTTP 503: {"status":"error","reason":"database schema version 41, expected 42: run migrations"}`, 5),
		healthcheck: &container.HealthConfig{Test: []string{"CMD", "curl", "-fsS", "http://localhost:8080/health"}}}
	db := detail{id: "5e0b00000003", name: "shop-db-1", image: "postgres:16", status: "running", started: ago(now, 30*time.Hour),
		labels: lbl("db"), networks: []string{"shop_default"},
		health: healthLog(now, "healthy", 0, 0, "accepting connections", 3), healthcheck: &container.HealthConfig{Test: []string{"CMD", "pg_isready"}}}
	f := scenarioBackend([]Container{
		web.ctr(now, "Up 6 minutes (healthy)", "0.0.0.0:443->443/tcp", "healthy"),
		api.ctr(now, "Up 6 minutes (unhealthy)", "8080/tcp", "unhealthy"),
		db.ctr(now, "Up 30 hours (healthy)", "5432/tcp", "healthy"),
	}, []detail{web, api, db})
	f.Composes = []ComposeProject{{Project: "shop", Name: "shop", WorkingDir: "/srv/shop", ConfigFiles: "/srv/shop/compose.yaml",
		Status: "running", Command: "docker compose up -d", Running: 3, Total: 3}}
	f.ComposeFiles = map[string]string{"/srv/shop": "services:\n  api:\n    image: acme/shop-api:2.0\n"}
	f.ComposeConfigs = map[string]string{"shop": "name: shop\nservices:\n" +
		"  web:\n    image: acme/shop-web:2.0\n    ports:\n      - 443:443\n" +
		"  api:\n    image: acme/shop-api:2.0\n    environment:\n      DATABASE_URL: postgres://shop@db:5432/shop\n      STRIPE_SECRET_KEY: sk_live_demo\n" +
		"    healthcheck:\n      test: [CMD, curl, -fsS, http://localhost:8080/health]\n" +
		"  db:\n    image: postgres:16\n    volumes:\n      - shopdata:/var/lib/postgresql/data\n" +
		"volumes:\n  shopdata: {}\n"}
	f.Networks = append(f.Networks, Network{ID: "5e0b0e000001", Name: "shop_default", Driver: "bridge", Scope: "local", Subnet: "172.25.0.0/16"})
	f.Volumes = []Volume{{Name: "shop_shopdata", Driver: "local", Mountpoint: "/var/lib/docker/volumes/shop_shopdata/_data", Created: ago(now, 2000*time.Hour)}}
	f.Images = []Image{
		{ID: "c0de00000001", Tags: "acme/shop-web:2.0", Size: "61 MB", Created: now.Add(-2 * time.Hour)},
		{ID: "c0de00000002", Tags: "acme/shop-api:2.0", Size: "188 MB", Created: now.Add(-2 * time.Hour)},
		{ID: "c0de00000003", Tags: "postgres:16", Size: "431 MB", Created: now.Add(-700 * time.Hour)},
	}
	f.LogsByContainer[web.id] = logLines(now, 6*time.Minute, "INFO  web 2.0 serving on :443")
	f.LogsByContainer[api.id] = logLines(now, 6*time.Minute,
		"INFO  shop-api 2.0 starting",
		"INFO  connected to postgres://shop@db:5432/shop",
		"ERROR schema version 41, expected 42 — refusing to serve until migrations run (shop-api migrate)",
		"WARN  health check failed: database schema out of date")
	f.LogsByContainer[db.id] = logLines(now, 6*time.Minute, "LOG:  database system is ready to accept connections")
	f.EventLines = []string{
		ago(now, 6*time.Minute) + " container create shop-api-1 (local)",
		ago(now, 6*time.Minute) + " container start shop-api-1 (local)",
		ago(now, 4*time.Minute) + " container health_status: unhealthy shop-api-1 (local)",
	}
	return f
}

func scenarioStaleImages(now time.Time) *FakeBackend {
	web := detail{id: "0b5e00000001", name: "web", image: "acme/web:3.2", status: "running", started: ago(now, 72*time.Hour), networks: []string{"bridge"}}
	job := detail{id: "0b5e00000002", name: "nightly-report", image: "acme/report:1.4", status: "exited", started: ago(now, 10*time.Hour), networks: []string{"bridge"}}
	f := scenarioBackend([]Container{
		web.ctr(now, "Up 3 days", "0.0.0.0:80->8080/tcp", ""),
		job.ctr(now, "Exited (0) 9 hours ago", "", ""),
	}, []detail{web, job})
	day := 24 * time.Hour
	f.Images = []Image{
		{ID: "3a2b00000032", Tags: "acme/web:3.2", Size: "148 MB", Created: now.Add(-4 * day)},
		{ID: "3a2b00000031", Tags: "acme/web:3.1", Size: "146 MB", Created: now.Add(-30 * day)},
		{ID: "3a2b00000030", Tags: "acme/web:3.0", Size: "151 MB", Created: now.Add(-75 * day)},
		{ID: "4c0d00000014", Tags: "acme/report:1.4", Size: "402 MB", Created: now.Add(-12 * day)},
		{ID: "4c0d00000013", Tags: "acme/report:1.3", Size: "398 MB", Created: now.Add(-60 * day)},
		{ID: "5d1e000000aa", Tags: "<none>:<none>", Size: "1.2 GB", Created: now.Add(-20 * day)},
		{ID: "5d1e000000bb", Tags: "<none>:<none>", Size: "640 MB", Created: now.Add(-41 * day)},
		{ID: "6e2f00000001", Tags: "golang:1.22", Size: "814 MB", Created: now.Add(-200 * day)},
	}
	return f
}

func scenarioDiskFull(now time.Time) *FakeBackend {
	f := scenarioStaleImages(now)
	f.Volumes = []Volume{
		{Name: "webdata", Driver: "local", Mountpoint: "/var/lib/docker/volumes/webdata/_data", Created: ago(now, 2000*time.Hour)},
		{Name: "old_reports_cache", Driver: "local", Mountpoint: "/var/lib/docker/volumes/old_reports_cache/_data", Created: ago(now, 4000*time.Hour)},
	}
	web := f.Details["0b5e00000001"]
	web.Mounts = []types.MountPoint{{Type: mount.TypeVolume, Name: "webdata", Source: "/var/lib/docker/volumes/webdata/_data", Destination: "/data", RW: true}}
	f.Details["0b5e00000001"] = web
	f.DFReport = "TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE\n" +
		"Images          8         2         19.2GB    12.4GB (64%)\n" +
		"Containers      2         1         310MB     22MB (7%)\n" +
		"Local Volumes   2         1         6.8GB     4.1GB (60%)\n" +
		"Build Cache     214       0         9.3GB     9.3GB\n"
	f.HostInfo = &HostSummary{NCPU: 2, MemTotal: 4096 * mib}
	return f
}

func scenarioFleet(now time.Time) *FakeBackend {
	mk := func(id, name, image string, memMiB uint64, cpu float64) (detail, ContainerStats) {
		d := detail{id: id, name: name, image: image, status: "running", started: ago(now, 50*time.Hour), networks: []string{"bridge"},
			memory: int64(memMiB+512) * mib}
		return d, ContainerStats{ID: id, CPUPerc: cpu, MemUsage: memMiB * mib, MemLimit: (memMiB + 512) * mib,
			MemPerc: float64(memMiB) / float64(memMiB+512) * 100}
	}
	search, s1 := mk("f1ee70000001", "search", "elasticsearch:8.15", 5600, 180)
	app, s2 := mk("f1ee70000002", "app", "acme/app:7.2", 1500, 35)
	cache, s3 := mk("f1ee70000003", "cache", "redis:7.4", 600, 4)
	report := detail{id: "f1ee70000004", name: "reports", image: "acme/reports:1.0", status: "running", started: ago(now, 3*time.Hour),
		networks: []string{"bridge"}, health: healthLog(now, "unhealthy", 12, 1, "timeout after 5s", 4)}
	f := scenarioBackend([]Container{
		search.ctr(now, "Up 2 days", "9200/tcp", ""),
		app.ctr(now, "Up 2 days", "0.0.0.0:443->8443/tcp", ""),
		cache.ctr(now, "Up 2 days", "6379/tcp", ""),
		report.ctr(now, "Up 3 hours (unhealthy)", "", "unhealthy"),
	}, []detail{search, app, cache, report})
	f.Stats = map[string]ContainerStats{search.id: s1, app.id: s2, cache.id: s3,
		report.id: {ID: report.id, CPUPerc: 0.2, MemUsage: 90 * mib, MemLimit: 8192 * mib, MemPerc: 1.1}}
	f.HostInfo = &HostSummary{NCPU: 4, MemTotal: 8192 * mib}
	return f
}

func scenarioInsecure(now time.Time) *FakeBackend {
	admin := detail{id: "ad0000000001", name: "admin-panel", image: "portainer/portainer-ce:latest", status: "running", user: "",
		started: ago(now, 20*24*time.Hour), privileged: true, networks: []string{"bridge"},
		env:    []string{"ADMIN_PASSWORD=hunter2-demo", "TZ=UTC"},
		mounts: []types.MountPoint{{Type: mount.TypeBind, Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock", RW: true}},
		binds:  []string{"/var/run/docker.sock:/var/run/docker.sock"},
		ports:  nat.PortMap{"9000/tcp": {{HostIP: "0.0.0.0", HostPort: "9000"}}}}
	redis := detail{id: "ad0000000002", name: "redis", image: "redis:latest", status: "running", user: "",
		started: ago(now, 20*24*time.Hour), networks: []string{"bridge"},
		ports: nat.PortMap{"6379/tcp": {{HostIP: "0.0.0.0", HostPort: "6379"}}}}
	backup := detail{id: "ad0000000003", name: "backup", image: "acme/backup:1.2", status: "running", user: "0",
		started: ago(now, 5*24*time.Hour), networks: []string{"bridge"}, capAdd: []string{"SYS_ADMIN"},
		mounts: []types.MountPoint{{Type: mount.TypeBind, Source: "/", Destination: "/host", RW: true}},
		binds:  []string{"/:/host"}, env: []string{"S3_SECRET_KEY=demo-s3-secret", "S3_BUCKET=backups"}}
	app := detail{id: "ad0000000004", name: "app", image: "acme/app:3.4.1", status: "running", user: "10001",
		started: ago(now, 2*24*time.Hour), networks: []string{"bridge"}, memory: 512 * mib, nanoCPUs: 1e9,
		ports: nat.PortMap{"8080/tcp": {{HostIP: "127.0.0.1", HostPort: "8080"}}}}
	f := scenarioBackend([]Container{
		admin.ctr(now, "Up 3 weeks", "0.0.0.0:9000->9000/tcp", ""),
		redis.ctr(now, "Up 3 weeks", "0.0.0.0:6379->6379/tcp", ""),
		backup.ctr(now, "Up 5 days", "", ""),
		app.ctr(now, "Up 2 days", "127.0.0.1:8080->8080/tcp", ""),
	}, []detail{admin, redis, backup, app})
	f.Images = []Image{
		{ID: "e0a000000001", Tags: "portainer/portainer-ce:latest", Size: "294 MB", Created: now.Add(-40 * 24 * time.Hour)},
		{ID: "e0a000000002", Tags: "redis:latest", Size: "117 MB", Created: now.Add(-60 * 24 * time.Hour)},
		{ID: "e0a000000003", Tags: "acme/backup:1.2", Size: "88 MB", Created: now.Add(-9 * 24 * time.Hour)},
		{ID: "e0a000000004", Tags: "acme/app:3.4.1", Size: "120 MB", Created: now.Add(-3 * 24 * time.Hour)},
	}
	return f
}

func scenarioIsolatedNetwork(now time.Time) *FakeBackend {
	front := detail{id: "fe0000000001", name: "frontend", image: "acme/frontend:2.1", status: "running", started: ago(now, 2*time.Hour),
		networks: []string{"web-net"}, env: []string{"API_URL=http://backend:8080"}}
	back := detail{id: "be0000000001", name: "backend", image: "acme/backend:2.1", status: "running", started: ago(now, 2*time.Hour),
		networks: []string{"api-net"}, mounts: []types.MountPoint{{Type: mount.TypeVolume, Name: "uploads", Source: "/var/lib/docker/volumes/uploads/_data", Destination: "/srv/uploads", RW: true}}}
	db := detail{id: "db0000000001", name: "postgres", image: "postgres:16", status: "running", started: ago(now, 2*time.Hour),
		networks: []string{"api-net"}, mounts: []types.MountPoint{{Type: mount.TypeVolume, Name: "pgdata", Source: "/var/lib/docker/volumes/pgdata/_data", Destination: "/var/lib/postgresql/data", RW: true}}}
	f := scenarioBackend([]Container{
		front.ctr(now, "Up 2 hours", "0.0.0.0:80->3000/tcp", ""),
		back.ctr(now, "Up 2 hours", "8080/tcp", ""),
		db.ctr(now, "Up 2 hours", "5432/tcp", ""),
	}, []detail{front, back, db})
	f.Networks = append(f.Networks,
		Network{ID: "0e7000000001", Name: "web-net", Driver: "bridge", Scope: "local", Subnet: "172.21.0.0/16"},
		Network{ID: "0e7000000002", Name: "api-net", Driver: "bridge", Scope: "local", Subnet: "172.22.0.0/16"})
	f.NetworkDetails = map[string]dockernetwork.Inspect{
		"0e7000000001": {Name: "web-net", ID: "0e7000000001", Driver: "bridge", Scope: "local",
			IPAM:       dockernetwork.IPAM{Config: []dockernetwork.IPAMConfig{{Subnet: "172.21.0.0/16", Gateway: "172.21.0.1"}}},
			Containers: map[string]dockernetwork.EndpointResource{front.id: {Name: "frontend", IPv4Address: "172.21.0.2/16"}}},
		"0e7000000002": {Name: "api-net", ID: "0e7000000002", Driver: "bridge", Scope: "local", Internal: true,
			IPAM: dockernetwork.IPAM{Config: []dockernetwork.IPAMConfig{{Subnet: "172.22.0.0/16", Gateway: "172.22.0.1"}}},
			Containers: map[string]dockernetwork.EndpointResource{
				back.id: {Name: "backend", IPv4Address: "172.22.0.2/16"}, db.id: {Name: "postgres", IPv4Address: "172.22.0.3/16"}}},
	}
	f.Volumes = []Volume{
		{Name: "uploads", Driver: "local", Mountpoint: "/var/lib/docker/volumes/uploads/_data", Created: ago(now, 300*time.Hour)},
		{Name: "pgdata", Driver: "local", Mountpoint: "/var/lib/docker/volumes/pgdata/_data", Created: ago(now, 300*time.Hour)},
		{Name: "legacy_assets", Driver: "local", Mountpoint: "/var/lib/docker/volumes/legacy_assets/_data", Created: ago(now, 3000*time.Hour)},
	}
	f.LogsByContainer[front.id] = logLines(now, 10*time.Minute,
		"INFO  frontend 2.1 listening on 0.0.0.0:3000",
		"ERROR GET /api/products: getaddrinfo ENOTFOUND backend",
		"ERROR GET /api/cart: getaddrinfo ENOTFOUND backend")
	f.LogsByContainer[back.id] = logLines(now, 2*time.Hour, "INFO  backend 2.1 listening on 0.0.0.0:8080", "INFO  connected to postgres:5432")
	f.LogsByContainer[db.id] = logLines(now, 2*time.Hour, "LOG:  database system is ready to accept connections")
	return f
}
