package forza

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestDecodeRoundTrip(t *testing.T) {
	original := Dash{
		IsRaceOn:         true,
		LapNumber:        7,
		LastLapTimeSec:   95.412,
		CurrentSpeedMps:  78.4,
		CarID:            1234,
		CarClass:         5,
		PerformanceIndex: 825,
		DrivetrainType:   2,
	}

	packet := EncodeForTest(original)
	if len(packet) != PacketSize {
		t.Fatalf("EncodeForTest produced %d bytes, want %d", len(packet), PacketSize)
	}

	decoded, err := Decode(packet)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded != original {
		t.Fatalf("round-trip mismatch:\n got: %+v\nwant: %+v", decoded, original)
	}
}

// TestDecodeAbsoluteOffsets pins the absolute byte positions Decode reads
// from. Unlike TestDecodeRoundTrip — which uses EncodeForTest and so shares
// (and would mask) any offset mistake — this writes the FH6 dash layout by
// hand at literal offsets. It exists specifically to catch a regression to
// the Forza *Motorsport* offsets (speed@244, lap@300), which decode real FH6
// telemetry as garbage.
func TestDecodeAbsoluteOffsets(t *testing.T) {
	packet := make([]byte, PacketSize)

	binary.LittleEndian.PutUint32(packet[0:], 1) // IsRaceOn (sled, offset 0)

	// Car-info block — in the sled, not +12 shifted.
	binary.LittleEndian.PutUint32(packet[212:], uint32(int32(1234))) // CarOrdinal
	binary.LittleEndian.PutUint32(packet[216:], 5)                   // CarClass (int32; we read its low byte)
	binary.LittleEndian.PutUint32(packet[220:], uint32(int32(825)))  // PerformanceIndex
	binary.LittleEndian.PutUint32(packet[224:], uint32(int32(2)))    // DrivetrainType

	// Dash fields — Motorsport offset + 12.
	binary.LittleEndian.PutUint32(packet[256:], math.Float32bits(78.4)) // speed
	binary.LittleEndian.PutUint32(packet[300:], math.Float32bits(95.4)) // last lap time
	binary.LittleEndian.PutUint16(packet[312:], 7)                      // lap number

	got, err := Decode(packet)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	want := Dash{
		IsRaceOn:         true,
		LapNumber:        7,
		LastLapTimeSec:   95.4,
		CurrentSpeedMps:  78.4,
		CarID:            1234,
		CarClass:         5,
		PerformanceIndex: 825,
		DrivetrainType:   2,
	}
	if got != want {
		t.Fatalf("Decode read wrong offsets:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestDecodeRejectsWrongSize(t *testing.T) {
	for _, size := range []int{0, 232, 323, 325, 500} {
		_, err := Decode(make([]byte, size))
		if err != ErrWrongSize {
			t.Errorf("size %d: got err=%v, want ErrWrongSize", size, err)
		}
	}
}
