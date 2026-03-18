package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/fogghost/redis_grpc_hw/booking-service/internal/api"
	grpcclient "github.com/fogghost/redis_grpc_hw/booking-service/internal/grpcclient"
	apphttp "github.com/fogghost/redis_grpc_hw/booking-service/internal/http"
	"github.com/fogghost/redis_grpc_hw/booking-service/internal/repository"
	postgresplatform "github.com/fogghost/redis_grpc_hw/internal/platform/postgres"
	"github.com/go-chi/chi/v5"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	databaseURL := envOrDefault("DATABASE_URL", "postgres://postgres:postgres@localhost:5433/bookings?sslmode=disable")
	httpAddr := envOrDefault("HTTP_ADDR", ":8080")
	flightServiceAddr := envOrDefault("FLIGHT_SERVICE_ADDR", "localhost:50051")
	apiKey := envOrDefault("API_KEY", "secret-key")

	db, err := postgresplatform.Open(ctx, databaseURL, 20, 2*time.Second)
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	defer db.Close()

	maxAttempts := envInt("RETRY_MAX_ATTEMPTS", 3)
	initialBackoff := envDurationMS("RETRY_INITIAL_BACKOFF_MS", 100*time.Millisecond)
	requestTimeout := envDurationMS("GRPC_REQUEST_TIMEOUT_MS", 1500*time.Millisecond)

	flightClient, err := grpcclient.New(ctx, flightServiceAddr, apiKey, maxAttempts, initialBackoff, requestTimeout)
	if err != nil {
		log.Fatalf("create flight client: %v", err)
	}
	defer flightClient.Close()

	repo := repository.New(db)
	server := apphttp.NewServer(repo, flightClient)

	router := chi.NewRouter()
	handler := api.HandlerFromMux(server, router)

	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("booking-service listening on %s", httpAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve http: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envDurationMS(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return time.Duration(parsed) * time.Millisecond
}
