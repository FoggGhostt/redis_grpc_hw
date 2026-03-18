package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	FlightStatusScheduled FlightStatus = "SCHEDULED"
	FlightStatusDeparted  FlightStatus = "DEPARTED"
	FlightStatusCancelled FlightStatus = "CANCELLED"
	FlightStatusCompleted FlightStatus = "COMPLETED"

	ReservationStatusActive   ReservationStatus = "ACTIVE"
	ReservationStatusReleased ReservationStatus = "RELEASED"
	ReservationStatusExpired  ReservationStatus = "EXPIRED"
)

var (
	ErrFlightNotFound       = errors.New("flight not found")
	ErrReservationNotFound  = errors.New("reservation not found")
	ErrInsufficientSeats    = errors.New("insufficient seats")
	ErrFlightNotBookable    = errors.New("flight not bookable")
	ErrReservationConflict  = errors.New("reservation request conflicts with existing reservation")
)

type FlightStatus string
type ReservationStatus string

type Flight struct {
	ID             uuid.UUID
	FlightNumber   string
	AirlineCode    string
	AirlineName    string
	Origin         string
	Destination    string
	DepartureTime  time.Time
	ArrivalTime    time.Time
	TotalSeats     int32
	AvailableSeats int32
	PriceKopecks   int64
	Status         FlightStatus
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type SeatReservation struct {
	ID        uuid.UUID
	FlightID  uuid.UUID
	BookingID uuid.UUID
	SeatCount int32
	Status    ReservationStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) SearchFlights(ctx context.Context, origin, destination string, departureDate *time.Time) ([]Flight, error) {
	const query = `
		SELECT id, flight_number, airline_code, airline_name, origin, destination,
		       departure_time, arrival_time, total_seats, available_seats, price_kopecks,
		       status, created_at, updated_at
		FROM flights
		WHERE origin = $1
		  AND destination = $2
		  AND status = 'SCHEDULED'
		  AND ($3::date IS NULL OR (departure_time AT TIME ZONE 'UTC')::date = $3::date)
		ORDER BY departure_time`

	var dateParam any
	if departureDate != nil {
		dateParam = departureDate.UTC().Format("2006-01-02")
	}

	rows, err := r.db.QueryContext(ctx, query, origin, destination, dateParam)
	if err != nil {
		return nil, fmt.Errorf("search flights: %w", err)
	}
	defer rows.Close()

	flights := make([]Flight, 0)
	for rows.Next() {
		var flight Flight
		if err := rows.Scan(
			&flight.ID,
			&flight.FlightNumber,
			&flight.AirlineCode,
			&flight.AirlineName,
			&flight.Origin,
			&flight.Destination,
			&flight.DepartureTime,
			&flight.ArrivalTime,
			&flight.TotalSeats,
			&flight.AvailableSeats,
			&flight.PriceKopecks,
			&flight.Status,
			&flight.CreatedAt,
			&flight.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan flight: %w", err)
		}
		flights = append(flights, flight)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flights: %w", err)
	}

	return flights, nil
}

func (r *Repository) GetFlight(ctx context.Context, flightID uuid.UUID) (*Flight, error) {
	const query = `
		SELECT id, flight_number, airline_code, airline_name, origin, destination,
		       departure_time, arrival_time, total_seats, available_seats, price_kopecks,
		       status, created_at, updated_at
		FROM flights
		WHERE id = $1`

	var flight Flight
	err := r.db.QueryRowContext(ctx, query, flightID).Scan(
		&flight.ID,
		&flight.FlightNumber,
		&flight.AirlineCode,
		&flight.AirlineName,
		&flight.Origin,
		&flight.Destination,
		&flight.DepartureTime,
		&flight.ArrivalTime,
		&flight.TotalSeats,
		&flight.AvailableSeats,
		&flight.PriceKopecks,
		&flight.Status,
		&flight.CreatedAt,
		&flight.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFlightNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get flight: %w", err)
	}

	return &flight, nil
}

func (r *Repository) ReserveSeats(ctx context.Context, flightID, bookingID uuid.UUID, seatCount int32) (*SeatReservation, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	existing, err := getReservationForUpdate(ctx, tx, bookingID, false)
	if err != nil && !errors.Is(err, ErrReservationNotFound) {
		return nil, err
	}
	if err == nil {
		if existing.FlightID != flightID || existing.SeatCount != seatCount {
			return nil, ErrReservationConflict
		}
		return existing, nil
	}

	const lockFlightQuery = `
		SELECT id, flight_number, airline_code, airline_name, origin, destination,
		       departure_time, arrival_time, total_seats, available_seats, price_kopecks,
		       status, created_at, updated_at
		FROM flights
		WHERE id = $1
		FOR UPDATE`

	var flight Flight
	err = tx.QueryRowContext(ctx, lockFlightQuery, flightID).Scan(
		&flight.ID,
		&flight.FlightNumber,
		&flight.AirlineCode,
		&flight.AirlineName,
		&flight.Origin,
		&flight.Destination,
		&flight.DepartureTime,
		&flight.ArrivalTime,
		&flight.TotalSeats,
		&flight.AvailableSeats,
		&flight.PriceKopecks,
		&flight.Status,
		&flight.CreatedAt,
		&flight.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFlightNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock flight: %w", err)
	}

	if flight.Status != FlightStatusScheduled {
		return nil, ErrFlightNotBookable
	}
	if flight.AvailableSeats < seatCount {
		return nil, ErrInsufficientSeats
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE flights
		SET available_seats = available_seats - $1,
		    updated_at = NOW()
		WHERE id = $2`,
		seatCount, flightID,
	); err != nil {
		return nil, fmt.Errorf("decrement available seats: %w", err)
	}

	const insertReservation = `
		INSERT INTO seat_reservations (flight_id, booking_id, seat_count, status)
		VALUES ($1, $2, $3, 'ACTIVE')
		RETURNING id, flight_id, booking_id, seat_count, status, created_at, updated_at`

	var reservation SeatReservation
	if err := tx.QueryRowContext(ctx, insertReservation, flightID, bookingID, seatCount).Scan(
		&reservation.ID,
		&reservation.FlightID,
		&reservation.BookingID,
		&reservation.SeatCount,
		&reservation.Status,
		&reservation.CreatedAt,
		&reservation.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert reservation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return &reservation, nil
}

func (r *Repository) ReleaseReservation(ctx context.Context, bookingID uuid.UUID) (*SeatReservation, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	reservation, err := getReservationForUpdate(ctx, tx, bookingID, true)
	if err != nil {
		return nil, err
	}

	if reservation.Status != ReservationStatusActive {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit tx: %w", err)
		}
		return reservation, nil
	}

	var totalSeats int32
	if err := tx.QueryRowContext(ctx, `
		SELECT total_seats
		FROM flights
		WHERE id = $1
		FOR UPDATE`,
		reservation.FlightID,
	).Scan(&totalSeats); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFlightNotFound
		}
		return nil, fmt.Errorf("lock flight for release: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE flights
		SET available_seats = available_seats + $1,
		    updated_at = NOW()
		WHERE id = $2`,
		reservation.SeatCount, reservation.FlightID,
	); err != nil {
		return nil, fmt.Errorf("restore seats: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE seat_reservations
		SET status = 'RELEASED',
		    updated_at = NOW()
		WHERE booking_id = $1`,
		bookingID,
	); err != nil {
		return nil, fmt.Errorf("mark reservation released: %w", err)
	}

	reservation.Status = ReservationStatusReleased
	reservation.UpdatedAt = time.Now().UTC()

	_ = totalSeats

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return reservation, nil
}

func getReservationForUpdate(ctx context.Context, tx *sql.Tx, bookingID uuid.UUID, forUpdate bool) (*SeatReservation, error) {
	query := `
		SELECT id, flight_id, booking_id, seat_count, status, created_at, updated_at
		FROM seat_reservations
		WHERE booking_id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}

	var reservation SeatReservation
	err := tx.QueryRowContext(ctx, query, bookingID).Scan(
		&reservation.ID,
		&reservation.FlightID,
		&reservation.BookingID,
		&reservation.SeatCount,
		&reservation.Status,
		&reservation.CreatedAt,
		&reservation.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrReservationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get reservation: %w", err)
	}

	return &reservation, nil
}
