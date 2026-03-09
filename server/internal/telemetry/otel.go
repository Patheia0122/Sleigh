package telemetry

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func InitOTEL(ctx context.Context, endpoint, serviceVersion string) (trace.Tracer, func(context.Context) error, error) {
	cleanEndpoint := strings.TrimSpace(endpoint)
	if cleanEndpoint == "" {
		return otel.Tracer("sleigh.sandbox"), nil, nil
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(cleanEndpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, nil, err
	}
	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			attribute.String("service.name", "sleigh-server"),
			attribute.String("service.version", strings.TrimSpace(serviceVersion)),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Tracer("sleigh.sandbox"), tp.Shutdown, nil
}
