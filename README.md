# AIOps

An AI operations layer for an existing proprietary market-data publish/subscribe plant. The design builds on existing Prometheus/Grafana observability and engineers' operational procedures; it does not replace the messaging bus.

- [Initial architecture proposal](docs/ai-layer-architecture.md)
- [Component stack and AI model reference design](docs/production-stack-design.md)
- [Self-contained lab specification](docs/self-contained-lab-spec.md) — proposed demo and evaluation contract; no engineer input required to start
- [Architecture review record](docs/architecture-reviews.md)
- [Run the Go plant lab](lab/README.md) — real synthetic delivery, observability and a read-only web console with durable parallel investigations; rule-based diagnostics, not live AI
- [Exercise bounded agent contracts](lab/README.md#exercise-the-first-ai-contracts-without-a-provider) — LangGraph replay over frozen Go evidence, scoped read tools and exact quantitative-claim checks; no provider calls
- [Exercise isolated specialists](lab/README.md#exercise-the-service-enforced-worker-boundary) — Go-authorized evidence tools, separate Linux sandboxes, durable read/byte budgets and cancellation; scripted workers, not live AI

Status: architecture plus a functional lab and a locally testable worker-isolation boundary. Live models, model-spending reservations, restricted-viewer authorization and fleet-capacity qualification remain undelivered. No production change is authorized.
