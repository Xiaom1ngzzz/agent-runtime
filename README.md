# Agent Runtime Book

A book/repository for designing and documenting an Agent Runtime — the layer between an LLM and a running agent system.

## Structure

- `BOOK.md` — book goals and writing principles.
- `STYLE_GUIDE.md` — writing conventions.
- `ROADMAP.md` — the full outline and progress.
- `adr/` — Architecture Decision Records.
- `chapters/` — book chapters.
- `runtime-go/` — reference runtime in Go (`domain/`, `context/`, `state/`, ...). `go build ./...`.
- `runtime-rs/` — reference runtime in Rust, feature-matched to the Go version. `cargo build`.
- `diagrams/` — architecture and sequence diagrams (source + rendered).

## How to read

Start from `BOOK.md`, then `ROADMAP.md`, then chapters in order.
