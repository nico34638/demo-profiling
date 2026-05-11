package main

import (
	"context"
	"log"
	"math"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "demo-profiling-app"

func initTracer(ctx context.Context) func() {
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint("otel-collector:4317"),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		log.Fatalf("failed to create trace exporter: %v", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			attribute.String("deployment.environment", "demo"),
		)),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	return func() { _ = tp.Shutdown(ctx) }
}

// cpuIntensiveWork force du vrai CPU via math.Sqrt + math.Log (non optimisable par le compilateur)
func cpuIntensiveWork(ctx context.Context, iterations int) float64 {
	_, span := otel.Tracer(serviceName).Start(ctx, "cpuIntensiveWork",
		trace.WithAttributes(attribute.Int("iterations", iterations)),
	)
	defer span.End()

	result := 0.0
	for i := 1; i <= iterations; i++ {
		result += math.Sqrt(float64(i)) * math.Log(float64(i))
	}
	span.SetAttributes(attribute.Float64("result", result))
	return result
}

// memoryIntensiveWork simule des allocations mémoire
func memoryIntensiveWork(ctx context.Context, size int) []byte {
	_, span := otel.Tracer(serviceName).Start(ctx, "memoryIntensiveWork",
		trace.WithAttributes(attribute.Int("size_bytes", size)),
	)
	defer span.End()

	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	return buf
}

func slowHandler(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer(serviceName).Start(r.Context(), "slow-handler",
		trace.WithAttributes(attribute.String("workload", "cpu-heavy")),
	)
	defer span.End()

	cpuIntensiveWork(ctx, 50_000_000)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("done\n"))
}

func fastHandler(w http.ResponseWriter, r *http.Request) {
	_, span := otel.Tracer(serviceName).Start(r.Context(), "fast-handler",
		trace.WithAttributes(attribute.String("workload", "cpu-light")),
	)
	defer span.End()

	cpuIntensiveWork(r.Context(), 100_000)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok\n"))
}

func leakHandler(w http.ResponseWriter, r *http.Request) {
	_, span := otel.Tracer(serviceName).Start(r.Context(), "leak-handler",
		trace.WithAttributes(attribute.String("workload", "memory-alloc")),
	)
	defer span.End()

	data := memoryIntensiveWork(r.Context(), 1024*1024)
	span.SetAttributes(attribute.Int("allocated_bytes", len(data)))
	runtime.KeepAlive(data)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("allocated\n"))
}

// backgroundWork génère du trafic continu pour alimenter les profils eBPF
func backgroundWork(ctx context.Context) {
	tracer := otel.Tracer(serviceName)
	for {
		_, span := tracer.Start(ctx, "background-work")
		switch rand.Intn(3) {
		case 0:
			span.SetAttributes(attribute.String("workload", "cpu-heavy"))
			cpuIntensiveWork(ctx, 25_000_000)
		case 1:
			span.SetAttributes(attribute.String("workload", "cpu-light"))
			cpuIntensiveWork(ctx, 50_000)
		case 2:
			span.SetAttributes(attribute.String("workload", "memory-alloc"))
			data := memoryIntensiveWork(ctx, 512*1024)
			runtime.KeepAlive(data)
		}
		span.End()
		time.Sleep(time.Duration(200+rand.Intn(300)) * time.Millisecond)
	}
}

func main() {
	ctx := context.Background()
	shutdown := initTracer(ctx)
	defer shutdown()

	go backgroundWork(ctx)

	http.Handle("/slow", otelhttp.NewHandler(http.HandlerFunc(slowHandler), "/slow"))
	http.Handle("/fast", otelhttp.NewHandler(http.HandlerFunc(fastHandler), "/fast"))
	http.Handle("/leak", otelhttp.NewHandler(http.HandlerFunc(leakHandler), "/leak"))
	http.Handle("/healthz", otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	}), "/healthz"))

	log.Printf("Service '%s' démarré sur :8080", serviceName)
	log.Println("  GET /slow  — charge CPU élevée (~1-3s)")
	log.Println("  GET /fast  — charge CPU faible")
	log.Println("  GET /leak  — allocations mémoire")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
