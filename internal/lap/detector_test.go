package lap

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"
	"time"

	"github.com/robbyronk/faster-together-collector/internal/forza"
)

func frame(lap uint16, speed float32, lastLapTime float32) []byte {
	return raceFrame(true, lap, speed, lastLapTime)
}

func raceFrame(racing bool, lap uint16, speed float32, lastLapTime float32) []byte {
	return forza.EncodeForTest(forza.Dash{
		IsRaceOn:         racing,
		LapNumber:        lap,
		LastLapTimeSec:   lastLapTime,
		CurrentSpeedMps:  speed,
		CarID:            1234,
		CarClass:         5,
		PerformanceIndex: 825,
		DrivetrainType:   2,
	})
}

func TestDetectorEmitsOnLapIncrement(t *testing.T) {
	d := NewDetector(WithClock(func() time.Time { return time.Unix(1700000000, 0).UTC() }))

	// Lap 0 frames (out lap)
	for _, spd := range []float32{10, 50, 70} {
		if got := d.OnPacket(frame(0, spd, 0)); got != nil {
			t.Fatalf("unexpected emit during lap 0: %+v", got)
		}
	}

	// Transition to lap 1 → emit lap 0 with lastLapTime=88.4
	completed := d.OnPacket(frame(1, 60, 88.4))
	if completed == nil {
		t.Fatal("expected lap-completed emit on lap transition 0→1")
	}
	if completed.LapTimeSec != 88.4 {
		t.Errorf("LapTimeSec=%v, want 88.4", completed.LapTimeSec)
	}
	if completed.MaxSpeedMps != 70 {
		t.Errorf("MaxSpeedMps=%v, want 70", completed.MaxSpeedMps)
	}
	if completed.CompletedAt.IsZero() {
		t.Error("CompletedAt should be set")
	}
	// blob must gunzip to N * 324 bytes
	r, err := gzip.NewReader(bytes.NewReader(completed.Blob))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(raw)%forza.PacketSize != 0 {
		t.Errorf("gunzipped %d bytes, not a multiple of %d", len(raw), forza.PacketSize)
	}
}

func TestDetectorMultipleLaps(t *testing.T) {
	d := NewDetector()
	for _, lap := range []uint16{0, 0, 1, 1, 2, 2} {
		_ = d.OnPacket(frame(lap, 50, 90.0))
	}

	emits := 0
	d2 := NewDetector()
	d2.OnPacket(frame(0, 50, 0))
	d2.OnPacket(frame(0, 60, 0))
	if c := d2.OnPacket(frame(1, 55, 91.0)); c != nil {
		emits++
	}
	d2.OnPacket(frame(1, 55, 91.0))
	if c := d2.OnPacket(frame(2, 55, 92.0)); c != nil {
		emits++
	}
	if c := d2.OnPacket(frame(3, 55, 93.0)); c != nil {
		emits++
	}

	if emits != 3 {
		t.Errorf("expected 3 lap completions, got %d", emits)
	}
}

func TestDetectorSessionResetDoesNotEmit(t *testing.T) {
	d := NewDetector()
	d.OnPacket(frame(3, 50, 0))
	d.OnPacket(frame(3, 60, 0))

	// Session reset: lap number drops to 0
	if c := d.OnPacket(frame(0, 50, 0)); c != nil {
		t.Fatalf("session reset should not emit a completed lap, got %+v", c)
	}
}

func TestDetectorLapJumpDoesNotEmit(t *testing.T) {
	d := NewDetector()
	d.OnPacket(frame(0, 50, 0))

	// Lap jumps forward by 2 (race restart preset) — treat as reset
	if c := d.OnPacket(frame(5, 50, 0)); c != nil {
		t.Fatalf("lap jump should not emit, got %+v", c)
	}
}

func TestDetectorIgnoresFreeRoamFrames(t *testing.T) {
	d := NewDetector()

	// Free-roam frames (IsRaceOn=false) must never be buffered, even though
	// their lap number is 0 — otherwise they pile up under "lap 0".
	for _, spd := range []float32{10, 50, 200} {
		if c := d.OnPacket(raceFrame(false, 0, spd, 0)); c != nil {
			t.Fatalf("free-roam frame should not emit, got %+v", c)
		}
	}

	// Now a real race starts at lap 0 and completes its first lap.
	d.OnPacket(raceFrame(true, 0, 60, 0))
	d.OnPacket(raceFrame(true, 0, 80, 0))
	completed := d.OnPacket(raceFrame(true, 1, 60, 61.0))
	if completed == nil {
		t.Fatal("expected emit on 0→1 once racing")
	}
	if completed.MaxSpeedMps != 80 {
		t.Errorf("MaxSpeedMps=%v, want 80 — free-roam 200 m/s frame leaked into the lap", completed.MaxSpeedMps)
	}
	// blob must be exactly the two buffered lap-0 race frames (not the free-roam ones)
	if n := gunzipLen(t, completed.Blob) / forza.PacketSize; n != 2 {
		t.Errorf("blob has %d frames, want 2 (free-roam frames must be excluded)", n)
	}
}

func TestDetectorRaceEndDiscardsInFlightLap(t *testing.T) {
	d := NewDetector()
	d.OnPacket(raceFrame(true, 0, 50, 0))
	d.OnPacket(raceFrame(true, 0, 60, 0))

	// Race ends mid-lap (IsRaceOn 1→0): the in-flight lap is incomplete.
	if c := d.OnPacket(raceFrame(false, 0, 0, 0)); c != nil {
		t.Fatalf("race-end frame should not emit, got %+v", c)
	}

	// A fresh race starts; the previous buffer must be gone, so a 0→1 here
	// emits a lap built only from the new race's frames.
	d.OnPacket(raceFrame(true, 0, 55, 0))
	completed := d.OnPacket(raceFrame(true, 1, 55, 70.0))
	if completed == nil {
		t.Fatal("expected emit on new race 0→1")
	}
	if n := gunzipLen(t, completed.Blob) / forza.PacketSize; n != 1 {
		t.Errorf("blob has %d frames, want 1 (stale buffer from the abandoned race must be discarded)", n)
	}
}

func gunzipLen(t *testing.T, blob []byte) int {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return len(raw)
}

func TestDetectorResetDiscardsBuffer(t *testing.T) {
	d := NewDetector()
	d.OnPacket(frame(0, 50, 0))
	d.OnPacket(frame(0, 50, 0))
	d.Reset()

	// First frame after reset starts a fresh state — no emit
	if c := d.OnPacket(frame(0, 50, 0)); c != nil {
		t.Fatalf("post-reset packet should not emit, got %+v", c)
	}
}
