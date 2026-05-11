"use strict";

const { NodeSDK } = require("@opentelemetry/sdk-node");
const {
  OTLPTraceExporter,
} = require("@opentelemetry/exporter-trace-otlp-grpc");
const { Resource } = require("@opentelemetry/resources");
const { ATTR_SERVICE_NAME } = require("@opentelemetry/semantic-conventions");

const sdk = new NodeSDK({
  resource: new Resource({
    [ATTR_SERVICE_NAME]:
      process.env.OTEL_SERVICE_NAME || "demo-profiling-app-node",
  }),
  traceExporter: new OTLPTraceExporter({
    url: "grpc://otel-collector:4317",
  }),
});

sdk.start();

process.on("SIGTERM", () => sdk.shutdown().finally(() => process.exit(0)));

const http = require("http");
const { trace, context } = require("@opentelemetry/api");

const tracer = trace.getTracer("demo-profiling-app-node");

// Charge CPU intensive — math pour éviter l'optimisation du moteur V8
function cpuIntensiveWork(iterations) {
  let result = 0;
  for (let i = 1; i <= iterations; i++) {
    result += Math.sqrt(i) * Math.log(i);
  }
  return result;
}

// Allocations mémoire
function memoryIntensiveWork(size) {
  const buf = Buffer.alloc(size);
  for (let i = 0; i < buf.length; i++) {
    buf[i] = i % 256;
  }
  return buf;
}

// Trafic de fond
function backgroundWork() {
  function loop() {
    const span = tracer.startSpan("background-work");
    const ctx = trace.setSpan(context.active(), span);

    context.with(ctx, () => {
      const r = Math.floor(Math.random() * 3);
      if (r === 0) {
        span.setAttribute("workload", "cpu-heavy");
        cpuIntensiveWork(5_000_000);
      } else if (r === 1) {
        span.setAttribute("workload", "cpu-light");
        cpuIntensiveWork(50_000);
      } else {
        span.setAttribute("workload", "memory-alloc");
        const data = memoryIntensiveWork(512 * 1024);
        void data;
      }
      span.end();
    });

    setTimeout(loop, 200 + Math.floor(Math.random() * 300));
  }
  loop();
}

const server = http.createServer((req, res) => {
  const span = tracer.startSpan(`${req.method} ${req.url}`);
  const ctx = trace.setSpan(context.active(), span);

  context.with(ctx, () => {
    if (req.url === "/slow") {
      const child = tracer.startSpan("cpu-computation", {}, ctx);
      child.setAttribute("iterations", 15_000_000);
      const result = cpuIntensiveWork(15_000_000);
      child.setAttribute("result", result);
      child.end();
      span.end();
      res.writeHead(200);
      res.end("done\n");
    } else if (req.url === "/fast") {
      cpuIntensiveWork(50_000);
      span.end();
      res.writeHead(200);
      res.end("ok\n");
    } else if (req.url === "/leak") {
      const data = memoryIntensiveWork(1024 * 1024);
      span.setAttribute("allocated_bytes", data.length);
      span.end();
      res.writeHead(200);
      res.end("allocated\n");
    } else if (req.url === "/healthz") {
      span.end();
      res.writeHead(200);
      res.end("ok\n");
    } else {
      span.end();
      res.writeHead(404);
      res.end("not found\n");
    }
  });
});

server.listen(8081, () => {
  console.log(
    `Service '${process.env.OTEL_SERVICE_NAME || "demo-profiling-app-node"}' démarré sur :8081`,
  );
  console.log("  GET /slow  — charge CPU élevée");
  console.log("  GET /fast  — charge CPU faible");
  console.log("  GET /leak  — allocations mémoire");
});

backgroundWork();
