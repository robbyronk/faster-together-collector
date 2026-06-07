package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DeviceFlowClient implements the collector side of the OAuth 2.0 Device
// Authorization Grant (RFC 8628) against the Phoenix server.
type DeviceFlowClient struct {
	HTTP    *http.Client
	BaseURL string // e.g. "http://localhost:4001"
}

// CodeResponse is the payload from POST /oauth/device/code.
type CodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// RequestCode hits POST /oauth/device/code.
func (c *DeviceFlowClient) RequestCode(ctx context.Context) (*CodeResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/oauth/device/code", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device/code: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var out CodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TokenError mirrors RFC 8628 error codes.
type TokenError string

const (
	ErrAuthorizationPending TokenError = "authorization_pending"
	ErrSlowDown             TokenError = "slow_down"
	ErrExpiredToken         TokenError = "expired_token"
	ErrAccessDenied         TokenError = "access_denied"
	ErrInvalidGrant         TokenError = "invalid_grant"
)

func (e TokenError) Error() string { return string(e) }

// PollOnce hits POST /oauth/device/token a single time.
func (c *DeviceFlowClient) PollOnce(ctx context.Context, deviceCode string) (string, error) {
	body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/oauth/device/token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}

	if resp.StatusCode == http.StatusOK {
		return payload.AccessToken, nil
	}

	switch payload.Error {
	case "authorization_pending":
		return "", ErrAuthorizationPending
	case "slow_down":
		return "", ErrSlowDown
	case "expired_token":
		return "", ErrExpiredToken
	case "access_denied":
		return "", ErrAccessDenied
	case "invalid_grant":
		return "", ErrInvalidGrant
	default:
		return "", fmt.Errorf("device/token: HTTP %d: %s", resp.StatusCode, payload.Error)
	}
}

// PollUntilToken blocks until the server returns a token, the device code
// expires, the user denies, or ctx is canceled. It honors `slow_down` by
// doubling the interval.
func (c *DeviceFlowClient) PollUntilToken(ctx context.Context, deviceCode string, interval time.Duration) (string, error) {
	current := interval
	for {
		token, err := c.PollOnce(ctx, deviceCode)
		if err == nil {
			return token, nil
		}

		switch {
		case errors.Is(err, ErrAuthorizationPending):
			// keep waiting
		case errors.Is(err, ErrSlowDown):
			current *= 2
		default:
			return "", err
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(current):
		}
	}
}

func (c *DeviceFlowClient) do(req *http.Request) (*http.Response, error) {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	return c.HTTP.Do(req)
}
