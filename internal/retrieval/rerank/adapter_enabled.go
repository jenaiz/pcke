//go:build rerank

package rerank

import (
	"errors"

	"github.com/jenaiz/pcke/internal/retrieval"
)

// Default returns the embedding adapter stub. The stub satisfies the
// Reranker contract but ships no model; callers configure one out-of-
// band (see PRD v5.2 §4.4). Until a model is wired in, Available() is
// false and Reorder is a pass-through — the same behavior as the
// default build but with the symbol surface present for downstream
// callers that detect the build tag at compile time.
//
// Wiring a real model (ONNX, external HTTP, etc.) means replacing
// adapterStub below with an implementation that reads from the
// configured backend and returns ErrModelNotConfigured only when the
// configuration is missing.
func Default() Reranker { return adapterStub{} }

// ErrModelNotConfigured is returned by a real adapter when it cannot
// locate a model file or remote endpoint. It exists here so concrete
// implementations have a stable sentinel to wrap.
var ErrModelNotConfigured = errors.New("rerank: model not configured")

type adapterStub struct{}

func (adapterStub) Available() bool { return false }

func (adapterStub) Reorder(_ string, sections []retrieval.Section) ([]retrieval.Section, error) {
	return sections, nil
}
