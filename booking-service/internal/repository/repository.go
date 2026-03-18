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
	BookingStatusConfirmed BookingStatus = "CONFIRMED"
	BookingStatusCancelled BookingStatus = "CANCELLED"
)

var (
	ErrBookingNotFound         = errors.New("booking not found")
	ErrBookingAlreadyCancelled = errors.New("booking already cancelled")
)

type BookingStatus string

type Booking struct {
	ID                uuid.UUID
	UserID            string
	FlightID          uuid.UUID
	PassengerName     string
	PassengerEmail    string
	SeatCount         int32
	TotalPriceKopecks int64
	Status            BookingStatus
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type CreateBookingParams struct {
	ID                uuid.UUID
	UserID            string
	FlightID          uuid.UUID
	PassengerName     string
	PassengerEmail    string
	SeatCount         int32
	TotalPriceKopecks int64
}

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, params CreateBookingParams) (*Booking, error) {
	const query = `
		INSERT INTO bookings (
			id, user_id, flight_id, passenger_name, passenger_email, seat_count, total_price_kopecks, status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'CONFIRMED')
		RETURNING id, user_id, flight_id, passenger_name, passenger_email, seat_count,
		          total_price_kopecks, status, created_at, updated_at`

	var booking Booking
	if err := r.db.QueryRowContext(
		ctx,
		query,
		params.ID,
		params.UserID,
		params.FlightID,
		params.PassengerName,
		params.PassengerEmail,
		params.SeatCount,
		params.TotalPriceKopecks,
	).Scan(
		&booking.ID,
		&booking.UserID,
		&booking.FlightID,
		&booking.PassengerName,
		&booking.PassengerEmail,
		&booking.SeatCount,
		&booking.TotalPriceKopecks,
		&booking.Status,
		&booking.CreatedAt,
		&booking.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert booking: %w", err)
	}

	return &booking, nil
}

func (r *Repository) GetByID(ctx context.Context, bookingID uuid.UUID) (*Booking, error) {
	const query = `
		SELECT id, user_id, flight_id, passenger_name, passenger_email, seat_count,
		       total_price_kopecks, status, created_at, updated_at
		FROM bookings
		WHERE id = $1`

	var booking Booking
	err := r.db.QueryRowContext(ctx, query, bookingID).Scan(
		&booking.ID,
		&booking.UserID,
		&booking.FlightID,
		&booking.PassengerName,
		&booking.PassengerEmail,
		&booking.SeatCount,
		&booking.TotalPriceKopecks,
		&booking.Status,
		&booking.CreatedAt,
		&booking.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBookingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get booking: %w", err)
	}

	return &booking, nil
}

func (r *Repository) ListByUser(ctx context.Context, userID string) ([]Booking, error) {
	const query = `
		SELECT id, user_id, flight_id, passenger_name, passenger_email, seat_count,
		       total_price_kopecks, status, created_at, updated_at
		FROM bookings
		WHERE user_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list bookings: %w", err)
	}
	defer rows.Close()

	bookings := make([]Booking, 0)
	for rows.Next() {
		var booking Booking
		if err := rows.Scan(
			&booking.ID,
			&booking.UserID,
			&booking.FlightID,
			&booking.PassengerName,
			&booking.PassengerEmail,
			&booking.SeatCount,
			&booking.TotalPriceKopecks,
			&booking.Status,
			&booking.CreatedAt,
			&booking.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan booking: %w", err)
		}
		bookings = append(bookings, booking)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bookings: %w", err)
	}

	return bookings, nil
}

func (r *Repository) Cancel(ctx context.Context, bookingID uuid.UUID) (*Booking, error) {
	const query = `
		UPDATE bookings
		SET status = 'CANCELLED',
		    updated_at = NOW()
		WHERE id = $1
		  AND status = 'CONFIRMED'
		RETURNING id, user_id, flight_id, passenger_name, passenger_email, seat_count,
		          total_price_kopecks, status, created_at, updated_at`

	var booking Booking
	err := r.db.QueryRowContext(ctx, query, bookingID).Scan(
		&booking.ID,
		&booking.UserID,
		&booking.FlightID,
		&booking.PassengerName,
		&booking.PassengerEmail,
		&booking.SeatCount,
		&booking.TotalPriceKopecks,
		&booking.Status,
		&booking.CreatedAt,
		&booking.UpdatedAt,
	)
	if err == nil {
		return &booking, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("cancel booking: %w", err)
	}

	existing, getErr := r.GetByID(ctx, bookingID)
	if getErr != nil {
		return nil, getErr
	}
	if existing.Status == BookingStatusCancelled {
		return nil, ErrBookingAlreadyCancelled
	}

	return nil, fmt.Errorf("cancel booking: unexpected state")
}
