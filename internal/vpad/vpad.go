// Package vpad is the backend-owned headless input keystone for xemu: a
// virtual Xbox-360 gamepad created through Linux uinput. xemu binds it as a
// real SDL controller by GUID (auto_bind=false + an explicit port binding in
// the instance TOML), and this package emits button / D-pad / stick / trigger
// events into it — the input half the scraper never had.
//
// This is the Go port of scripts/runtime/padpool.py. It exists because QMP
// `sendkey` does NOT drive xemu's Xbox controller (ADR-0003): xemu's
// keyboard→controller map is the SDL-scancode path, while `sendkey` feeds the
// emulated keyboard through qemu_input — a channel the game never reads. A
// virtual pad enters through the same SDL controller path a physical pad (or
// the screen) uses, so it actually moves the game (proven live — see cmd/vpad
// and cmd/inputpoc).
//
// The device descriptor is byte-for-byte what padpool.py uses (name
// "Microsoft X-Box 360 pad", VID 0x045E / PID 0x028E / USB bus, per-port USB
// version) so the SDL GUID xemu computes is identical and predictable — see
// GUID / PredictGUID.
//
// Callers name inputs with the SAME logical labels as the xemu.SendKey
// primitive (a/b/x/y, Up/Down/Left/Right, Return=Start, BackSpace=Back,
// 1–5, ESDF/IJKL sticks, w/o triggers) so both input surfaces share one
// vocabulary; see SupportedLabels.
package vpad

import (
	"fmt"
	"sort"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	uinputPath = "/dev/uinput"

	// Device identity — must match padpool.py exactly so the derived SDL GUID
	// is identical (the name feeds the GUID's CRC16 field).
	deviceName   = "Microsoft X-Box 360 pad"
	usbVendor    = 0x045E
	usbProduct   = 0x028E
	usbBus       = 0x03   // BUS_USB
	baseVersion  = 0x0600 // default USB-version base; port N → base+N
	stickFull    = 30000  // stick deflection magnitude (matches padpool FULL)
	triggerFull  = 255
	uinputMaxAbs = 64 // ABS_CNT

	// Default press duration for Tap/Chord — mirrors xemu.SendKey's default.
	defaultHold = 100 * time.Millisecond
)

// uinput ioctl request codes (legacy uinput_user_dev API). Computed as
// _IOW('U', nr, int) for the SET bits and _IO('U', nr) for create/destroy.
const (
	uiSetEvbit   = 0x40045564 // _IOW('U',100,int)
	uiSetKeybit  = 0x40045565 // _IOW('U',101,int)
	uiSetAbsbit  = 0x40045567 // _IOW('U',103,int)
	uiDevCreate  = 0x5501     // _IO('U',1)
	uiDevDestroy = 0x5502     // _IO('U',2)
)

// evdev event types + codes (from linux/input-event-codes.h; x/sys/unix does
// not export the BTN_*/ABS_HAT* set, so we define the ones we emit).
const (
	evSyn     = 0x00
	evKey     = 0x01
	evAbs     = 0x03
	synReport = 0x00

	btnA      = 0x130
	btnB      = 0x131
	btnX      = 0x133
	btnY      = 0x134
	btnTL     = 0x136
	btnTR     = 0x137
	btnSelect = 0x13a
	btnStart  = 0x13b
	btnMode   = 0x13c
	btnThumbL = 0x13d
	btnThumbR = 0x13e

	absX     = 0x00
	absY     = 0x01
	absZ     = 0x02
	absRX    = 0x03
	absRY    = 0x04
	absRZ    = 0x05
	absHat0X = 0x10
	absHat0Y = 0x11
)

// control describes how one logical label maps onto a uinput event: which
// event type + code, and the value written when the control is "active"
// (buttons 1, D-pad ±1, sticks ±stickFull, triggers triggerFull). Neutral is
// always 0.
type control struct {
	evType uint16
	code   uint16
	active int32
}

