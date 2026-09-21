package leaguescraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/xemu-cartographer/xc-scraper/capture"
	"github.com/xemu-cartographer/xc-scraper/daemon"
	"github.com/xemu-cartographer/xc-scraper/offsets"
	"github.com/xemu-cartographer/xc-scraper/wire"
	"github.com/xemu-cartographer/xemu-cartographer/internal/podman"
)

// Config push (DESIGN-STEP8 §10): in wire mode the league is the daemon's
// source of truth for capture policies, the dummy-gamertag allowlist, the
// imported offset sets and the per-instance meta (overlay path, VNC URL,
// host-drive flag, offset set, neutral host). The ConfigPusher builds that
// daemon.Document from PocketBase + the podman Manager and PUTs it through
// the control API: in full on every upstream connect and, debounced, on PB
// record changes; per instance at podman Create.

const (
	// DefaultPushDebounce coalesces PB hook bursts into one full push.
	DefaultPushDebounce = 250 * time.Millisecond
	// DefaultPushTimeout bounds one push (the Ctl's own 2 s cap applies too).
	DefaultPushTimeout = 5 * time.Second

	// DummyGamertagsCollection / ContainersCollection / OffsetSetsCollection /
	// ISOsCollection are the PB collections the document is built from.
	DummyGamertagsCollection = "dummy_gamertags"
	ContainersCollection     = "containers"
	OffsetSetsCollection     = "offset_sets"
	ISOsCollection           = "isos"

	// PBSinkPrefix marks a capture-policy sink the league serves itself
	// (eventwriter.go); such rows are pushed with the sink stripped.
	PBSinkPrefix = "pb:"
)

// PushHookCollections are the PB collections whose record changes trigger a
// debounced full push (§10).
var PushHookCollections = []string{
	CapturePoliciesCollection, DummyGamertagsCollection, ContainersCollection, OffsetSetsCollection,
}

// IsPBSink reports whether a capture-policy sink spec is league-side.
func IsPBSink(spec string) bool { return strings.HasPrefix(spec, PBSinkPrefix) }

// ControlClient is the daemon control surface the pusher needs
// (*xcclient.Ctl satisfies it).
type ControlClient interface {
	PutConfig(ctx context.Context, doc daemon.Document) (daemon.Document, error)
	PutInstance(ctx context.Context, name string, cfg daemon.InstanceConfig) (daemon.Document, error)
}

// PodSource is the podman view the pusher needs (*podman.Manager satisfies
// it). nil ⇒ the document carries no instances section.
type PodSource interface {
	List() ([]podman.ContainerInfo, error)
	OverlayPath(name string) (string, bool)
}

// PushOptions configures NewConfigPusher.
type PushOptions struct {
	App  core.App
	Ctl  ControlClient
	Pods PodSource
	// HostDriveMarker is HOSTRUNNER_DRIVE_MARKER ("" ⇒ daemon.DefaultHostDriveMarker).
	HostDriveMarker string
	Debounce        time.Duration
	Timeout         time.Duration
	Logf            func(format string, args ...any)
}

// ConfigPusher builds and ships the daemon configuration document.
type ConfigPusher struct {
	opt PushOptions

	mu     sync.Mutex // guards timer + closed
	timer  *time.Timer
	closed bool

	pushMu sync.Mutex // serialises pushes (build + PUT)

	pushes    atomic.Uint64
	instances atomic.Uint64
	lastErr   atomic.Pointer[error]
}

