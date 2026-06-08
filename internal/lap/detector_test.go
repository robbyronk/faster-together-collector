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

func TestDetectorLapJumpNoTimeDoesNotEmit(t *testing.T) {
	d := NewDetector()
	d.OnPacket(frame(0, 50, 0))

	// Large forward jump with no lastLapTime (quick restart preset) — treat as reset.
	if c := d.OnPacket(frame(5, 50, 0)); c != nil {
		t.Fatalf("lap jump without lastLapTime should not emit, got %+v", c)
	}
}

func TestDetectorFinalLapEmitsOnLargeJump(t *testing.T) {
	d := NewDetector(WithClock(func() time.Time { return time.Unix(1700000000, 0).UTC() }))
	d.OnPacket(frame(0, 60, 0))
	d.OnPacket(frame(1, 70, 44.2))
	d.OnPacket(frame(2, 65, 54.8))

	// Race-finish: Forza bumps lap from 2 to 9 and populates LastLapTimeSec.
	completed := d.OnPacket(frame(9, 0, 39.7))
	if completed == nil {
		t.Fatal("expected emit on race-finish large lap jump with lastLapTime set")
	}
	if completed.LapTimeSec != 39.7 {
		t.Errorf("LapTimeSec=%v, want 39.7", completed.LapTimeSec)
	}
}

func TestDetectorSprintRaceEmitsOnRaceEnd(t *testing.T) {
	d := NewDetector(WithClock(func() time.Time { return time.Unix(1700000000, 0).UTC() }))
	d.OnPacket(raceFrame(true, 0, 40, 0))
	d.OnPacket(raceFrame(true, 0, 65, 0))
	d.OnPacket(raceFrame(true, 0, 70, 0))

	// Sprint ends: IsRaceOn drops to 0, lap never incremented, car was moving.
	completed := d.OnPacket(raceFrame(false, 0, 0, 0))
	if completed == nil {
		t.Fatal("expected sprint emit on race end")
	}
	if completed.LapTimeSec != 0 {
		t.Errorf("LapTimeSec=%v, want 0 (sprint time unavailable from telemetry)", completed.LapTimeSec)
	}
	if completed.MaxSpeedMps != 70 {
		t.Errorf("MaxSpeedMps=%v, want 70", completed.MaxSpeedMps)
	}
	if n := gunzipLen(t, completed.Blob) / forza.PacketSize; n != 3 {
		t.Errorf("blob has %d frames, want 3", n)
	}
}

func TestDetectorSprintLobbyNoMovementNotEmitted(t *testing.T) {
	d := NewDetector()
	d.OnPacket(raceFrame(true, 0, 0, 0))
	d.OnPacket(raceFrame(true, 0, 0, 0))

	// Race ends but car never moved (pre-race lobby noise) — must not emit.
	if c := d.OnPacket(raceFrame(false, 0, 0, 0)); c != nil {
		t.Fatalf("lobby session with zero speed should not emit, got %+v", c)
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

func TestDetectorMultiLapDNFDiscardsInFlightLap(t *testing.T) {
	d := NewDetector()
	// Complete lap 0 normally, now on lap 1.
	d.OnPacket(raceFrame(true, 0, 50, 0))
	d.OnPacket(raceFrame(true, 1, 60, 70.0))
	d.OnPacket(raceFrame(true, 1, 60, 70.0))

	// DNF mid-lap-1 (IsRaceOn 1→0): the in-flight lap is incomplete.
	if c := d.OnPacket(raceFrame(false, 1, 0, 0)); c != nil {
		t.Fatalf("race-end mid-circuit should not emit, got %+v", c)
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
