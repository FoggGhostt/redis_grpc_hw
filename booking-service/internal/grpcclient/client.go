package grpcclient

import (
	"context"
	"fmt"
	"log"
	"time"

	flightv1 "github.com/fogghost/redis_grpc_hw/gen/flight/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
	Status         string
}

type Client struct {
	conn           *grpc.ClientConn
	stub           flightv1.FlightServiceClient
	apiKey         string
	maxAttempts    int
	initialBackoff time.Duration
	requestTimeout time.Duration
}

func New(ctx context.Context, addr, apiKey string, maxAttempts int, initialBackoff, requestTimeout time.Duration) (*Client, error) {
	conn, err := grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial flight-service: %w", err)
	}

	return &Client{
		conn:           conn,
		stub:           flightv1.NewFlightServiceClient(conn),
		apiKey:         apiKey,
		maxAttempts:    maxAttempts,
		initialBackoff: initialBackoff,
		requestTimeout: requestTimeout,
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) SearchFlights(ctx context.Context, origin, destination string, date *time.Time) ([]Flight, error) {
	req := &flightv1.SearchFlightsRequest{
		Origin:      origin,
		Destination: destination,
	}
	if date != nil {
		req.DepartureDate = timestamppb.New(date.UTC())
	}

	resp, err := callWithRetry(ctx, c.maxAttempts, c.initialBackoff, c.requestTimeout, "SearchFlights", func(attemptCtx context.Context) (*flightv1.SearchFlightsResponse, error) {
		return c.stub.SearchFlights(c.withMetadata(attemptCtx), req)
	})
	if err != nil {
		return nil, err
	}

	flights := make([]Flight, 0, len(resp.GetFlights()))
	for _, item := range resp.GetFlights() {
		flights = append(flights, fromProtoFlight(item))
	}

	return flights, nil
}

func (c *Client) GetFlight(ctx context.Context, flightID uuid.UUID) (*Flight, error) {
	resp, err := callWithRetry(ctx, c.maxAttempts, c.initialBackoff, c.requestTimeout, "GetFlight", func(attemptCtx context.Context) (*flightv1.GetFlightResponse, error) {
		return c.stub.GetFlight(c.withMetadata(attemptCtx), &flightv1.GetFlightRequest{FlightId: flightID.String()})
	})
	if err != nil {
		return nil, err
	}

	flight := fromProtoFlight(resp.GetFlight())
	return &flight, nil
}

func (c *Client) ReserveSeats(ctx context.Context, flightID, bookingID uuid.UUID, seatCount int32) error {
	_, err := callWithRetry(ctx, c.maxAttempts, c.initialBackoff, c.requestTimeout, "ReserveSeats", func(attemptCtx context.Context) (*flightv1.ReserveSeatsResponse, error) {
		return c.stub.ReserveSeats(c.withMetadata(attemptCtx), &flightv1.ReserveSeatsRequest{
			FlightId:  flightID.String(),
			BookingId: bookingID.String(),
			SeatCount: seatCount,
		})
	})
	return err
}

func (c *Client) ReleaseReservation(ctx context.Context, bookingID uuid.UUID) error {
	_, err := callWithRetry(ctx, c.maxAttempts, c.initialBackoff, c.requestTimeout, "ReleaseReservation", func(attemptCtx context.Context) (*flightv1.ReleaseReservationResponse, error) {
		return c.stub.ReleaseReservation(c.withMetadata(attemptCtx), &flightv1.ReleaseReservationRequest{
			BookingId: bookingID.String(),
		})
	})
	return err
}

func (c *Client) withMetadata(ctx context.Context) context.Context {
	if c.apiKey == "" {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("x-api-key", c.apiKey))
}

func callWithRetry[T any](
	ctx context.Context,
	maxAttempts int,
	initialBackoff time.Duration,
	requestTimeout time.Duration,
	operation string,
	fn func(context.Context) (T, error),
) (T, error) {
	var zero T
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if initialBackoff <= 0 {
		initialBackoff = 100 * time.Millisecond
	}

	backoff := initialBackoff
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		resp, err := fn(attemptCtx)
		cancel()
		if err == nil {
			return resp, nil
		}

		st, ok := status.FromError(err)
		if !ok {
			return zero, err
		}

		if !shouldRetry(st.Code()) || attempt == maxAttempts {
			return zero, err
		}

		log.Printf("grpc retry op=%s attempt=%d backoff=%s reason=%s", operation, attempt, backoff, st.Code())
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}

	return zero, nil
}

func shouldRetry(code codes.Code) bool {
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}

func fromProtoFlight(item *flightv1.Flight) Flight {
	return Flight{
		ID:             uuid.MustParse(item.GetId()),
		FlightNumber:   item.GetFlightNumber(),
		AirlineCode:    item.GetAirlineCode(),
		AirlineName:    item.GetAirlineName(),
		Origin:         item.GetOrigin(),
		Destination:    item.GetDestination(),
		DepartureTime:  item.GetDepartureTime().AsTime().UTC(),
		ArrivalTime:    item.GetArrivalTime().AsTime().UTC(),
		TotalSeats:     item.GetTotalSeats(),
		AvailableSeats: item.GetAvailableSeats(),
		PriceKopecks:   item.GetPriceKopecks(),
		Status:         protoFlightStatusToString(item.GetStatus()),
	}
}

func protoFlightStatusToString(statusValue flightv1.FlightStatus) string {
	switch statusValue {
	case flightv1.FlightStatus_FLIGHT_STATUS_SCHEDULED:
		return "SCHEDULED"
	case flightv1.FlightStatus_FLIGHT_STATUS_DEPARTED:
		return "DEPARTED"
	case flightv1.FlightStatus_FLIGHT_STATUS_CANCELLED:
		return "CANCELLED"
	case flightv1.FlightStatus_FLIGHT_STATUS_COMPLETED:
		return "COMPLETED"
	default:
		return "UNSPECIFIED"
	}
}
