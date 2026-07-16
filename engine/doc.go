// Package engine hosts the transport-agnostic session engine: the Build
// facade, session lifecycle, and every subsystem a shell needs to run
// conversations (LLM providers, tools, permissions, persistence).
//
// The package contents migrate here in stages from the root module (see
// docs/architecture.md). Until stage S5 lands this package is a placeholder
// so the module skeleton, replace chain, and CI wiring can be verified first.
package engine
