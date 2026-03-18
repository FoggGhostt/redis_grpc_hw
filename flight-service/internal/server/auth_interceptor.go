package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type AuthInterceptor struct {
	apiKey string
}

func NewAuthInterceptor(apiKey string) *AuthInterceptor {
	return &AuthInterceptor{apiKey: apiKey}
}

func (a *AuthInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if err := a.authorize(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func (a *AuthInterceptor) authorize(ctx context.Context) error {
	if a.apiKey == "" {
		return nil
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}

	values := md.Get("x-api-key")
	if len(values) == 0 {
		return status.Error(codes.Unauthenticated, "missing x-api-key")
	}

	if values[0] != a.apiKey {
		return status.Error(codes.Unauthenticated, "invalid x-api-key")
	}

	return nil
}
