// Package upload posts completed laps to the server's POST /api/laps endpoint.
package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"

	"github.com/robbyronk/faster-together-collector/internal/lap"
)

// Errors returned by Send. The CLI uses these to decide token-management
// actions (e.g. ErrUnauthorized → clear stored token).
var (
	ErrUnauthorized = errors.New("upload: server returned 401")
	ErrClientError  = errors.New("upload: server returned 4xx")
)

// Client posts to {BaseURL}/api/laps with a bearer token.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Token   string
}

// Send uploads one completed lap. On HTTP error, returns one of the sentinel
// errors above (for 4xx classification) or the raw transport error.
//
// Retries are the caller's responsibility — Send is a single attempt.
func (c *Client) Send(ctx context.Context, l *lap.Completed) error {
	body, contentType, err := buildMultipart(l)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/laps", body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %s", ErrUnauthorized, string(respBody))
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return fmt.Errorf("%w: HTTP %d: %s", ErrClientError, resp.StatusCode, string(respBody))
	}
	return fmt.Errorf("upload: HTTP %d: %s", resp.StatusCode, string(respBody))
}

// SendWithRetry retries on transport errors and 5xx with exponential backoff
// (1 s → 2 s → 4 s, max 3 attempts). Does not retry on 4xx — those are
// programmer/auth errors that won't resolve on their own.
func (c *Client) SendWithRetry(ctx context.Context, l *lap.Completed) error {
	delay := time.Second
	var last error

	for attempt := 1; attempt <= 3; attempt++ {
		err := c.Send(ctx, l)
		if err == nil {
			return nil
		}

		// 4xx is terminal — don't retry.
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrClientError) {
			return err
		}

		last = err
		if attempt == 3 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}

	return last
}

func buildMultipart(l *lap.Completed) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	metaHeader := textproto.MIMEHeader{}
	metaHeader.Set("Content-Disposition", `form-data; name="metadata"; filename="metadata.json"`)
	metaHeader.Set("Content-Type", "application/json")
	metaPart, err := w.CreatePart(metaHeader)
	if err != nil {
		return nil, "", err
	}
	meta := map[string]any{
		"lap_time_seconds":  l.LapTimeSec,
		"car_id":            l.CarID,
		"car_class":         l.CarClass,
		"performance_index": l.PerformanceIndex,
		"drivetrain_type":   l.DrivetrainType,
		"max_speed_mps":     l.MaxSpeedMps,
		"completed_at":      l.CompletedAt.UTC().Format(time.RFC3339Nano),
	}
	if err := json.NewEncoder(metaPart).Encode(meta); err != nil {
		return nil, "", err
	}

	blobHeader := textproto.MIMEHeader{}
	blobHeader.Set("Content-Disposition", `form-data; name="blob"; filename="lap.bin.gz"`)
	blobHeader.Set("Content-Type", "application/gzip")
	blobPart, err := w.CreatePart(blobHeader)
	if err != nil {
		return nil, "", err
	}
	if _, err := blobPart.Write(l.Blob); err != nil {
		return nil, "", err
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}

	return &buf, w.FormDataContentType(), nil
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}
