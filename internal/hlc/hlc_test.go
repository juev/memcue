package hlc

import (
	"errors"
	"math/rand/v2"
	"sort"
	"testing"
	"time"
)

const devA, devB = "00000000000000aa", "00000000000000bb"

func fixed(ms int64) func() time.Time { return func() time.Time { return time.UnixMilli(ms) } }

func TestStringOrderIsStampOrder(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var stamps []Stamp
	for range 500 {
		stamps = append(stamps, Stamp{Millis: r.Int64N(1 << 41), Counter: r.IntN(maxCounter + 1),
			Device: []string{devA, devB}[r.IntN(2)]})
	}
	stamps = append(stamps, Stamp{})
	for _, a := range stamps {
		for _, b := range stamps[:50] {
			byString := 0
			if a.String() < b.String() {
				byString = -1
			} else if a.String() > b.String() {
				byString = 1
			}
			if got := a.Compare(b); got != byString {
				t.Fatalf("%v vs %v: Compare %d, strings %d", a, b, got, byString)
			}
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, s := range []Stamp{{}, {Millis: 1, Device: devA}, {Millis: 1762790400000, Counter: 99999, Device: devB}} {
		got, err := Parse(s.String())
		if err != nil || got != s {
			t.Fatalf("Parse(%q) = %v, %v; want %v", s.String(), got, err, s)
		}
	}
	for _, bad := range []string{"x", "1762790400000-00001", "1762790400000-00001-ZZ00000000000000",
		"176279040000-00001-00000000000000aa", "1762790400000-0001-00000000000000aa", "-762790400000-00001-00000000000000aa"} {
		if _, err := Parse(bad); !errors.Is(err, ErrStamp) {
			t.Errorf("Parse(%q) error %v, want ErrStamp", bad, err)
		}
	}
}

func TestNextIsMonotonicWhenTheWallClockStepsBack(t *testing.T) {
	wall := int64(5000)
	c, err := New(devA, Stamp{}, func() time.Time { return time.UnixMilli(wall) })
	if err != nil {
		t.Fatal(err)
	}
	prev := c.Next()
	for _, w := range []int64{5000, 4000, 4000, 6000, 1000} {
		wall = w
		next := c.Next()
		if next.Compare(prev) <= 0 {
			t.Fatalf("wall %d: %v not after %v", w, next, prev)
		}
		prev = next
	}
	if prev.Millis != 6000 || prev.Counter != 1 {
		t.Fatalf("after a step back to 1000 the clock stays on 6000: got %v", prev)
	}
}

func TestCounterOverflowMovesToTheNextMillisecond(t *testing.T) {
	c, _ := New(devA, Stamp{Millis: 7, Counter: maxCounter, Device: devA}, fixed(7))
	if got := c.Next(); got != (Stamp{Millis: 8, Device: devA}) {
		t.Fatalf("got %v", got)
	}
}

func TestObservedStampOrdersLaterChanges(t *testing.T) {
	far := Stamp{Millis: 9000, Counter: 3, Device: devB}
	c, _ := New(devA, Stamp{}, fixed(1000))
	c.Observe(far)
	if got := c.Next(); got.Compare(far) <= 0 || got.Device != devA {
		t.Fatalf("next %v must follow observed %v and carry its own device", got, far)
	}
	c.Observe(Stamp{Millis: 10, Device: devB})
	if got := c.Last(); got.Millis != 9000 {
		t.Fatalf("an older stamp must not move the clock back: %v", got)
	}
}

func TestNewRejectsAMalformedDevice(t *testing.T) {
	for _, d := range []string{"", "00000000000000a", "00000000000000AA", "00000000000000ag"} {
		if _, err := New(d, Stamp{}, nil); !errors.Is(err, ErrDevice) {
			t.Errorf("New(%q) error %v", d, err)
		}
	}
	d, err := NewDevice()
	if err != nil || ValidDevice(d) != nil {
		t.Fatalf("NewDevice = %q, %v", d, err)
	}
}

func TestAhead(t *testing.T) {
	now := time.UnixMilli(10_000)
	if got := Ahead(Stamp{Millis: 70_000, Device: devA}, now); got != time.Minute {
		t.Fatalf("got %v", got)
	}
	if got := Ahead(Stamp{Millis: 1, Device: devA}, now); got != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestZeroSortsFirst(t *testing.T) {
	s := []string{Stamp{Millis: 1, Device: devA}.String(), ""}
	sort.Strings(s)
	if s[0] != "" {
		t.Fatal("zero stamp must sort first")
	}
}
