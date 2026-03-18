package server

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/fogghost/redis_grpc_hw/flight-service/internal/cache"
	"github.com/fogghost/redis_grpc_hw/flight-service/internal/repository"
	flightv1 "github.com/fogghost/redis_grpc_hw/gen/flight/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	flightv1.UnimplementedFlightServiceServer
	repo  *repository.Repository
	cache *cache.Cache
}

func New(repo *repository.Repository, cache *cache.Cache) *Server {
	return &Server{repo: repo, cache: cache}
}

func (s *Server) SearchFlights(ctx context.Context, req *flightv1.SearchFlightsRequest) (*flightv1.SearchFlightsResponse, error) {
	origin := normalizeIATA(req.GetOrigin())
	destination := normalizeIATA(req.GetDestination())
	if origin == "" || destination == "" {
		return nil, status.Error(codes.InvalidArgument, "origin and destination are required")
	}

	var departureDate *time.Time
	dateKey := ""
	if req.DepartureDate != nil {
		ts := req.DepartureDate.AsTime().UTC()
		departureDate = &ts
		dateKey = ts.Format("2006-01-02")
	}

	cached, err := s.cache.GetSearch(ctx, origin, destination, dateKey)
	if err != nil {
		log.Printf("cache get search failed: %v", err)
	}
	if cached != nil {
		return &flightv1.SearchFlightsResponse{Flights: flightsToProto(cached)}, nil
	}

	flights, err := s.repo.SearchFlights(ctx, origin, destination, departureDate)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "search flights: %v", err)
	}

	if err := s.cache.SetSearch(ctx, origin, destination, dateKey, flights); err != nil {
		log.Printf("cache set search failed: %v", err)
	}

	return &flightv1.SearchFlightsResponse{Flights: flightsToProto(flights)}, nil
}

func (s *Server) GetFlight(ctx context.Context, req *flightv1.GetFlightRequest) (*flightv1.GetFlightResponse, error) {
	flightID, err := uuid.Parse(req.GetFlightId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "flight_id must be a UUID")
	}

	cached, err := s.cache.GetFlight(ctx, flightID)
	if err != nil {
		log.Printf("cache get flight failed: %v", err)
	}
	if cached != nil {
		return &flightv1.GetFlightResponse{Flight: flightToProto(*cached)}, nil
	}

	flight, err := s.repo.GetFlight(ctx, flightID)
	if err != nil {
		if errors.Is(err, repository.ErrFlightNotFound) {
			return nil, status.Error(codes.NotFound, "flight not found")
		}
		return nil, status.Errorf(codes.Internal, "get flight: %v", err)
	}

	if err := s.cache.SetFlight(ctx, flight); err != nil {
		log.Printf("cache set flight failed: %v", err)
	}

	return &flightv1.GetFlightResponse{Flight: flightToProto(*flight)}, nil
}

func (s *Server) ReserveSeats(ctx context.Context, req *flightv1.ReserveSeatsRequest) (*flightv1.ReserveSeatsResponse, error) {
	flightID, err := uuid.Parse(req.GetFlightId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "flight_id must be a UUID")
	}
	bookingID, err := uuid.Parse(req.GetBookingId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "booking_id must be a UUID")
	}
	if req.GetSeatCount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "seat_count must be positive")
	}

	reservation, err := s.repo.ReserveSeats(ctx, flightID, bookingID, req.GetSeatCount())
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrFlightNotFound):
			return nil, status.Error(codes.NotFound, "flight not found")
		case errors.Is(err, repository.ErrInsufficientSeats):
			return nil, status.Error(codes.ResourceExhausted, "not enough seats available")
		case errors.Is(err, repository.ErrFlightNotBookable):
			return nil, status.Error(codes.FailedPrecondition, "flight is not available for booking")
		case errors.Is(err, repository.ErrReservationConflict):
			return nil, status.Error(codes.FailedPrecondition, "booking_id already used with different reservation parameters")
		default:
			return nil, status.Errorf(codes.Internal, "reserve seats: %v", err)
		}
	}

	if err := s.cache.DeleteFlight(ctx, flightID); err != nil {
		log.Printf("cache invalidate flight failed: %v", err)
	}
	if err := s.cache.DeleteSearchByPattern(ctx, "search:*"); err != nil {
		log.Printf("cache invalidate search failed: %v", err)
	}

	return &flightv1.ReserveSeatsResponse{Reservation: reservationToProto(*reservation)}, nil
}

