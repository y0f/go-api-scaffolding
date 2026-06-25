package observability

import (
	"context"
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// TelemetryConfig configures tracing and metrics. When OTLPEndpoint is empty
// tracing is disabled but metrics (scraped from the returned registry) stay on.
type TelemetryConfig struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	OTLPEndpoint   string
	SampleRatio    float64
}

// Telemetry owns the metric registry and a single shutdown hook that flushes
// and stops every provider in reverse order of construction.
type Telemetry struct {
	Registry *prometheus.Registry
	shutdown func(context.Context) error
}

func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t.shutdown == nil {
		return nil
	}
	return t.shutdown(ctx)
}

// Setup wires the global tracer and meter providers and the W3C propagator,
// returning a Telemetry whose Registry should back the /metrics endpoint.
func Setup(ctx context.Context, cfg TelemetryConfig) (*Telemetry, error) {
	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
		attribute.String("deployment.environment.name", cfg.Environment),
	))
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	registry := prometheus.NewRegistry()
	promExporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promExporter),
	)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	shutdowns := []func(context.Context) error{meterProvider.Shutdown}

	var tracerProvider *sdktrace.TracerProvider
	if cfg.OTLPEndpoint != "" {
		exporter, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("create otlp trace exporter: %w", err)
		}
		tracerProvider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
			sdktrace.WithBatcher(exporter),
		)
	} else {
		tracerProvider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.NeverSample()),
		)
	}
	otel.SetTracerProvider(tracerProvider)
	shutdowns = append(shutdowns, tracerProvider.Shutdown)

	return &Telemetry{
		Registry: registry,
		shutdown: func(ctx context.Context) error {
			var errs []error
			for i := len(shutdowns) - 1; i >= 0; i-- {
				errs = append(errs, shutdowns[i](ctx))
			}
			return errors.Join(errs...)
		},
	}, nil
}
