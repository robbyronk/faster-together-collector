// Package lap turns a stream of raw 324-byte FH telemetry packets into
// completed-lap events. It owns the lap-detection state machine; the UDP
// listener feeds it frames, the uploader consumes its output.
package lap

import (
	"time"

	"github.com/robbyronk/faster-together-collector/internal/forza"
)

// Completed describes a single finished lap. The blob is the gzipped
// concatenation of every raw 324-byte packet observed during the lap.
type Completed struct {
	LapTimeSec       float32
	CarID            int32
	CarClass         uint8
	PerformanceIndex int32
	DrivetrainType   int32
	MaxSpeedMps      float32
	CompletedAt      time.Time
	Blob             []byte
}

// Detector is the per-session state machine. Not safe for concurrent use;
// drive it from a single goroutine.
type Detector struct {
	now    func() time.Time
	gzip   func([]byte) ([]byte, error)
	state  *state
}

type state struct {
	currentLap       uint16
	buffer           [][]byte
	maxSpeed         float32
	carID            int32
	carClass         uint8
	performanceIndex int32
	drivetrainType   int32
}

// NewDetector returns a detector with default time + gzip. Pass options to
// override (for tests).
func NewDetector(opts ...Option) *Detector {
	d := &Detector{
		now:  time.Now,
		gzip: gzipBytes,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Option configures a Detector.
type Option func(*Detector)

// WithClock overrides the time source (test only).
func WithClock(now func() time.Time) Option {
	return func(d *Detector) { d.now = now }
}

// WithGzip overrides the gzip function (test only).
func WithGzip(g func([]byte) ([]byte, error)) Option {
	return func(d *Detector) { d.gzip = g }
}

// OnPacket consumes one raw 324-byte packet. If this packet causes a lap
// transition, returns the completed lap; otherwise returns nil.
//
// On any error (gzip failure, malformed packet), returns nil — the frame is
// dropped silently from the lap blob, which matches the PRD's "best-effort
// upload" stance.
func (d *Detector) OnPacket(raw []byte) *Completed {
	dash, err := forza.Decode(raw)
	if err != nil {
		return nil
	}

	// Gate on IsRaceOn. Forza's lap counter sits at 0 during free roam *and*
	// throughout the first lap of a race, so without this gate every free-roam
	// frame accumulates under "lap 0" and the eventual flush is an enormous
	// blob. We only buffer frames captured while a race/event is active.
	//
	// On a 1→0 transition (race ended):
	//   - Sprint races (LapNumber never incremented, car actually moved) are
	//     emitted with LapTimeSec=0; Forza never sets LastLapTimeSec for sprints.
	//   - All other in-flight buffers (mid-circuit DNF, post-race cool-down lap)
	//     are dropped. State is cleared so the next race starts fresh.
	if !dash.IsRaceOn {
		var completed *Completed
		if d.state != nil && d.state.currentLap == 0 && d.state.maxSpeed >= 1.0 {
			completed = d.emit(0)
		}
		d.state = nil
		return completed
	}

	if d.state == nil {
		d.state = &state{currentLap: dash.LapNumber}
	}

	s := d.state

	switch {
	case dash.LapNumber == s.currentLap:
		s.append(raw, dash)
		return nil

	case dash.LapNumber == s.currentLap+1:
		completed := d.emit(dash.LastLapTimeSec)
		s.reset(dash, raw)
		return completed

	case dash.LapNumber < s.currentLap:
		// session reset / new race — drop the in-flight buffer
		s.reset(dash, raw)
		return nil

	default:
		// LapNumber jumped forward by more than one. Two causes:
		//   1. Race-finish counter update (e.g. 2→9): Forza bumps LapNumber by
		//      the total lap count when you cross the final finish line, and sets
		//      LastLapTimeSec to the just-completed lap's time. Emit the buffer.
		//   2. Quick restart with laps preset: LastLapTimeSec is 0. Drop.
		if dash.LastLapTimeSec > 0 {
			completed := d.emit(dash.LastLapTimeSec)
			s.reset(dash, raw)
			return completed
		}
		s.reset(dash, raw)
		return nil
	}
}

// Reset abandons any in-flight lap buffer. Called on shutdown so partial
// laps are not emitted (PRD §2.3).
func (d *Detector) Reset() {
	d.state = nil
}

func (d *Detector) emit(lapTimeSec float32) *Completed {
	s := d.state
	concatenated := concat(s.buffer)
	blob, err := d.gzip(concatenated)
	if err != nil {
		return nil
	}

	return &Completed{
		LapTimeSec:       lapTimeSec,
		CarID:            s.carID,
		CarClass:         s.carClass,
		PerformanceIndex: s.performanceIndex,
		DrivetrainType:   s.drivetrainType,
		MaxSpeedMps:      s.maxSpeed,
		CompletedAt:      d.now().UTC(),
		Blob:             blob,
	}
}

func (s *state) append(raw []byte, d forza.Dash) {
	cp := make([]byte, len(raw))
	copy(cp, raw)
	s.buffer = append(s.buffer, cp)
	if d.CurrentSpeedMps > s.maxSpeed {
		s.maxSpeed = d.CurrentSpeedMps
	}
	s.carID = d.CarID
	s.carClass = d.CarClass
	s.performanceIndex = d.PerformanceIndex
	s.drivetrainType = d.DrivetrainType
}

func (s *state) reset(d forza.Dash, firstFrame []byte) {
	s.currentLap = d.LapNumber
	s.buffer = nil
	s.maxSpeed = 0
	s.append(firstFrame, d)
}

func concat(chunks [][]byte) []byte {
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	out := make([]byte, 0, total)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}
