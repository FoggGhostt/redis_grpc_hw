package http

import (
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/fogghost/redis_grpc_hw/booking-service/internal/api"
	"github.com/fogghost/redis_grpc_hw/booking-service/internal/grpcclient"
	"github.com/fogghost/redis_grpc_hw/booking-service/internal/repository"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	repo         *repository.Repository
	flightClient *grpcclient.Client
}

func NewServer(repo *repository.Repository, flightClient *grpcclient.Client) *Server {
	return &Server{repo: repo, flightClient: flightClient}
}

func (s *Server) Health(w nethttp.ResponseWriter, _ *nethttp.Request) {
	writeJSON(w, nethttp.StatusOK, api.HealthResponse{Status: "ok"})
}

func (s *Server) SearchFlights(w nethttp.ResponseWriter, r *nethttp.Request, params api.SearchFlightsParams) {
	origin := normalizeIATA(params.Origin)
	destination := normalizeIATA(params.Destination)
	if origin == "" || destination == "" {
		writeError(w, nethttp.StatusBadRequest, "VALIDATION_ERROR", "origin and destination must be 3-letter IATA codes")
		return
	}

	var departureDate *time.Time
	if params.Date != nil {
		t := params.Date.Time.UTC()
		departureDate = &t
	}

	flights, err := s.flightClient.SearchFlights(r.Context(), origin, destination, departureDate)
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	items := make([]api.Flight, 0, len(flights))
	for _, flight := range flights {
		items = append(items, toAPIFlight(flight))
	}

	writeJSON(w, nethttp.StatusOK, api.FlightListResponse{Items: items})
}

func (s *Server) GetFlightById(w nethttp.ResponseWriter, r *nethttp.Request, id openapi_types.UUID) {
	flight, err := s.flightClient.GetFlight(r.Context(), uuid.UUID(id))
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, nethttp.StatusOK, toAPIFlight(*flight))
}