// labelToControl mirrors the frontend VNC map / xemu.SendKey label set, mapped
// onto Xbox-pad controls. Control_L and r (xemu's keyboard-only reset chord)
// have no pad equivalent and are intentionally absent.
var labelToControl = map[string]control{
	// Face buttons.
	"a": {evKey, btnA, 1},
	"b": {evKey, btnB, 1},
	"x": {evKey, btnX, 1},
	"y": {evKey, btnY, 1},
	// D-pad (hat).
	"Up":    {evAbs, absHat0Y, -1},
	"Down":  {evAbs, absHat0Y, 1},
	"Left":  {evAbs, absHat0X, -1},
	"Right": {evAbs, absHat0X, 1},
	// Start / Back / Guide.
	"Return":    {evKey, btnStart, 1},
	"BackSpace": {evKey, btnSelect, 1},
	"5":         {evKey, btnMode, 1},
	// Bumpers, L3, R3.
	"1": {evKey, btnTL, 1},
	"2": {evKey, btnTR, 1},
	"3": {evKey, btnThumbL, 1},
	"4": {evKey, btnThumbR, 1},
	// Triggers (LT / RT).
	"w": {evAbs, absZ, triggerFull},
	"o": {evAbs, absRZ, triggerFull},
	// Left stick (ESDF): e=fwd(-Y) s=left(-X) d=back(+Y) f=right(+X).
	"e": {evAbs, absY, -stickFull},
	"s": {evAbs, absX, -stickFull},
	"d": {evAbs, absY, stickFull},
	"f": {evAbs, absX, stickFull},
	// Right stick (IJKL): i=up(-RY) j=left(-RX) k=down(+RY) l=right(+RX).
	"i": {evAbs, absRY, -stickFull},
	"j": {evAbs, absRX, -stickFull},
	"k": {evAbs, absRY, stickFull},
	"l": {evAbs, absRX, stickFull},
}

// absAxes are the analog axes the device advertises, with their ranges.
var absAxes = []struct {
	code                 uint16
	min, max, fuzz, flat int32
}{
	{absX, -32768, 32767, 16, 128},
	{absY, -32768, 32767, 16, 128},
	{absRX, -32768, 32767, 16, 128},
	{absRY, -32768, 32767, 16, 128},
	{absZ, 0, 255, 0, 0},
	{absRZ, 0, 255, 0, 0},
	{absHat0X, -1, 1, 0, 0},
	{absHat0Y, -1, 1, 0, 0},
}

var buttonCodes = []uint16{
	btnA, btnB, btnX, btnY,
	btnTL, btnTR, btnSelect, btnStart,
	btnMode, btnThumbL, btnThumbR,
}

// Pad is one open virtual gamepad. Safe for concurrent use.
type Pad struct {
	mu      sync.Mutex
	fd      int
	name    string
	version int
	guid    string
}

// Options configures a Pad. Zero value yields a default Xbox-360 pad at USB
// version base+1 (0x0601).
type Options struct {
	Name    string // uinput device name; default "Microsoft X-Box 360 pad"
	Version int    // USB version; default 0x0601. Determines the SDL GUID.
}

// New creates and registers a uinput virtual Xbox-360 pad. The kernel keeps the
// device alive until Close (or the process exits). Requires write access to
// /dev/uinput.
func New(opts Options) (*Pad, error) {
	name := opts.Name
	if name == "" {
		name = deviceName
	}
	version := opts.Version
	if version == 0 {
		version = baseVersion + 1
	}

	fd, err := unix.Open(uinputPath, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s (need write access / uinput module): %w", uinputPath, err)
	}
	p := &Pad{fd: fd, name: name, version: version, guid: PredictGUID(name, version)}

	if err := p.configure(); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	// Centre every axis so xemu reads a neutral pad from the first frame.
	if err := p.Neutral(); err != nil {
		_ = p.Close()
		return nil, err
	}
	return p, nil
}

