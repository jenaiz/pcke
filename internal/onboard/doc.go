// Package onboard generates structured project walkthroughs from the
// knowledge base. It combines architecture, conventions, constraints,
// and module analysis into an interactive tour for new developers.
//
// The engine reads knowledge nodes, relations, and evolution logs to
// produce a [Walkthrough] with ordered sections. Modules are ranked by
// a composite complexity score (see [ScoreModules]).
//
// Configuration is loaded from .pcke/onboarding.toml (see [LoadConfig]).
package onboard
