package caretaker

import (
	"context"
	"encoding/binary"
	"os"
	"time"
)

// watchButton reads the power button (logind leaves it alone on the box:
// logind.conf.d/soundstorm-button.conf) and counts its presses into runs.
func (u *Updater) watchButton(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			u.log.Error("power button watch stopped", "panic", r)
		}
	}()
	devices, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return
	}
	path := powerButton(string(devices))
	if path == "" {
		u.log.Warn("no power button found to watch")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		u.log.Warn("could not watch the power button", "err", err)
		return
	}
	go func() { <-ctx.Done(); f.Close() }()
	press := make(chan struct{}, 16)
	go countRuns(ctx, press, 2*time.Second, func(n int) { u.Pressed(ctx, n) })
	// struct input_event on 64-bit Linux: a timeval (16 bytes), then type,
	// code (16 bits each) and value (32). A key going down is EV_KEY (1),
	// KEY_POWER (116), value 1.
	buf := make([]byte, 24)
	for {
		if _, err := readFull(f, buf); err != nil {
			return
		}
		typ := binary.LittleEndian.Uint16(buf[16:])
		code := binary.LittleEndian.Uint16(buf[18:])
		value := int32(binary.LittleEndian.Uint32(buf[20:]))
		if typ == 1 && code == 116 && value == 1 {
			select {
			case press <- struct{}{}:
			default:
			}
		}
	}
}

func readFull(f *os.File, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := f.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
