package main

import (
	"context"
	"log"
	"math"
	"math/rand"
	"net/http"
	"runtime"
	"time"

	pyroscope "github.com/grafana/pyroscope-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "demo-profiling-app"

func initTracer(ctx context.Context) *sdktrace.TracerProvider {
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint("otel-collector:4317"),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		log.Fatalf("failed to create trace exporter: %v", err)
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion("1.0.0"),
		attribute.String("deployment.environment", "demo"),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	return tp
}

// cpuIntensiveWork simule un calcul intensif en CPU (math.Sqrt + math.Log forcent le CPU)
func cpuIntensiveWork(iterations int) float64 {
	result := 0.0
	for i := 1; i <= iterations; i++ {
		result += math.Sqrt(float64(i)) * math.Log(float64(i))
	}
	return result
}

// memoryIntensiveWork simule des allocations mémoire
func memoryIntensiveWork(size int) []byte {
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	return buf
}

// slowHandler simule une requête lente avec beaucoup de CPU
func slowHandler(w http.ResponseWriter, r *http.Request) {
	tracer := otel.Tracer(serviceName)
	ctx, span := tracer.Start(r.Context(), "slow-handler",
		trace.WithAttributes(attribute.String("http.path", "/slow")),
	)
	defer span.End()

	// Corréler le profil avec le span courant
	spanCtx := span.SpanContext()
	pyroscope.TagWrapper(ctx, pyroscope.Labels(
		"span_id", spanCtx.SpanID().String(),
		"trace_id", spanCtx.TraceID().String(),
		"endpoint", "/slow",
	), func(_ context.Context) {
		_, childSpan := tracer.Start(ctx, "cpu-computation")
		cpuIntensiveWork(50_000_000)
		childSpan.End()
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("done\n"))
}

// fastHandler simule une requête rapide avec peu de CPU
func fastHandler(w http.ResponseWriter, r *http.Request) {
	tracer := otel.Tracer(serviceName)
	ctx, span := tracer.Start(r.Context(), "fast-handler",
		trace.WithAttributes(attribute.String("http.path", "/fast")),
	)
	defer span.End()

	spanCtx := span.SpanContext()
	pyroscope.TagWrapper(ctx, pyroscope.Labels(
		"span_id", spanCtx.SpanID().String(),
		"trace_id", spanCtx.TraceID().String(),
		"endpoint", "/fast",
	), func(_ context.Context) {
		cpuIntensiveWork(100_000)
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok\n"))
}

// leakHandler simule une fuite mémoire progressive
func leakHandler(w http.ResponseWriter, r *http.Request) {
	tracer := otel.Tracer(serviceName)
	ctx, span := tracer.Start(r.Context(), "leak-handler",
		trace.WithAttributes(attribute.String("http.path", "/leak")),
	)
	defer span.End()

	spanCtx := span.SpanContext()
	pyroscope.TagWrapper(ctx, pyroscope.Labels(
		"span_id", spanCtx.SpanID().String(),
		"trace_id", spanCtx.TraceID().String(),
		"endpoint", "/leak",
	), func(_ context.Context) {
		// Simule des allocations (sera collecté par le profil mémoire)
		data := memoryIntensiveWork(1024 * 1024) // 1MB
		runtime.KeepAlive(data)
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("allocated\n"))
}

// backgroundWork génère du trafic en arrière-plan pour les profils continus
func backgroundWork(ctx context.Context) {
	tracer := otel.Tracer(serviceName)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		endpoint := []string{"/slow", "/fast", "/leak"}[rand.Intn(3)]

		_, span := tracer.Start(ctx, "background-work",
			trace.WithAttributes(attribute.String("simulated.endpoint", endpoint)),
		)
		spanCtx := span.SpanContext()

		pyroscope.TagWrapper(ctx, pyroscope.Labels(
			"span_id", spanCtx.SpanID().String(),
			"trace_id", spanCtx.TraceID().String(),
			"source", "background",
		), func(_ context.Context) {
			switch endpoint {
			case "/slow":
				cpuIntensiveWork(5_000_000)
			case "/fast":
				cpuIntensiveWork(50_000)
			case "/leak":
				memoryIntensiveWork(512 * 1024)
			}
		})

		span.End()
		time.Sleep(time.Duration(100+rand.Intn(400)) * time.Millisecond)
	}
}

func main() {
	// Initialise Pyroscope — envoie les profils directement à Pyroscope
	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: serviceName,
		ServerAddress:   "http://pyroscope:4040",
		Logger:          pyroscope.StandardLogger,
		Tags:            map[string]string{"environment": "demo"},
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileGoroutines,
		},
	})
	if err != nil {
		log.Fatalf("failed to start pyroscope: %v", err)
	}

	// Initialise OTel Tracing (envoie les traces vers le collector)
	ctx := context.Background()
	tp := initTracer(ctx)
	defer func() {
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("failed to shutdown tracer: %v", err)
		}
	}()

	// Lance le générateur de trafic en arrière-plan
	go backgroundWork(ctx)

	// Expose les endpoints HTTP
	http.HandleFunc("/slow", slowHandler)
	http.HandleFunc("/fast", fastHandler)
	http.HandleFunc("/leak", leakHandler)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	})

	log.Printf("Service '%s' démarré sur :8080", serviceName)
	log.Println("  GET /slow  — charge CPU élevée")
	log.Println("  GET /fast  — charge CPU faible")
	log.Println("  GET /leak  — allocations mémoire")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
