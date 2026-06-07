package upload

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robbyronk/faster-together-collector/internal/lap"
)

func fakeLap() *lap.Completed {
	raw := bytes.Repeat([]byte{0xAB}, 324*3)
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(raw)
	w.Close()
	return &lap.Completed{
		LapTimeSec:       95.412,
		CarID:            1234,
		CarClass:         5,
		PerformanceIndex: 825,
		DrivetrainType:   2,
		MaxSpeedMps:      78.4,
		CompletedAt:      time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
		Blob:             buf.Bytes(),
	}
}

func TestSendHappyPath(t *testing.T) {
	var gotMetadata map[string]any
	var gotBlob []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer abc123" {
			t.Errorf("Authorization=%q", r.Header.Get("Authorization"))
		}
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatal(err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(part)
			if part.FormName() == "metadata" {
				_ = json.Unmarshal(b, &gotMetadata)
			}
			if part.FormName() == "blob" {
				gotBlob = b
			}
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x","received_at":"2026-05-28T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "abc123"}
	if err := c.Send(context.Background(), fakeLap()); err != nil {
		t.Fatal(err)
	}

	if gotMetadata["car_id"].(float64) != 1234 {
		t.Errorf("metadata.car_id=%v", gotMetadata["car_id"])
	}
	r, err := gzip.NewReader(bytes.NewReader(gotBlob))
	if err != nil {
		t.Fatal(err)
	}
	un, _ := io.ReadAll(r)
	if len(un)%324 != 0 {
		t.Errorf("blob unzips to %d bytes, not multiple of 324", len(un))
	}
}

func TestSend401ReturnsErrUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "abc"}
	err := c.Send(context.Background(), fakeLap())
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("got %v, want ErrUnauthorized", err)
	}
}

func TestSendWithRetry5xxThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "abc"}
	// Shortcut delay via overriding the HTTP client? Easier: just live with
	// the 1s sleep — total ~1s.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.SendWithRetry(ctx, fakeLap()); err != nil {
		t.Fatalf("retry should have succeeded: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls=%d, want 2", calls.Load())
	}
}

func TestSendWithRetryDoesNotRetry4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "abc"}
	err := c.SendWithRetry(context.Background(), fakeLap())
	if !errors.Is(err, ErrClientError) {
		t.Errorf("got %v, want ErrClientError", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls=%d, want 1 (no retry on 4xx)", calls.Load())
	}
}
