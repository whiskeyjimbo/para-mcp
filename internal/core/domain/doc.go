// Package domain defines the core types and interfaces for the paras note system.
//
// It contains the Vault interface (the primary port for note storage), the
// NoteService application logic, and the value types shared across the
// hexagonal architecture: notes, front-matter, filters, queries, ETags, and
// the PARA category taxonomy (Projects, Areas, Resources, Archives).
//
// Nothing in this package imports infrastructure code; all external
// dependencies flow inward through the interfaces defined here.
package domain
