# AIOps

An AI operations layer for an existing proprietary market-data publish/subscribe plant. The design builds on existing Prometheus/Grafana observability and engineers' operational procedures; it does not replace the messaging bus.

- [Initial architecture proposal](docs/ai-layer-architecture.md)
- [Component stack and AI model reference design](docs/production-stack-design.md)
- [Self-contained lab specification](docs/self-contained-lab-spec.md) — proposed demo and evaluation contract; no engineer input required to start
- [Architecture review record](docs/architecture-reviews.md)
- [Run the Go plant lab](lab/README.md) — real synthetic delivery, observability and a read-only web console with durable parallel investigations; rule-based diagnostics, not live AI

Status: architecture plus an initial functional lab increment for review. The live-AI investigation layer, enforced isolation and fleet-capacity qualification are not delivered yet. No production change is authorized.