// configure declares capabilities and creates the device (legacy uinput API).
func (p *Pad) configure() error {
	for _, ev := range []int{evKey, evAbs, evSyn} {
		if err := unix.IoctlSetInt(p.fd, uiSetEvbit, ev); err != nil {
			return fmt.Errorf("UI_SET_EVBIT %d: %w", ev, err)
		}
	}
	for _, code := range buttonCodes {
		if err := unix.IoctlSetInt(p.fd, uiSetKeybit, int(code)); err != nil {
			return fmt.Errorf("UI_SET_KEYBIT 0x%x: %w", code, err)
		}
	}
	for _, ax := range absAxes {
		if err := unix.IoctlSetInt(p.fd, uiSetAbsbit, int(ax.code)); err != nil {
			return fmt.Errorf("UI_SET_ABSBIT 0x%x: %w", ax.code, err)
		}
	}

	var dev uinputUserDev
	copy(dev.Name[:len(dev.Name)-1], p.name)
	dev.ID = inputID{Bustype: usbBus, Vendor: usbVendor, Product: usbProduct, Version: uint16(p.version)}
	for _, ax := range absAxes {
		dev.Absmin[ax.code] = ax.min
		dev.Absmax[ax.code] = ax.max
		dev.Absfuzz[ax.code] = ax.fuzz
		dev.Absflat[ax.code] = ax.flat
	}
	if _, err := unix.Write(p.fd, asBytes(unsafe.Pointer(&dev), unsafe.Sizeof(dev))); err != nil {
		return fmt.Errorf("write uinput_user_dev: %w", err)
	}
	if err := unix.IoctlSetInt(p.fd, uiDevCreate, 0); err != nil {
		return fmt.Errorf("UI_DEV_CREATE: %w", err)
	}
	return nil
}

// GUID returns the SDL controller GUID xemu will see for this pad — the value
// to put in the instance TOML's port binding.
func (p *Pad) GUID() string { return p.guid }

// Name returns the uinput device name.
func (p *Pad) Name() string { return p.name }

// Close destroys the device and releases the fd.
func (p *Pad) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fd < 0 {
		return nil
	}
	_ = unix.IoctlSetInt(p.fd, uiDevDestroy, 0)
	err := unix.Close(p.fd)
	p.fd = -1
	return err
}

// emit writes one input_event followed by a SYN_REPORT so the kernel dispatches
// the change immediately. Caller holds p.mu.
func (p *Pad) emitLocked(evType, code uint16, value int32) error {
	if err := p.writeEvent(evType, code, value); err != nil {
		return err
	}
	return p.writeEvent(evSyn, synReport, 0)
}

func (p *Pad) writeEvent(evType, code uint16, value int32) error {
	ev := inputEvent{Type: evType, Code: code, Value: value}
	_, err := unix.Write(p.fd, asBytes(unsafe.Pointer(&ev), unsafe.Sizeof(ev)))
	return err
}

