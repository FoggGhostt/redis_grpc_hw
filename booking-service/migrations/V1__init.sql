CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS bookings (
    id UUID PRIMARY KEY,
    user_id TEXT NOT NULL,
    flight_id UUID NOT NULL,
    passenger_name TEXT NOT NULL,
    passenger_email TEXT NOT NULL,
    seat_count INTEGER NOT NULL CHECK (seat_count > 0),
    total_price_kopecks BIGINT NOT NULL CHECK (total_price_kopecks > 0),
    status TEXT NOT NULL CHECK (status IN ('CONFIRMED', 'CANCELLED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bookings_user_id ON bookings (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bookings_flight_id ON bookings (flight_id);
