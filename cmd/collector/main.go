// collector — FasterTogetherClub UDP telemetry uploader.
//
// Usage:
//   collector                  # default: listen + upload
//   collector --port 5301
//   collector --server http://localhost:4001
//   collector login            # device-flow login, even if a token exists
//   collector logout           # delete the stored token
//   collector doctor           # diagnostic snapshot
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robbyronk/faster-together-collector/internal/auth"
	"github.com/robbyronk/faster-together-collector/internal/cli"
	"github.com/robbyronk/faster-together-collector/internal/lap"
	"github.com/robbyronk/faster-together-collector/internal/udp"
	"github.com/robbyronk/faster-together-collector/internal/upload"
)

func main() {
	port := flag.Int("port", 5300, "UDP port to listen on")
	server := flag.String("server", "https://fastertogether.club", "Server base URL")
	tokenFile := flag.String("token-file", "", "Override path to the bearer token file")
	flag.Parse()

	tokenPath := *tokenFile
	if tokenPath == "" {
		var err error
		tokenPath, err = auth.DefaultTokenPath()
		if err != nil {
			log.Fatalf("could not resolve config dir: %v", err)
		}
	}

	switch flag.Arg(0) {
	case "login":
		if err := runLogin(*server, tokenPath); err != nil {
			log.Fatal(err)
		}
	case "logout":
		if err := auth.DeleteToken(tokenPath); err != nil {
			log.Fatal(err)
		}
		fmt.Println("logged out")
	case "doctor":
		runDoctor(*server, tokenPath, *port)
	case "":
		runListener(*server, tokenPath, *port)
	default:
		log.Fatalf("unknown subcommand: %q", flag.Arg(0))
	}
}

func runLogin(server, tokenPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	client := &auth.DeviceFlowClient{BaseURL: server, HTTP: &http.Client{Timeout: 30 * time.Second}}
	code, err := client.RequestCode(ctx)
	if err != nil {
		return fmt.Errorf("requesting device code: %w", err)
	}

	fmt.Printf("Activation code: %s\n", code.UserCode)
	cli.OpenBrowser(os.Stdout, code.VerificationURIComplete)
	fmt.Printf("Waiting for approval (this will time out in %d minutes)...\n", code.ExpiresIn/60)

	token, err := client.PollUntilToken(ctx, code.DeviceCode, time.Duration(code.Interval)*time.Second)
	if err != nil {
		return fmt.Errorf("polling for token: %w", err)
	}

	if err := auth.WriteToken(tokenPath, token); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}
	fmt.Printf("Logged in. Token saved to %s\n", tokenPath)
	return nil
}

func runListener(server, tokenPath string, port int) {
	token, err := auth.ReadToken(tokenPath)
	if err != nil {
		log.Fatalf("reading token: %v", err)
	}
	if token == "" {
		log.Fatalf("no token found at %s. Run `collector login`.", tokenPath)
	}

	listener, addr, err := udp.New(port)
	if err != nil {
		log.Fatalf("binding UDP: %v", err)
	}
	defer listener.Close()
	log.Printf("listening on %s", addr)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	frames := make(chan []byte, 256)
	completedLaps := make(chan *lap.Completed, 32)

	detector := lap.NewDetector()
	uploader := &upload.Client{BaseURL: server, Token: token}

	go func() {
		defer close(frames)
		_ = listener.Run(ctx, frames)
	}()

	go func() {
		defer close(completedLaps)
		for raw := range frames {
			if completed := detector.OnPacket(raw); completed != nil {
				select {
				case completedLaps <- completed:
				default:
					// uploader backlog full; drop the oldest by consuming one
					<-completedLaps
					completedLaps <- completed
					log.Print("uploader backlog full; dropped a lap")
				}
			}
		}
	}()

	uploaded := 0
	failed := 0
	for completed := range completedLaps {
		err := uploader.SendWithRetry(ctx, completed)
		switch {
		case err == nil:
			uploaded++
			log.Printf("uploaded lap (%.3f s, max %.1f m/s)", completed.LapTimeSec, completed.MaxSpeedMps)
		case errors.Is(err, upload.ErrUnauthorized):
			log.Print("Your collector login expired. Run `collector login`.")
			_ = auth.DeleteToken(tokenPath)
			cancel()
		default:
			failed++
			log.Printf("upload failed (dropping lap): %v", err)
		}
	}

	log.Printf("shutting down: uploaded=%d failed=%d wrong_size=%d total=%d",
		uploaded, failed, listener.WrongSize.Load(), listener.Total.Load())
}

func runDoctor(server, tokenPath string, port int) {
	fmt.Printf("server:     %s\n", server)
	fmt.Printf("token file: %s\n", tokenPath)

	token, err := auth.ReadToken(tokenPath)
	if err != nil {
		fmt.Printf("token read error: %v\n", err)
	} else if token == "" {
		fmt.Println("token: (none — run `collector login`)")
	} else {
		fmt.Printf("token: present (%d chars)\n", len(token))
	}

	// Best-effort server reachability
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("server: unreachable (%v)\n", err)
	} else {
		fmt.Printf("server: reachable (HTTP %d)\n", resp.StatusCode)
		resp.Body.Close()
	}

	fmt.Printf("would listen on: 0.0.0.0:%d\n", port)
}
