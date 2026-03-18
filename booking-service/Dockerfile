FROM golang:1.25 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/booking-service ./booking-service/cmd/server

FROM gcr.io/distroless/base-debian12

COPY --from=builder /bin/booking-service /booking-service

EXPOSE 8080

ENTRYPOINT ["/booking-service"]