// NewConfigPusher returns a pusher; nothing runs until Push/Schedule/hooks.
func NewConfigPusher(o PushOptions) *ConfigPusher {
	if o.HostDriveMarker == "" {
		o.HostDriveMarker = daemon.DefaultHostDriveMarker
	}
	if o.Debounce <= 0 {
		o.Debounce = DefaultPushDebounce
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultPushTimeout
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	return &ConfigPusher{opt: o}
}

// Pushes / InstancePushes / LastError expose counters for tests + health.
func (p *ConfigPusher) Pushes() uint64         { return p.pushes.Load() }
func (p *ConfigPusher) InstancePushes() uint64 { return p.instances.Load() }
func (p *ConfigPusher) LastError() error {
	if e := p.lastErr.Load(); e != nil {
		return *e
	}
	return nil
}

func (p *ConfigPusher) setErr(err error) {
	if err == nil {
		p.lastErr.Store(nil)
		return
	}
	p.lastErr.Store(&err)
}

// Document builds the full daemon document from PB + podman. Rows the daemon
// would reject (daemon.ValidatePolicy) are logged and skipped so one bad row
// never blocks the fleet; pb: sinks are stripped (mode/cadence kept, so the
// daemon's hard caps and cadences still apply) and served by the EventWriter.
func (p *ConfigPusher) Document() daemon.Document {
	doc := daemon.Document{Version: 1}
	app := p.opt.App
	if app == nil {
		return doc
	}
	doc.CapturePolicies = p.policies(app)
	doc.DummyGamertags = p.dummyGamertags(app)
	doc.OffsetSets = p.offsetSets(app)
	doc.Instances = p.instanceConfigs(app)
	return doc
}

func (p *ConfigPusher) policies(app core.App) []capture.Policy {
	records, err := app.FindAllRecords(CapturePoliciesCollection)
	if err != nil {
		p.opt.Logf("configpush: load %s: %v", CapturePoliciesCollection, err)
		return nil
	}
	var out []capture.Policy
	for _, pol := range recordsToPolicies(records) {
		if IsPBSink(pol.Sink) {
			pol.Sink = ""
		}
		if err := daemon.ValidatePolicy(pol); err != nil {
			p.opt.Logf("configpush: skip policy %s/%s: %v", pol.Instance, pol.Class, err)
			continue
		}
		out = append(out, pol)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Instance != out[j].Instance {
			return out[i].Instance < out[j].Instance
		}
		return out[i].Class < out[j].Class
	})
	return out
}

