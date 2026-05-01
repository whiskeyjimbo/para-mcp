// Package semantic contains the semantic pipeline: embedding, vector storage,
// summarisation, and re-ranking. Each backend must assert port conformance with
// a package-level var _ ports.X = (*ConcreteType)(nil) guard.
package semantic
