package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fogghost/redis_grpc_hw/flight-service/internal/cache"
	"github.com/fogghost/redis_grpc_hw/flight-service/internal/repository"
	"github.com/fogghost/redis_grpc_hw/flight-service/internal/server"
	flightv1 "github.com/fogghost/redis_grpc_hw/gen/flight/v1"
	postgresplatform "github.com/fogghost/redis_grpc_hw/internal/platform/postgres"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	databaseURL := envOrDefault("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/flights?sslmode=disable")
	redisURL := envOrDefault("REDIS_URL", "redis://localhost:6379")
	grpcAddr := envOrDefault("GRPC_ADDR", ":50051")
	apiKey := envOrDefault("API_KEY", "secret-key")

	db, err := postgresplatform.Open(ctx, databaseURL, 20, 2*time.Second)
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	defer db.Close()

	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("parse redis url: %v", err)
	}

	redisClient := redis.NewClient(redisOpts)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("ping redis: %v", err)
	}
	defer redisClient.Close()

	repo := repository.New(db)
	cacheStore := cache.New(redisClient)
	service := server.New(repo, cacheStore)

	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(server.NewAuthInterceptor(apiKey).Unary()))
	flightv1.RegisterFlightServiceServer(grpcServer, service)
	reflection.Register(grpcServer)

	listener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen grpc: %v", err)
	}

	go func() {
		<-ctx.Done()
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()

		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			grpcServer.Stop()
		}
	}()

	log.Printf("flight-service listening on %s", grpcAddr)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("serve grpc: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
