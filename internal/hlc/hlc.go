// Package hlc is the hybrid logical clock that stamps every field change of a synced fact base (#714).
//
// A stamp is wall milliseconds, a counter and the device that made it. Its string form has a fixed width, so strings
// compare as stamps do, and the empty string sorts before every stamp. A clock never issues a stamp at or below one it
// has issued or observed, so a device whose wall clock steps back still stamps its later changes later. The method is
// the one of juev/lists (docs/specs/sync.md, S1 there).
package hlc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DeviceLen is the length of a device id: 16 lowercase hex digits.
const DeviceLen = 16

const (
	millisWidth  = 13 // milliseconds since the Unix epoch fit 13 digits until the year 2286
	counterWidth = 5
	maxCounter   = 99999
)

// ErrStamp: a string that is not a stamp.
var ErrStamp = errors.New("not an HLC stamp")

// ErrDevice: a device id that is not 16 lowercase hex digits.
var ErrDevice = errors.New("device id must be 16 lowercase hex digits")

// Stamp is one point of the clock. The zero Stamp is before every issued one.
type Stamp struct {
	Millis  int64
	Counter int
	Device  string
}

// IsZero reports whether s is the zero Stamp.
func (s Stamp) IsZero() bool { return s == Stamp{} }

// String is the fixed-width form "MMMMMMMMMMMMM-CCCCC-DEVICE"; the zero Stamp is "".
func (s Stamp) String() string {
	if s.IsZero() {
		return ""
	}
	return fmt.Sprintf("%0*d-%0*d-%s", millisWidth, s.Millis, counterWidth, s.Counter, s.Device)
}

// Compare orders stamps by time, then counter, then device: -1, 0 or +1.
func (s Stamp) Compare(o Stamp) int {
	switch {
	case s.Millis != o.Millis:
		return cmp(s.Millis, o.Millis)
	case s.Counter != o.Counter:
		return cmp(int64(s.Counter), int64(o.Counter))
	default:
		return strings.Compare(s.Device, o.Device)
	}
}

func cmp(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// Parse reads the String form back; "" is the zero Stamp.
func Parse(v string) (Stamp, error) {
	if v == "" {
		return Stamp{}, nil
	}
	parts := strings.Split(v, "-")
	if len(parts) != 3 || len(parts[0]) != millisWidth || len(parts[1]) != counterWidth {
		return Stamp{}, fmt.Errorf("%w: %q", ErrStamp, v)
	}
	ms, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || ms < 0 {
		return Stamp{}, fmt.Errorf("%w: %q", ErrStamp, v)
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 {
		return Stamp{}, fmt.Errorf("%w: %q", ErrStamp, v)
	}
	if err := ValidDevice(parts[2]); err != nil {
		return Stamp{}, fmt.Errorf("%w: %q", ErrStamp, v)
	}
	return Stamp{Millis: ms, Counter: n, Device: parts[2]}, nil
}

// ValidDevice checks a device id.
func ValidDevice(d string) error {
	if len(d) != DeviceLen {
		return ErrDevice
	}
	for _, c := range d {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ErrDevice
		}
	}
	return nil
}

// NewDevice returns a random device id.
func NewDevice() (string, error) {
	b := make([]byte, DeviceLen/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Clock issues stamps for one device. It is not safe for concurrent use; a base serializes it by its write
// transaction and persists Last between processes.
type Clock struct {
	device string
	last   Stamp
	now    func() time.Time
}

// New returns a clock of device that continues after last (the zero Stamp for a new device). now may be nil.
func New(device string, last Stamp, now func() time.Time) (*Clock, error) {
	if err := ValidDevice(device); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Clock{device: device, last: last, now: now}, nil
}

// Device is the clock's device id.
func (c *Clock) Device() string { return c.device }

// Last is the greatest stamp the clock has issued or observed.
func (c *Clock) Last() Stamp { return c.last }

// Next issues a stamp after every stamp the clock has issued or observed.
func (c *Clock) Next() Stamp {
	wall := c.now().UnixMilli()
	next := Stamp{Millis: wall, Device: c.device}
	if wall <= c.last.Millis {
		next.Millis, next.Counter = c.last.Millis, c.last.Counter+1
		if next.Counter > maxCounter {
			next.Millis, next.Counter = next.Millis+1, 0
		}
	}
	c.last = next
	return next
}

// Observe moves the clock past a stamp received from another device, so the next stamp it issues is later.
func (c *Clock) Observe(s Stamp) {
	if s.Compare(c.last) > 0 {
		c.last = Stamp{Millis: s.Millis, Counter: s.Counter, Device: c.device}
	}
}

// Ahead reports how far s is ahead of the wall clock now, or 0 when it is not.
func Ahead(s Stamp, now time.Time) time.Duration {
	d := time.Duration(s.Millis-now.UnixMilli()) * time.Millisecond
	if d < 0 {
		return 0
	}
	return d
}
