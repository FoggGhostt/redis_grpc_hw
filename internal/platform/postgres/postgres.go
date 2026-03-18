package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func Open(ctx context.Context, dsn string, maxAttempts int, delay time.Duration) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = db.PingContext(ctx)
		if lastErr == nil {
			return db, nil
		}

		select {
		case <-ctx.Done():
			_ = db.Close()
			return nil, fmt.Errorf("wait for postgres: %w", ctx.Err())
		case <-time.After(delay):
		}
	}

	_ = db.Close()
	return nil, fmt.Errorf("ping postgres after %d attempts: %w", maxAttempts, lastErr)
}
