package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/fogghost/redis_grpc_hw/flight-service/internal/repository"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	flightTTL = 5 * time.Minute
	searchTTL = 5 * time.Minute
)

type Cache struct {
	client *redis.Client
}

func New(client *redis.Client) *Cache {
	return &Cache{client: client}
}

func flightKey(flightID uuid.UUID) string {
	return fmt.Sprintf("flight:%s", flightID.String())
}

func searchKey(origin, destination, date string) string {
	return fmt.Sprintf("search:%s:%s:%s", origin, destination, date)
}

func (c *Cache) GetFlight(ctx context.Context, flightID uuid.UUID) (*repository.Flight, error) {
	key := flightKey(flightID)
	payload, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		log.Printf("CACHE MISS key=%s", key)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get flight: %w", err)
	}

	var flight repository.Flight
	if err := json.Unmarshal(payload, &flight); err != nil {
		return nil, fmt.Errorf("unmarshal flight: %w", err)
	}

	log.Printf("CACHE HIT key=%s", key)
	return &flight, nil
}

func (c *Cache) SetFlight(ctx context.Context, flight *repository.Flight) error {
	key := flightKey(flight.ID)
	payload, err := json.Marshal(flight)
	if err != nil {
		return fmt.Errorf("marshal flight: %w", err)
	}

	if err := c.client.Set(ctx, key, payload, flightTTL).Err(); err != nil {
		return fmt.Errorf("redis set flight: %w", err)
	}

	log.Printf("CACHE SET key=%s ttl=%s", key, flightTTL)
	return nil
}

func (c *Cache) DeleteFlight(ctx context.Context, flightID uuid.UUID) error {
	key := flightKey(flightID)
	if err := c.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis del flight: %w", err)
	}

	log.Printf("CACHE INVALIDATE key=%s", key)
	return nil
}

func (c *Cache) GetSearch(ctx context.Context, origin, destination, date string) ([]repository.Flight, error) {
	key := searchKey(origin, destination, date)
	payload, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		log.Printf("CACHE MISS key=%s", key)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get search: %w", err)
	}

	var flights []repository.Flight
	if err := json.Unmarshal(payload, &flights); err != nil {
		return nil, fmt.Errorf("unmarshal flights: %w", err)
	}

	log.Printf("CACHE HIT key=%s count=%d", key, len(flights))
	return flights, nil
}

func (c *Cache) SetSearch(ctx context.Context, origin, destination, date string, flights []repository.Flight) error {
	key := searchKey(origin, destination, date)
	payload, err := json.Marshal(flights)
	if err != nil {
		return fmt.Errorf("marshal flights: %w", err)
	}

	if err := c.client.Set(ctx, key, payload, searchTTL).Err(); err != nil {
		return fmt.Errorf("redis set search: %w", err)
	}

	log.Printf("CACHE SET key=%s count=%d ttl=%s", key, len(flights), searchTTL)
	return nil
}

func (c *Cache) DeleteSearchByPattern(ctx context.Context, pattern string) error {
	var cursor uint64
	keys := make([]string, 0)

	for {
		batch, next, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("redis scan: %w", err)
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}

	if len(keys) == 0 {
		return nil
	}

	if err := c.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis del search keys: %w", err)
	}

	log.Printf("CACHE INVALIDATE pattern=%s deleted=%d", pattern, len(keys))
	return nil
}