func (s *Server) CreateBooking(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req api.BookingCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, nethttp.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	if err := validateCreateBooking(req); err != nil {
		writeError(w, nethttp.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	bookingID := uuid.New()
	flightID := uuid.UUID(req.FlightId)

	flight, err := s.flightClient.GetFlight(r.Context(), flightID)
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	if err := s.flightClient.ReserveSeats(r.Context(), flightID, bookingID, int32(req.SeatCount)); err != nil {
		writeGRPCError(w, err)
		return
	}

	totalPrice := int64(req.SeatCount) * flight.PriceKopecks
	booking, err := s.repo.Create(r.Context(), repository.CreateBookingParams{
		ID:                bookingID,
		UserID:            strings.TrimSpace(req.UserId),
		FlightID:          flightID,
		PassengerName:     strings.TrimSpace(req.PassengerName),
		PassengerEmail:    string(req.PassengerEmail),
		SeatCount:         int32(req.SeatCount),
		TotalPriceKopecks: totalPrice,
	})
	if err != nil {
		if releaseErr := s.flightClient.ReleaseReservation(r.Context(), bookingID); releaseErr != nil {
			writeError(w, nethttp.StatusInternalServerError, "CONSISTENCY_ERROR", fmt.Sprintf("booking save failed and compensation failed: %v", releaseErr))
			return
		}
		writeError(w, nethttp.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, nethttp.StatusCreated, toAPIBooking(*booking))
}

func (s *Server) GetBookingById(w nethttp.ResponseWriter, r *nethttp.Request, id openapi_types.UUID) {
	booking, err := s.repo.GetByID(r.Context(), uuid.UUID(id))
	if err != nil {
		if errors.Is(err, repository.ErrBookingNotFound) {
			writeError(w, nethttp.StatusNotFound, "NOT_FOUND", "booking not found")
			return
		}
		writeError(w, nethttp.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	writeJSON(w, nethttp.StatusOK, toAPIBooking(*booking))
}

func (s *Server) ListBookings(w nethttp.ResponseWriter, r *nethttp.Request, params api.ListBookingsParams) {
	userID := strings.TrimSpace(params.UserId)
	if userID == "" {
		writeError(w, nethttp.StatusBadRequest, "VALIDATION_ERROR", "user_id is required")
		return
	}

	bookings, err := s.repo.ListByUser(r.Context(), userID)
	if err != nil {
		writeError(w, nethttp.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	items := make([]api.Booking, 0, len(bookings))
	for _, booking := range bookings {
		items = append(items, toAPIBooking(booking))
	}

	writeJSON(w, nethttp.StatusOK, api.BookingListResponse{Items: items})
}

func (s *Server) CancelBooking(w nethttp.ResponseWriter, r *nethttp.Request, id openapi_types.UUID) {
	bookingID := uuid.UUID(id)

	booking, err := s.repo.GetByID(r.Context(), bookingID)
	if err != nil {
		if errors.Is(err, repository.ErrBookingNotFound) {
			writeError(w, nethttp.StatusNotFound, "NOT_FOUND", "booking not found")
			return
		}
		writeError(w, nethttp.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if booking.Status != repository.BookingStatusConfirmed {
		writeError(w, nethttp.StatusConflict, "INVALID_STATE", "booking is already cancelled")
		return
	}

	if err := s.flightClient.ReleaseReservation(r.Context(), bookingID); err != nil {
		writeGRPCError(w, err)
		return
	}

	cancelled, err := s.repo.Cancel(r.Context(), bookingID)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrBookingNotFound):
			writeError(w, nethttp.StatusNotFound, "NOT_FOUND", "booking not found")
		case errors.Is(err, repository.ErrBookingAlreadyCancelled):
			writeError(w, nethttp.StatusConflict, "INVALID_STATE", "booking is already cancelled")
		default:
			writeError(w, nethttp.StatusInternalServerError, "DB_ERROR", err.Error())
		}
		return
	}

	writeJSON(w, nethttp.StatusOK, toAPIBooking(*cancelled))
}

func validateCreateBooking(req api.BookingCreateRequest) error {
	if strings.TrimSpace(req.UserId) == "" {
		return errors.New("user_id is required")
	}
	if uuid.UUID(req.FlightId) == uuid.Nil {
		return errors.New("flight_id is required")
	}
	if strings.TrimSpace(req.PassengerName) == "" {
		return errors.New("passenger_name is required")
	}
	if strings.TrimSpace(string(req.PassengerEmail)) == "" {
		return errors.New("passenger_email is required")
	}
	if req.SeatCount <= 0 {
		return errors.New("seat_count must be positive")
	}
	return nil
}

func normalizeIATA(code string) string {
	code = strings.TrimSpace(strings.ToUpper(code))
	if len(code) != 3 {
		return ""
	}
	return code
}

func toAPIFlight(flight grpcclient.Flight) api.Flight {
	return api.Flight{
		Id:             flight.ID,
		FlightNumber:   flight.FlightNumber,
		AirlineCode:    flight.AirlineCode,
		AirlineName:    flight.AirlineName,
		Origin:         flight.Origin,
		Destination:    flight.Destination,
		DepartureTime:  flight.DepartureTime,
		ArrivalTime:    flight.ArrivalTime,
		TotalSeats:     int(flight.TotalSeats),
		AvailableSeats: int(flight.AvailableSeats),
		PriceKopecks:   flight.PriceKopecks,
		Status:         api.FlightStatus(flight.Status),
	}
}

func toAPIBooking(booking repository.Booking) api.Booking {
	return api.Booking{
		Id:                booking.ID,
		UserId:            booking.UserID,
		FlightId:          booking.FlightID,
		PassengerName:     booking.PassengerName,
		PassengerEmail:    openapi_types.Email(booking.PassengerEmail),
		SeatCount:         int(booking.SeatCount),
		TotalPriceKopecks: booking.TotalPriceKopecks,
		Status:            api.BookingStatus(booking.Status),
		CreatedAt:         booking.CreatedAt.UTC(),
		UpdatedAt:         booking.UpdatedAt.UTC(),
	}
}

func writeJSON(w nethttp.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w nethttp.ResponseWriter, statusCode int, code, message string) {
	writeJSON(w, statusCode, api.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

func writeGRPCError(w nethttp.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok {
		writeError(w, nethttp.StatusServiceUnavailable, "UPSTREAM_ERROR", err.Error())
		return
	}

	switch st.Code() {
	case codes.InvalidArgument:
		writeError(w, nethttp.StatusBadRequest, "INVALID_ARGUMENT", st.Message())
	case codes.NotFound:
		writeError(w, nethttp.StatusNotFound, "NOT_FOUND", st.Message())
	case codes.ResourceExhausted, codes.FailedPrecondition:
		writeError(w, nethttp.StatusConflict, "CONFLICT", st.Message())
	case codes.Unavailable, codes.DeadlineExceeded:
		writeError(w, nethttp.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", st.Message())
	case codes.Unauthenticated:
		writeError(w, nethttp.StatusServiceUnavailable, "UPSTREAM_AUTH_ERROR", st.Message())
	default:
		writeError(w, nethttp.StatusServiceUnavailable, "UPSTREAM_ERROR", st.Message())
	}
}
