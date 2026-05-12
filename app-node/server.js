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

const express = require("express");
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

async function handleSlow(req, res) {
  const span = tracer.startSpan("slow-handler", {}, context.active());
  span.setAttribute("workload", "cpu-heavy");
  const result = cpuIntensiveWork(50_000_000);
  span.setAttribute("result", result);
  span.end();
  res.send("done\n");
}

async function handleFast(req, res) {
  const span = tracer.startSpan("fast-handler", {}, context.active());
  span.setAttribute("workload", "cpu-light");
  const result = cpuIntensiveWork(100_000);
  span.setAttribute("result", result);
  span.end();
  res.send("ok\n");
}

async function handleLeak(req, res) {
  const span = tracer.startSpan("leak-handler", {}, context.active());
  span.setAttribute("workload", "memory-alloc");
  const data = memoryIntensiveWork(1024 * 1024);
  span.setAttribute("allocated_bytes", data.length);
  span.end();
  res.send("allocated\n");
}

function handleHealthz(req, res) {
  res.send("ok\n");
}

// Routes async

// --- App Express ---

const app = express();

// Middleware : span parent pour chaque requête
app.use((req, res, next) => {
  const span = tracer.startSpan(`${req.method} ${req.path}`);
  const ctx = trace.setSpan(context.active(), span);
  res.on("finish", () => {
    span.setAttribute("http.status_code", res.statusCode);
    span.end();
  });
  context.with(ctx, next);
});

app.get("/slow", handleSlow);
app.get("/fast", handleFast);
app.get("/leak", handleLeak);
app.get("/healthz", handleHealthz);

// Gestion des erreurs async
app.use((err, req, res, next) => {
  console.error(err);
  res.status(500).send("internal error\n");
});

app.listen(8081, () => {
  const name = process.env.OTEL_SERVICE_NAME || "demo-profiling-app-node";
  console.log(`Service '${name}' démarré sur :8081`);
  console.log("  GET /slow        — charge CPU élevée (~1-3s)");
  console.log("  GET /fast        — charge CPU faible");
  console.log("  GET /leak        — allocations mémoire");
});

backgroundWork();
