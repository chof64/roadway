package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const (
	listenAddress = ":80"
	upstreamURL   = "http://127.0.0.1:8080"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "--" {
		log.Fatal("usage: health-proxy -- command [args...]")
	}

	target, err := url.Parse(envOrDefault("UPSTREAM_URL", upstreamURL))
	if err != nil {
		log.Fatalf("invalid UPSTREAM_URL: %v", err)
	}
	probe, err := target.Parse(envOrDefault("UPSTREAM_UP_PATH", "/"))
	if err != nil {
		log.Fatalf("invalid UPSTREAM_UP_PATH: %v", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/up", upHandler(client, probe))
	mux.Handle("/", httputil.NewSingleHostReverseProxy(target))
	server := &http.Server{
		Addr:              listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := exec.CommandContext(ctx, os.Args[2], os.Args[3:]...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		log.Fatalf("start upstream: %v", err)
	}

	serverErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	commandErrors := make(chan error, 1)
	go func() { commandErrors <- command.Wait() }()

	exitCode := 0
	select {
	case err := <-serverErrors:
		log.Printf("health proxy failed: %v", err)
		exitCode = 1
		stop()
		<-commandErrors
	case err := <-commandErrors:
		exitCode = commandExitCode(err)
	case <-ctx.Done():
		<-commandErrors
	}

	_ = server.Shutdown(context.Background())
	os.Exit(exitCode)
}

func upHandler(client *http.Client, probe *url.URL) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, probe.String(), nil)
		if err != nil {
			writeUnavailable(w)
			return
		}
		response, err := client.Do(request)
		if err != nil {
			writeUnavailable(w)
			return
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			writeUnavailable(w)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"up"}`+"\n")
	}
}

func writeUnavailable(w http.ResponseWriter) {
	http.Error(w, `{"status":"down"}`+"\n", http.StatusServiceUnavailable)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return 1
}