// set drives one label to active or neutral (no auto-release). Useful for the
// runner to hold state across frames.
func (p *Pad) set(label string, active bool) error {
	c, ok := labelToControl[label]
	if !ok {
		return fmt.Errorf("no pad control for label %q (see vpad.SupportedLabels)", label)
	}
	v := int32(0)
	if active {
		v = c.active
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.emitLocked(c.evType, c.code, v)
}

// Set holds (active=true) or releases (active=false) one labelled control.
func (p *Pad) Set(label string, active bool) error { return p.set(label, active) }

// Tap presses and releases one control using the default hold time.
func (p *Pad) Tap(label string) error { return p.TapHold(label, defaultHold) }

// TapHold presses one control, holds it for d, then releases. A stick label
// (e/s/d/f/i/j/k/l) held for d is a full-deflection stick push — e.g.
// TapHold("e", time.Second) is "left stick forward for 1s", the pad analogue
// of padpool's `move 1`.
func (p *Pad) TapHold(label string, d time.Duration) error {
	if _, ok := labelToControl[label]; !ok {
		return fmt.Errorf("no pad control for label %q (see vpad.SupportedLabels)", label)
	}
	if err := p.set(label, true); err != nil {
		return err
	}
	time.Sleep(d)
	return p.set(label, false)
}

// Chord presses several controls together for the default hold, then releases
// them in reverse order (e.g. Chord("Return") or a two-button combo).
func (p *Pad) Chord(labels ...string) error { return p.ChordHold(defaultHold, labels...) }

// ChordHold presses labels together, holds for d, then releases in reverse.
func (p *Pad) ChordHold(d time.Duration, labels ...string) error {
	if len(labels) == 0 {
		return fmt.Errorf("no labels")
	}
	for _, l := range labels {
		if _, ok := labelToControl[l]; !ok {
			return fmt.Errorf("no pad control for label %q", l)
		}
	}
	for _, l := range labels {
		if err := p.set(l, true); err != nil {
			return err
		}
	}
	time.Sleep(d)
	var firstErr error
	for i := len(labels) - 1; i >= 0; i-- {
		if err := p.set(labels[i], false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// clampStick keeps an axis value inside the advertised int16 range.
func clampStick(v int) int32 {
	if v > 32767 {
		v = 32767
	}
	if v < -32768 {
		v = -32768
	}
	return int32(v)
}

// LeftStick / RightStick set both axes of a stick to an absolute position
// (analog control for the state-aware runner). x>0 = right, y>0 = down (the
// raw evdev convention; game-forward is y<0).
func (p *Pad) LeftStick(x, y int) error  { return p.stick(absX, absY, x, y) }
func (p *Pad) RightStick(x, y int) error { return p.stick(absRX, absRY, x, y) }

func (p *Pad) stick(axX, axY uint16, x, y int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.writeEvent(evAbs, axX, clampStick(x)); err != nil {
		return err
	}
	return p.emitLocked(evAbs, axY, clampStick(y))
}

// Neutral recentres every stick / trigger / hat and releases every button.
func (p *Pad) Neutral() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ax := range absAxes {
		if err := p.writeEvent(evAbs, ax.code, 0); err != nil {
			return err
		}
	}
	for _, code := range buttonCodes {
		if err := p.writeEvent(evKey, code, 0); err != nil {
			return err
		}
	}
	return p.writeEvent(evSyn, synReport, 0)
}

// SupportedLabels returns the sorted set of labels Tap/Chord/Set accept.
func SupportedLabels() []string {
	out := make([]string, 0, len(labelToControl))
	for k := range labelToControl {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PredictGUID returns the SDL controller GUID xemu computes for a uinput device
// with the given name and USB version (bus USB, VID 0x045E, PID 0x028E). The
// name's CRC16 is embedded in the GUID; NameCRC16 computes it.
func PredictGUID(name string, version int) string {
	crc := NameCRC16(name)
	verLE := fmt.Sprintf("%02x%02x", version&0xFF, (version>>8)&0xFF)
	crcLE := fmt.Sprintf("%02x%02x", crc&0xFF, (crc>>8)&0xFF)
	// 0300 <crcLE> <vidLE 0000> <pidLE 0000> <verLE 0000>
	return "0300" + crcLE + "5e040000" + "8e020000" + verLE + "0000"
}

// NameCRC16 is SDL's CRC-16 over a device name (poly 0xA001, init 0), the value
// embedded in the evdev joystick GUID.
func NameCRC16(name string) uint16 {
	var crc uint16
	for _, bb := range []byte(name) {
		crc ^= uint16(bb)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// ---- uinput / evdev wire structs -------------------------------------------

type inputID struct {
	Bustype uint16
	Vendor  uint16
	Product uint16
	Version uint16
}

// uinputUserDev matches the C struct written to /dev/uinput before UI_DEV_CREATE.
type uinputUserDev struct {
	Name         [80]byte
	ID           inputID
	FFEffectsMax uint32
	Absmax       [uinputMaxAbs]int32
	Absmin       [uinputMaxAbs]int32
	Absfuzz      [uinputMaxAbs]int32
	Absflat      [uinputMaxAbs]int32
}

// inputEvent matches struct input_event on 64-bit Linux (16-byte timeval).
type inputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

func asBytes(p unsafe.Pointer, n uintptr) []byte {
	return unsafe.Slice((*byte)(p), n)
}