func (p *ConfigPusher) dummyGamertags(app core.App) []string {
	rows, err := app.FindAllRecords(DummyGamertagsCollection)
	if err != nil {
		p.opt.Logf("configpush: load %s: %v", DummyGamertagsCollection, err)
		return nil
	}
	var out []string
	for _, r := range rows {
		if tag := strings.TrimSpace(r.GetString("gamertag")); tag != "" {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

func (p *ConfigPusher) offsetSets(app core.App) map[string]json.RawMessage {
	rows, err := app.FindAllRecords(OffsetSetsCollection)
	if err != nil {
		p.opt.Logf("configpush: load %s: %v", OffsetSetsCollection, err)
		return nil
	}
	var out map[string]json.RawMessage
	for _, r := range rows {
		id := r.GetString("set_id")
		if id == "" || r.GetString("file") == "" {
			continue
		}
		raw, err := readRecordFile(app, r, r.GetString("file"))
		if err != nil {
			p.opt.Logf("configpush: offset set %q: %v", id, err)
			continue
		}
		if _, err := offsets.ParseSet(raw); err != nil {
			p.opt.Logf("configpush: skip offset set %q: %v", id, err)
			continue
		}
		if out == nil {
			out = map[string]json.RawMessage{}
		}
		out[id] = raw
	}
	return out
}

// readRecordFile returns the bytes of a record's file field through the
// app's filesystem (the same path offsets.SetDynamicSource used in embedded
// mode).
func readRecordFile(app core.App, rec *core.Record, name string) ([]byte, error) {
	fsys, err := app.NewFilesystem()
	if err != nil {
		return nil, err
	}
	defer fsys.Close()
	r, err := fsys.GetReader(rec.BaseFilesPath() + "/" + name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func (p *ConfigPusher) instanceConfigs(app core.App) map[string]daemon.InstanceConfig {
	if p.opt.Pods == nil {
		return nil
	}
	infos, err := p.opt.Pods.List()
	if err != nil {
		p.opt.Logf("configpush: list containers: %v", err)
		return nil
	}
	neutral := p.neutralHosts(app)
	var out map[string]daemon.InstanceConfig
	for _, info := range infos {
		if err := wire.ValidateInstanceName(info.Name); err != nil {
			p.opt.Logf("configpush: skip container %q: %v", info.Name, err)
			continue
		}
		if out == nil {
			out = map[string]daemon.InstanceConfig{}
		}
		ic := p.instanceConfig(app, info)
		ic.NeutralHost = neutral[info.Name]
		out[info.Name] = ic
	}
	return out
}

// InstanceConfig derives one container's daemon config (§10): overlay path
// when provisioned, the screen websockify URL, the host-drive marker verdict
// and the catalog offset set of its attached ISO. NeutralHost is filled by
// Document (it needs the containers collection).
func (p *ConfigPusher) InstanceConfig(info podman.ContainerInfo) daemon.InstanceConfig {
	ic := p.instanceConfig(p.opt.App, info)
	if p.opt.App != nil {
		ic.NeutralHost = p.neutralHosts(p.opt.App)[info.Name]
	}
	return ic
}

func (p *ConfigPusher) instanceConfig(app core.App, info podman.ContainerInfo) daemon.InstanceConfig {
	drive := strings.Contains(info.Name, p.opt.HostDriveMarker)
	ic := daemon.InstanceConfig{HostDrive: &drive}
	if p.opt.Pods != nil {
		if path, ok := p.opt.Pods.OverlayPath(info.Name); ok {
			ic.OverlayPath = path
		}
	}
	if info.Ports.BrowserWeb != 0 {
		ic.VNCURL = VNCURLForPort(info.Ports.BrowserWeb)
	}
	if app != nil && info.GameISO != "" {
		id := strings.TrimSuffix(filepath.Base(info.GameISO), ".iso")
		if rec, err := app.FindRecordById(ISOsCollection, id); err == nil && rec != nil {
			ic.OffsetSet = rec.GetString("offset_set")
		}
	}
	return ic
}

// VNCURLForPort is the screen websockify URL for a container's BrowserWeb
// port (the host-runner URL resolver's format).
func VNCURLForPort(browserWeb int) string {
	return fmt.Sprintf("ws://127.0.0.1:%d/websockify", browserWeb)
}

func (p *ConfigPusher) neutralHosts(app core.App) map[string]bool {
	rows, err := app.FindAllRecords(ContainersCollection)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, r := range rows {
		if r.GetBool("is_neutral_host") {
			out[r.GetString("name")] = true
		}
	}
	return out
}

// Push builds the document and PUTs it in full (PUT /api/ctl/config, which
// replaces the daemon's control layer — so a container removed here is
// dropped there). Serialised; errors are logged and kept in LastError.
func (p *ConfigPusher) Push() error {
	if p.opt.Ctl == nil {
		return nil
	}
	p.pushMu.Lock()
	defer p.pushMu.Unlock()
	doc := p.Document()
	ctx, cancel := context.WithTimeout(context.Background(), p.opt.Timeout)
	defer cancel()
	_, err := p.opt.Ctl.PutConfig(ctx, doc)
	p.pushes.Add(1)
	p.setErr(err)
	if err != nil {
		p.opt.Logf("configpush: push config: %v", err)
	}
	return err
}

// PushInstance PUTs one container's config (PUT /api/ctl/instances/{name}) —
// the podman Create hook, before xemu boots so the daemon binds the right
// offset set and host URL at attach.
func (p *ConfigPusher) PushInstance(info podman.ContainerInfo) error {
	if p.opt.Ctl == nil {
		return nil
	}
	p.pushMu.Lock()
	defer p.pushMu.Unlock()
	ic := p.InstanceConfig(info)
	ctx, cancel := context.WithTimeout(context.Background(), p.opt.Timeout)
	defer cancel()
	_, err := p.opt.Ctl.PutInstance(ctx, info.Name, ic)
	p.instances.Add(1)
	p.setErr(err)
	if err != nil {
		p.opt.Logf("configpush: push instance %s: %v", info.Name, err)
	}
	return err
}

// Schedule requests a full push after the debounce window; further calls
// inside the window restart it (one PUT per burst).
func (p *ConfigPusher) Schedule() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if p.timer == nil {
		p.timer = time.AfterFunc(p.opt.Debounce, func() { _ = p.Push() })
		return
	}
	p.timer.Reset(p.opt.Debounce)
}

// OnConnect is the xcclient.Client.OnConnect hook: a full push on its own
// goroutine so the stream worker is never blocked on HTTP.
func (p *ConfigPusher) OnConnect() { go func() { _ = p.Push() }() }

// Close cancels a pending debounced push.
func (p *ConfigPusher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
}

// RegisterHooks binds the after-{create,update,delete}-success hooks of
// PushHookCollections to Schedule (the captureprovider.go pattern). Call
// once during OnServe.
func (p *ConfigPusher) RegisterHooks(app core.App) {
	if app == nil {
		return
	}
	fire := func(e *core.RecordEvent) error {
		p.Schedule()
		return e.Next()
	}
	for _, col := range PushHookCollections {
		app.OnRecordAfterCreateSuccess(col).BindFunc(fire)
		app.OnRecordAfterUpdateSuccess(col).BindFunc(fire)
		app.OnRecordAfterDeleteSuccess(col).BindFunc(fire)
	}
}

// PodHooks returns the podman lifecycle hooks: Created ⇒ PushInstance
// (synchronous, so the meta lands before the box is started); Removed ⇒
// Schedule (the full document no longer carries the key).
func (p *ConfigPusher) PodHooks() podman.Hooks {
	return podman.Hooks{
		Created: func(info podman.ContainerInfo) { _ = p.PushInstance(info) },
		Removed: func(string) { p.Schedule() },
	}
}