func (s *Server) ReleaseReservation(ctx context.Context, req *flightv1.ReleaseReservationRequest) (*flightv1.ReleaseReservationResponse, error) {
	bookingID, err := uuid.Parse(req.GetBookingId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "booking_id must be a UUID")
	}

	reservation, err := s.repo.ReleaseReservation(ctx, bookingID)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrReservationNotFound):
			return nil, status.Error(codes.NotFound, "reservation not found")
		case errors.Is(err, repository.ErrFlightNotFound):
			return nil, status.Error(codes.NotFound, "flight not found")
		default:
			return nil, status.Errorf(codes.Internal, "release reservation: %v", err)
		}
	}

	if err := s.cache.DeleteFlight(ctx, reservation.FlightID); err != nil {
		log.Printf("cache invalidate flight failed: %v", err)
	}
	if err := s.cache.DeleteSearchByPattern(ctx, "search:*"); err != nil {
		log.Printf("cache invalidate search failed: %v", err)
	}

	return &flightv1.ReleaseReservationResponse{Reservation: reservationToProto(*reservation)}, nil
}

func normalizeIATA(code string) string {
	code = strings.TrimSpace(strings.ToUpper(code))
	if len(code) != 3 {
		return ""
	}
	return code
}

func flightsToProto(flights []repository.Flight) []*flightv1.Flight {
	result := make([]*flightv1.Flight, 0, len(flights))
	for _, flight := range flights {
		clone := flight
		result = append(result, flightToProto(clone))
	}
	return result
}

func flightToProto(flight repository.Flight) *flightv1.Flight {
	return &flightv1.Flight{
		Id:             flight.ID.String(),
		FlightNumber:   flight.FlightNumber,
		AirlineCode:    flight.AirlineCode,
		AirlineName:    flight.AirlineName,
		Origin:         flight.Origin,
		Destination:    flight.Destination,
		DepartureTime:  timestamppb.New(flight.DepartureTime.UTC()),
		ArrivalTime:    timestamppb.New(flight.ArrivalTime.UTC()),
		TotalSeats:     flight.TotalSeats,
		AvailableSeats: flight.AvailableSeats,
		PriceKopecks:   flight.PriceKopecks,
		Status:         mapFlightStatus(flight.Status),
	}
}

func reservationToProto(reservation repository.SeatReservation) *flightv1.SeatReservation {
	return &flightv1.SeatReservation{
		Id:        reservation.ID.String(),
		FlightId:  reservation.FlightID.String(),
		BookingId: reservation.BookingID.String(),
		SeatCount: reservation.SeatCount,
		Status:    mapReservationStatus(reservation.Status),
		CreatedAt: timestamppb.New(reservation.CreatedAt.UTC()),
		UpdatedAt: timestamppb.New(reservation.UpdatedAt.UTC()),
	}
}

func mapFlightStatus(statusValue repository.FlightStatus) flightv1.FlightStatus {
	switch statusValue {
	case repository.FlightStatusScheduled:
		return flightv1.FlightStatus_FLIGHT_STATUS_SCHEDULED
	case repository.FlightStatusDeparted:
		return flightv1.FlightStatus_FLIGHT_STATUS_DEPARTED
	case repository.FlightStatusCancelled:
		return flightv1.FlightStatus_FLIGHT_STATUS_CANCELLED
	case repository.FlightStatusCompleted:
		return flightv1.FlightStatus_FLIGHT_STATUS_COMPLETED
	default:
		return flightv1.FlightStatus_FLIGHT_STATUS_UNSPECIFIED
	}
}

func mapReservationStatus(statusValue repository.ReservationStatus) flightv1.ReservationStatus {
	switch statusValue {
	case repository.ReservationStatusActive:
		return flightv1.ReservationStatus_RESERVATION_STATUS_ACTIVE
	case repository.ReservationStatusReleased:
		return flightv1.ReservationStatus_RESERVATION_STATUS_RELEASED
	case repository.ReservationStatusExpired:
		return flightv1.ReservationStatus_RESERVATION_STATUS_EXPIRED
	default:
		return flightv1.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED
	}
}
