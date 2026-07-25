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
//	v0.9.0  the access layer — kind publishes the Istio ingress gateway to the
//	        host, everything the platform exposes moves to one path-based
//	        address, Kiali arrives with the new platform/access module,
//	        `endurance urls` prints those addresses and probes them, and
//	        `endurance status <app>` becomes the post-deploy success screen.
//	        An application asks for its own URL in its spec; the platform
//	        provides the door and does not decide what is behind it
//	v0.9.1  the platform hands over its own two logins — ArgoCD's and
//	        Grafana's, fetched from the cluster — instead of printing the
//	        kubectl commands that fetch them. Narrow by construction: those two
//	        and no others, and ENDURANCE_NO_CREDENTIALS=1 leaves them out of a
//	        transcript that is going to be screenshotted
//	v0.10.0 guided onboarding — `endurance init` sequences doctor, a handful of
//	        prompts with defaults that work, a confirmation screen nothing
//	        happens before, the bootstrap, the onboard and the deploy, and ends
//	        on the real success screen. Plus the developer verbs that make the
//	        platform usable without kubectl: `catalog list|get`, `logs`,
//	        `metrics`, `enable`/`disable ai|slack`, `config list` (presence,
//	        never values) and `uninstall`, which removes the CLI and not the
//	        cluster
//	v0.10.1 what the first live `init` showed: it onboarded without telling
//	        onboard which repo ArgoCD watches, so every Application it generated
//	        had an empty repoURL and was refused by the API server on the last
//	        step of the run. The generator now refuses to write an Application
//	        with no repo URL at all, `init` resolves one from this repo's origin
//	        remote (so a fork deploys from the fork), and a failed apply reports
//	        the fields kubectl named instead of only the headline above them
//	v0.10.2 the questions became navigable. Every prompt was its own one-field
//	        form, so "back" had nothing to go back to: a user who answered Yes to
//	        AI enrichment could not un-answer it, and esc was bound to nothing at
//	        all — leaving ctrl+c, which kills the run, as the only exit. `init`
//	        now asks everything as one form, with the two capability groups
//	        hidden when declined, and internal/prompt owns the keys for every
//	        form in the tool: ↑/shift+tab back, ↓/tab/enter next, esc cancels
//	v0.10.3 what the second live `init` showed, in four parts. An application may
//	        now declare several routes rather than one: the model allowed a
//	        single path, so onboarding a five-service application generated the
//	        one rule it could — `/` to the frontend — and the four API paths the
//	        browser calls stopped existing, with ArgoCD reporting Synced
//	        throughout. `routes:` is a list, sorted longest-path-first so `/`
//	        cannot shadow what is under it, carrying an optional `rewrite`, and
//	        weighted at the gateway when it names a canary service. The
//	        confirmation screen gained Edit beside Create and Cancel, so noticing
//	        a typo no longer costs every other answer. An empty credential now
//	        means the capability was declined — a required-and-empty field is a
//	        group huh will not let you leave backwards, which is what still held
//	        a user at the key prompt after v0.10.2. And the end-of-run screen
//	        hands over ArgoCD's and Grafana's passwords instead of only their
//	        usernames, inside a box that no longer outgrows the terminal
const Current = "v0.10.3"
