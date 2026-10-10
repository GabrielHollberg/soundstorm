package caretaker

import (
	"context"
	"encoding/binary"
	"os"
	"sync"
	"time"
)

// watchButton reads the power button (logind leaves it alone on the box:
// logind.conf.d/soundstorm-button.conf) and counts its presses into runs.
// Every device that sends the power key is read (powerButtons), and looked
// for again each half minute: one may appear late, and none found at start
// used to mean a button that never worked until the caretaker restarted.
func (u *Updater) watchButton(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			u.log.Error("power button watch stopped", "panic", r)
		}
	}()
	press := make(chan struct{}, 16)
	go countRuns(ctx, press, 2*time.Second, func(n int) { u.Pressed(ctx, n) })
	var (
		mu       sync.Mutex
		watched  = map[string]bool{}
		last     time.Time
		warned   bool
		recorded = func() {
			// One press can arrive on two devices at once (a board that
			// reports it twice): presses closer than this are one.
			mu.Lock()
			defer mu.Unlock()
			// 80ms: one press reported by two devices arrives together, and
			// a quarter second swallowed quick presses of ten.
			if time.Since(last) < 80*time.Millisecond {
				return
			}
			last = time.Now()
			select {
			case press <- struct{}{}:
			default:
			}
		}
	)
	for {
		if devices, err := os.ReadFile("/proc/bus/input/devices"); err == nil {
			found := powerButtons(string(devices))
			if len(found) == 0 && !warned {
				u.log.Warn("no power button found to watch yet")
				warned = true
			}
			for _, path := range found {
				mu.Lock()
				seen := watched[path]
				watched[path] = true
				mu.Unlock()
				if seen {
					continue
				}
				go func(path string) {
					u.readButton(ctx, path, recorded)
					mu.Lock()
					delete(watched, path) // gone: found again if it comes back
					mu.Unlock()
				}(path)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// readButton reads one input device, telling pressed of each power key
// going down, until it fails or ctx ends.
func (u *Updater) readButton(ctx context.Context, path string, pressed func()) {
	f, err := os.Open(path)
	if err != nil {
		u.log.Warn("could not watch the power button", "device", path, "err", err)
		return
	}
	u.log.Info("watching the power button", "device", path)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		f.Close()
	}()
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
		if typ == 1 && code == keyPower && value == 1 {
			pressed()
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
