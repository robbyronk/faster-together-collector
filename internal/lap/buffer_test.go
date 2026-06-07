package lap

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/robbyronk/faster-together-collector/internal/forza"
)

// TestGzipBufferRoundTrip is the spec's lap/buffer test: a buffer of raw
// 324-byte packets concatenates and gzips to something that gunzips back to
// the exact original bytes — and that length is a multiple of the packet size.
func TestGzipBufferRoundTrip(t *testing.T) {
	const n = 5
	buffer := make([][]byte, n)
	for i := range buffer {
		buffer[i] = frame(0, float32(10*i), 0)
		if len(buffer[i]) != forza.PacketSize {
			t.Fatalf("fixture frame %d is %d bytes, want %d", i, len(buffer[i]), forza.PacketSize)
		}
	}

	concatenated := concat(buffer)
	if len(concatenated) != n*forza.PacketSize {
		t.Fatalf("concat produced %d bytes, want %d", len(concatenated), n*forza.PacketSize)
	}

	gz, err := gzipBytes(concatenated)
	if err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}

	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if len(got)%forza.PacketSize != 0 {
		t.Errorf("gunzipped %d bytes, not a multiple of %d", len(got), forza.PacketSize)
	}
	if !bytes.Equal(got, concatenated) {
		t.Errorf("round-trip mismatch: gunzipped %d bytes differ from the original %d", len(got), len(concatenated))
	}
}

// TestGzipEmptyBuffer guards the degenerate case: an empty lap buffer (no
// frames) still gzips and gunzips cleanly to zero bytes, which is trivially a
// multiple of the packet size.
func TestGzipEmptyBuffer(t *testing.T) {
	concatenated := concat(nil)
	if len(concatenated) != 0 {
		t.Fatalf("concat(nil) produced %d bytes, want 0", len(concatenated))
	}

	gz, err := gzipBytes(concatenated)
	if err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}

	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("gunzipped %d bytes, want 0", len(got))
	}
}
