// Package forza decodes the Forza Horizon 5/6 "Data Out" telemetry packet.
//
// FH6 inherits FH5's packet layout. We only decode the few dash fields the
// collector needs for lap detection and metadata; everything else stays as
// opaque bytes for the server to store.
package forza

import (
	"encoding/binary"
	"errors"
	"math"
)

// PacketSize is the byte length of the "sled + dash" Data Out format.
// The shorter 232-byte "sled" format is rejected.
const PacketSize = 324

// Dash is a typed view of the fields we need from a 324-byte packet.
type Dash struct {
	// IsRaceOn is true whenever the player is driving and the game is not
	// paused — INCLUDING free roam. Verified against real FH6 captures: free
	// roam streams IsRaceOn=1 with DistanceTraveledM pinned at exactly 0.
	// Use InRace to tell an actual race/event from free roam.
	IsRaceOn          bool
	TimestampMS       uint32 // game-side ms tick; consecutive frames are ~16 ms apart
	LapNumber         uint16
	LastLapTimeSec    float32 // seconds, with milliseconds preserved
	CurrentLapSec     float32 // seconds into the current lap; resets to 0 at each line crossing
	CurrentSpeedMps   float32 // m/s
	DistanceTraveledM float32 // meters since race start; exactly 0 in free roam, negative during the start countdown
	CarID             int32
	CarClass          uint8
	PerformanceIndex  int32
	DrivetrainType    int32
}

// InRace reports whether the frame was captured during an actual race/event.
// IsRaceOn alone is not enough: FH6 keeps it 1 in free roam. The reliable
// discriminator (verified empirically) is DistanceTraveledM, which is exactly
// 0.0 for entire free-roam stints but nonzero during a race — negative while
// rolling up to the start line during the countdown, then climbing.
func (d Dash) InRace() bool {
	return d.IsRaceOn && d.DistanceTraveledM != 0
}

// ErrWrongSize is returned when the input is not exactly PacketSize bytes.
var ErrWrongSize = errors.New("forza: packet must be 324 bytes")

// Field offsets for the Forza Horizon "Data Out" layout. Offsets are
// little-endian.
//
// IMPORTANT: Forza *Horizon* (FH4/FH5/FH6) inserts 12 extra bytes after the
// 232-byte "sled", so every field in the *dash* section sits 12 bytes later
// than the Forza *Motorsport* layout that most online references document.
// Fields inside the sled (offset < 232 — the car-info block) are NOT shifted.
//
// Verified empirically against real FH6 captures: the Motorsport offsets
// decode to garbage (e.g. speed at 244 reads ±6000 m/s, lap number at 300
// reads values like 16573), while the +12 dash offsets below decode to sane
// values (speed 0–81 m/s, lap numbers 0,1,2, lap times ~50 s).
//
// Reference (Motorsport offsets; add 12 for the dash fields):
// https://support.forzamotorsport.net/hc/en-us/articles/21742934024211
const (
	offIsRaceOn    = 0 // int32 (sled; 0 in menus/paused, 1 while driving — including free roam)
	offTimestampMS = 4 // uint32 (sled)

	// Car-info block: lives in the sled, so these are NOT +12 shifted.
	offCarID            = 212 // int32 (CarOrdinal)
	offCarClass         = 216 // uint8  (low byte of the int32 CarClass)
	offPerformanceIndex = 220 // int32
	offDrivetrainType   = 224 // int32

	// Dash fields: Motorsport offset + 12.
	offCurrentSpeedMps  = 256 // float32 (Motorsport 244 + 12)
	offDistanceTraveled = 292 // float32 (Motorsport 280 + 12)
	offLastLapTime      = 300 // float32 (Motorsport 288 + 12)
	offCurrentLapTime   = 304 // float32 (Motorsport 292 + 12)
	offLapNumber        = 312 // uint16  (Motorsport 300 + 12)
)

// Decode reads the dash fields from a 324-byte packet.
func Decode(packet []byte) (Dash, error) {
	if len(packet) != PacketSize {
		return Dash{}, ErrWrongSize
	}

	return Dash{
		IsRaceOn:          binary.LittleEndian.Uint32(packet[offIsRaceOn:]) != 0,
		TimestampMS:       binary.LittleEndian.Uint32(packet[offTimestampMS:]),
		LapNumber:         binary.LittleEndian.Uint16(packet[offLapNumber:]),
		LastLapTimeSec:    math.Float32frombits(binary.LittleEndian.Uint32(packet[offLastLapTime:])),
		CurrentLapSec:     math.Float32frombits(binary.LittleEndian.Uint32(packet[offCurrentLapTime:])),
		CurrentSpeedMps:   math.Float32frombits(binary.LittleEndian.Uint32(packet[offCurrentSpeedMps:])),
		DistanceTraveledM: math.Float32frombits(binary.LittleEndian.Uint32(packet[offDistanceTraveled:])),
		CarID:             int32(binary.LittleEndian.Uint32(packet[offCarID:])),
		CarClass:          packet[offCarClass],
		PerformanceIndex:  int32(binary.LittleEndian.Uint32(packet[offPerformanceIndex:])),
		DrivetrainType:    int32(binary.LittleEndian.Uint32(packet[offDrivetrainType:])),
	}, nil
}

// EncodeForTest builds a 324-byte packet with the given dash fields. Test-only
// helper; the production code path never builds packets.
func EncodeForTest(d Dash) []byte {
	packet := make([]byte, PacketSize)

	if d.IsRaceOn {
		binary.LittleEndian.PutUint32(packet[offIsRaceOn:], 1)
	}
	binary.LittleEndian.PutUint32(packet[offTimestampMS:], d.TimestampMS)
	binary.LittleEndian.PutUint16(packet[offLapNumber:], d.LapNumber)
	binary.LittleEndian.PutUint32(packet[offLastLapTime:], math.Float32bits(d.LastLapTimeSec))
	binary.LittleEndian.PutUint32(packet[offCurrentLapTime:], math.Float32bits(d.CurrentLapSec))
	binary.LittleEndian.PutUint32(packet[offCurrentSpeedMps:], math.Float32bits(d.CurrentSpeedMps))
	binary.LittleEndian.PutUint32(packet[offDistanceTraveled:], math.Float32bits(d.DistanceTraveledM))
	binary.LittleEndian.PutUint32(packet[offCarID:], uint32(d.CarID))
	packet[offCarClass] = d.CarClass
	binary.LittleEndian.PutUint32(packet[offPerformanceIndex:], uint32(d.PerformanceIndex))
	binary.LittleEndian.PutUint32(packet[offDrivetrainType:], uint32(d.DrivetrainType))

	return packet
}
