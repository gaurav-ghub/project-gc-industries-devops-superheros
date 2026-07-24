// Package version holds the single source of truth for the Endurance CLI
// version, so every banner the tool prints agrees with `endurance version`.
package version

// Current is the released CLI version.
//
//	v0.1.0  CLI foundation + onboarding
//	v0.2.0  release flow (per-service image promotion)
//	v0.3.0  Kyverno policy gate (pre-commit validation of generated manifests)
//	v0.4.0  canary (per-service versions + Istio traffic weights)
//	v0.5.0  developer notifications (CLI intent + ArgoCD outcome)
//	v0.6.0  Endurance rebrand (LaunchPad → Endurance) + ring-ship banner
//	v0.7.0  one output library — Go owns all rendering (live steps, progress,
//	        subprocess detail stream, success screen, URL block; golden-tested)
//	v0.8.0  the single front door — bootstrap / doctor / destroy / platform
//	        status, wrapping the bash modules as framed subprocesses; version
//	        reports every component the platform installs
//	v0.8.1  what the first live bootstrap showed: module end-of-run summaries
//	        silenced under framing (they announced a ready platform three
//	        modules early, and printed live admin passwords), access details
//	        gathered into one block at the end, a thin progress bar in place of
//	        the spinner, steps that resolve in the past tense, red bars for a
//	        teardown
const Current = "v0.8.1"
