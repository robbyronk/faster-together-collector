package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/device/code" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"device_code":"abc",
			"user_code":"WDJB-MJHT",
			"verification_uri":"http://localhost/activate",
			"verification_uri_complete":"http://localhost/activate?code=WDJB-MJHT",
			"expires_in":600,
			"interval":5
		}`))
	}))
	defer srv.Close()

	c := &DeviceFlowClient{BaseURL: srv.URL}
	got, err := c.RequestCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.UserCode != "WDJB-MJHT" || got.DeviceCode != "abc" || got.Interval != 5 {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestPollUntilToken(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"granted","token_type":"bearer"}`))
	}))
	defer srv.Close()

	c := &DeviceFlowClient{BaseURL: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	token, err := c.PollUntilToken(ctx, "dc", 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if token != "granted" {
		t.Errorf("token = %q, want granted", token)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestPollOnceMapsErrors(t *testing.T) {
	for _, tc := range []struct {
		body string
		want TokenError
	}{
		{`{"error":"authorization_pending"}`, ErrAuthorizationPending},
		{`{"error":"slow_down"}`, ErrSlowDown},
		{`{"error":"expired_token"}`, ErrExpiredToken},
		{`{"error":"access_denied"}`, ErrAccessDenied},
		{`{"error":"invalid_grant"}`, ErrInvalidGrant},
	} {
		t.Run(string(tc.want), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := &DeviceFlowClient{BaseURL: srv.URL}
			_, err := c.PollOnce(context.Background(), "dc")
			if err != tc.want {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
