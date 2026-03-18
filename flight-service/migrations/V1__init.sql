CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS flights (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    flight_number TEXT NOT NULL,
    airline_code TEXT NOT NULL,
    airline_name TEXT NOT NULL,
    origin CHAR(3) NOT NULL,
    destination CHAR(3) NOT NULL,
    departure_time TIMESTAMPTZ NOT NULL,
    arrival_time TIMESTAMPTZ NOT NULL,
    total_seats INTEGER NOT NULL CHECK (total_seats > 0),
    available_seats INTEGER NOT NULL CHECK (available_seats >= 0),
    price_kopecks BIGINT NOT NULL CHECK (price_kopecks > 0),
    status TEXT NOT NULL CHECK (status IN ('SCHEDULED', 'DEPARTED', 'CANCELLED', 'COMPLETED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (available_seats <= total_seats),
    CHECK (arrival_time > departure_time),
    CHECK (origin <> destination)
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_flights_number_departure_date
    ON flights (flight_number, ((departure_time AT TIME ZONE 'UTC')::date));

CREATE INDEX IF NOT EXISTS idx_flights_route_date_status
    ON flights (origin, destination, ((departure_time AT TIME ZONE 'UTC')::date), status);

CREATE TABLE IF NOT EXISTS seat_reservations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    flight_id UUID NOT NULL REFERENCES flights (id) ON DELETE CASCADE,
    booking_id UUID NOT NULL UNIQUE,
    seat_count INTEGER NOT NULL CHECK (seat_count > 0),
    status TEXT NOT NULL CHECK (status IN ('ACTIVE', 'RELEASED', 'EXPIRED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_seat_reservations_flight_id ON seat_reservations (flight_id);
CREATE INDEX IF NOT EXISTS idx_seat_reservations_status ON seat_reservations (status);

INSERT INTO flights (
    id,
    flight_number,
    airline_code,
    airline_name,
    origin,
    destination,
    departure_time,
    arrival_time,
    total_seats,
    available_seats,
    price_kopecks,
    status
) VALUES
    (
        '11111111-1111-1111-1111-111111111111',
        'SU1234',
        'SU',
        'Aeroflot',
        'SVO',
        'LED',
        '2026-04-01T08:00:00Z',
        '2026-04-01T09:30:00Z',
        120,
        120,
        650000,
        'SCHEDULED'
    ),
    (
        '22222222-2222-2222-2222-222222222222',
        'DP405',
        'DP',
        'Pobeda',
        'VKO',
        'LED',
        '2026-04-01T12:00:00Z',
        '2026-04-01T13:25:00Z',
        90,
        90,
        420000,
        'SCHEDULED'
    ),
    (
        '33333333-3333-3333-3333-333333333333',
        'S7123',
        'S7',
        'S7 Airlines',
        'SVO',
        'KZN',
        '2026-04-02T07:30:00Z',
        '2026-04-02T09:10:00Z',
        100,
        98,
        510000,
        'SCHEDULED'
    )
