// Package image is the preflight that refuses an image which cannot run on this
// platform — before a file is written.
//
// # The two failures it exists for
//
// Both come from the first outside run, and neither was caught by anything.
//
// **An image with no manifest for this cluster's architecture.** `bad-app` was
// built on an Apple Silicon MacBook without `--platform`, so it published an
// arm64-only manifest; the kind nodes are linux/amd64. The platform took about
// ten minutes and forty-one backoff events to say so, and the pod's own event
// had said it exactly:
//
//	failed to pull and unpack image "docker.io/dockergc00/bad-app:v2":
//	  no match for platform in manifest: not found
//
// `docker manifest inspect` answers "does this exist, and does it publish a
// manifest for this architecture" in about a second.
//
// **An image the platform's own security defaults make unrunnable.**
// `portfolio-frontend` was stock nginx on port 80, and every Endurance values
// file materialises runAsNonRoot, UID 10001 and all capabilities dropped. A
// process that is not root cannot bind a port below 1024 without
// NET_BIND_SERVICE, which the drop takes away. It exits immediately, every time,
// on any machine.
//
// The second one is the instructive half. The Kyverno gate reported
// `✓ all policies satisfied` and **was right** — the manifest *declares*
// non-root and dropped capabilities, which is exactly what the policy asks. The
// manifest is compliant and the container is doomed, and no static check of the
// YAML can tell the difference, because the fact that decides it is a property
// of the image and not of the manifest. That is why this is a separate gate and
// not another ClusterPolicy.
//
// # Why it runs before the policy gate
//
// So that a doomed image never gets a ✓ printed above its refusal. The policy
// gate saying `all policies satisfied` immediately before a container that
// cannot start is precisely the pair of facts that made the first outside run
// hard to read.
//
// # The registry half is behind an interface, and that is not decoration
//
// Nothing in the test suite may call `docker manifest inspect`. A test that did
// would pass on the laptop this was written on, hang in CI, and fail on a
// machine with no network — which is the exact class of fault that shipped a
// broken v0.11.0 release, when sixteen tests asked the host whether docker, kind
// and kubectl were installed. So the lookup is an [Inspector], the real one is
// the only thing that shells out, and every test injects a fake.
//
// # An unreachable registry is a skip, never a pass
//
// A gate that cannot reach the registry reports that it could not check, in the
// same voice the policy gate uses for a rule it could not evaluate. Reporting a
// pass would make "the image is fine" and "I could not ask" the same sentence,
// and the second one is what a laptop on a train says.
package image

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/gc-ghub/endurance/internal/spec"
)

// PrivilegedPort is the highest port a non-root process cannot bind without
// CAP_NET_BIND_SERVICE. Ports 1-1023 are the privileged range on every Unix.
const PrivilegedPort = 1024

// A Platform is an os/arch pair as a registry describes one.
type Platform struct {
	OS   string
	Arch string
}

func (p Platform) String() string {
	if p.OS == "" && p.Arch == "" {
		return "unknown"
	}
	return p.OS + "/" + p.Arch
}

// Matches reports whether an image published for p can run on want. Only os and
// architecture are compared: a variant mismatch (arm64/v8 against arm64) is not
// a reason a pod fails to start, and refusing on one would refuse images that
// work.
func (p Platform) Matches(want Platform) bool {
	return p.OS == want.OS && p.Arch == want.Arch
}

// NodePlatform is the platform the cluster's nodes run, which is what an image
// has to publish a manifest for.
//
// **linux, not runtime.GOOS.** kind nodes are Linux containers whatever the host
// is, so on the Windows laptop this platform is developed on the answer is
// linux/amd64 and not windows/amd64. Getting that wrong would refuse every image
// in existence on Windows, which is a more embarrassing failure than the one
// being fixed.
//
// The architecture is the machine's, because a kind node is a container on this
// Docker daemon and runs the host's architecture. That is why an arm64-only
// image is fine on the MacBook it was built on and fails here.
func NodePlatform() Platform {
	return Platform{OS: "linux", Arch: runtime.GOARCH}
}

// A Manifest is what a registry publishes for one reference.
type Manifest struct {
	Ref       string
	Platforms []Platform
}

// Publishes reports whether the reference has a manifest for want.
func (m Manifest) Publishes(want Platform) bool {
	for _, p := range m.Platforms {
		if p.Matches(want) {
			return true
		}
	}
	return false
}

// List names the platforms for an error message, so a developer is told what
// their image *does* publish rather than only what it does not.
func (m Manifest) List() string {
	if len(m.Platforms) == 0 {
		return "none"
	}
	out := make([]string, 0, len(m.Platforms))
	for _, p := range m.Platforms {
		out = append(out, p.String())
	}
	return strings.Join(out, ", ")
}

// An Inspector answers what a registry publishes for a reference.
//
// Three outcomes, and they are three different facts: the manifest (nil error),
// the reference genuinely not being there ([NotFoundError]), and not being able
// to ask at all (any other error). Collapsing the last two would turn a laptop
// with no network into a refusal of every image.
type Inspector func(ref string) (Manifest, error)

// NotFoundError is the registry saying the reference does not exist. It blocks;
// every other error skips.
type NotFoundError struct {
	Ref    string
	Detail string
}

func (e NotFoundError) Error() string {
	if e.Detail == "" {
		return e.Ref + ": no such image or tag"
	}
	return e.Ref + ": " + e.Detail
}

// DockerInspector is the real lookup: `docker manifest inspect --verbose`.
//
// --verbose because the plain form returns a manifest *list* with platforms for
// a multi-arch image and a bare single manifest with no platform block for a
// single-arch one — and a single-arch image is exactly the case this check
// exists for, so the form that omits the answer is the wrong form.
func DockerInspector(ref string) (Manifest, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return Manifest{}, fmt.Errorf("docker is not on PATH")
	}
	out, err := exec.Command("docker", "manifest", "inspect", "--verbose", ref).CombinedOutput()
	if err != nil {
		return Manifest{}, classify(ref, string(out), err)
	}
	return parseManifest(ref, out)
}

// notFoundPhrases are what a registry says when the reference is not there. A
// registry that says anything else — unauthorized, a TLS failure, a timeout — is
// a registry that did not answer the question, and that is a skip.
var notFoundPhrases = []string{
	"no such manifest",
	"manifest unknown",
	"not found",
	"does not exist",
	"repository does not exist",
}

func classify(ref, out string, err error) error {
	low := strings.ToLower(out)
	for _, p := range notFoundPhrases {
		if strings.Contains(low, p) {
			return NotFoundError{Ref: ref, Detail: firstLine(out)}
		}
	}
	if line := firstLine(out); line != "" {
		return fmt.Errorf("%s", line)
	}
	return err
}

// verboseEntry is the shape of one element of `docker manifest inspect
// --verbose`. A multi-arch reference returns an array of these; a single-arch
// one returns a single object.
type verboseEntry struct {
	Ref        string `json:"Ref"`
	Descriptor struct {
		Platform struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	} `json:"Descriptor"`
}

func parseManifest(ref string, data []byte) (Manifest, error) {
	m := Manifest{Ref: ref}
	var many []verboseEntry
	if err := json.Unmarshal(data, &many); err == nil {
		for _, e := range many {
			m.Platforms = append(m.Platforms, platformOf(e))
		}
		return m, nil
	}
	var one verboseEntry
	if err := json.Unmarshal(data, &one); err != nil {
		return m, fmt.Errorf("could not read what the registry returned for %s: %w", ref, err)
	}
	m.Platforms = append(m.Platforms, platformOf(one))
	return m, nil
}

func platformOf(e verboseEntry) Platform {
	return Platform{OS: e.Descriptor.Platform.OS, Arch: e.Descriptor.Platform.Architecture}
}

// A Finding is one reason an image cannot run here, with the change that fixes
// it.
//
// Fix is not optional politeness. The exit criterion for this phase says each
// refusal carries a message that says *what to change*, because "something is
// wrong" is what the cluster already said, ten minutes later, in forty-one
// backoff events.
type Finding struct {
	Service string
	Ref     string
	Reason  string
	Fix     string
}

func (f Finding) String() string {
	return "service " + f.Service + " · " + f.Ref + " — " + f.Reason
}

// A Skip is a check that could not be made, so an unevaluated image is visible
// rather than mistaken for a pass.
type Skip struct {
	Service string
	Ref     string
	Reason  string
}

// A Report is the outcome of the preflight.
type Report struct {
	Checked  int // service/check pairs actually evaluated
	Findings []Finding
	Skipped  []Skip
}

// OK reports whether nothing blocking was found.
func (r Report) OK() bool { return len(r.Findings) == 0 }

// Options configures a preflight run.
type Options struct {
	// Node is the platform the cluster's nodes run. Zero means [NodePlatform].
	Node Platform
	// Inspect is the registry lookup. Nil means the registry half does not run
	// and says so — which is what a caller with no docker gets, and what every
	// test gets unless it supplies a fake.
	Inspect Inspector
	// Skip is break glass: report everything and block nothing, exactly as
	// --skip-policy does for the Kyverno gate.
	Skip bool
}

func (o Options) node() Platform {
	if o.Node.OS == "" && o.Node.Arch == "" {
		return NodePlatform()
	}
	return o.Node
}

// Check runs the preflight against a defaulted application spec.
//
// It takes the *defaulted* spec on purpose: the privileged-port rule is about
// the security block ApplyDefaults materialises, and checking the raw input
// would be checking a posture the generated files do not have.
func Check(app spec.App, opts Options) Report {
	var rep Report
	node := opts.node()
	for _, s := range app.Services {
		rep.checkPort(s)
		for _, v := range s.Rollout() {
			rep.checkPlatform(s.Name, s.VersionRef(v), node, opts.Inspect)
		}
	}
	return rep
}

// checkPort is 14.4, and it is two lines because that is all it is.
//
// The knowledge was already in this repo — specs/my-app.yaml, the file `init`
// writes as its own default, uses nginx-unprivileged on 8080, and Phase 11's
// summary records why. It was in the repo as a workaround for one image and
// nowhere as a check for anybody else's.
func (r *Report) checkPort(s spec.Service) {
	r.Checked++
	if s.Port >= PrivilegedPort || !s.Security.RunAsNonRoot {
		return
	}
	r.Findings = append(r.Findings, Finding{
		Service: s.Name,
		Ref:     s.Ref(),
		Reason: fmt.Sprintf(
			"it listens on port %d, and this platform runs every container as UID %d with all "+
				"capabilities dropped — a process that is not root cannot bind a port below %d "+
				"without NET_BIND_SERVICE, so it exits at startup on every machine",
			s.Port, s.Security.RunAsUser, PrivilegedPort),
		Fix: fmt.Sprintf(
			"give the service a port of %d or above. Stock nginx is the usual case and has an "+
				"official unprivileged build: docker.io/nginxinc/nginx-unprivileged listens on 8080. "+
				"Otherwise change the image's own listen port — the security posture is not a per-app knob",
			PrivilegedPort),
	})
}

// checkPlatform is 14.3. It asks the registry, and it is careful about the
// difference between an answer and no answer.
func (r *Report) checkPlatform(service, ref string, node Platform, inspect Inspector) {
	if inspect == nil {
		r.Skipped = append(r.Skipped, Skip{Service: service, Ref: ref,
			Reason: "no registry lookup available — `docker manifest inspect --verbose " + ref +
				"` is what this would have run"})
		return
	}
	r.Checked++
	m, err := inspect(ref)
	if err != nil {
		var nf NotFoundError
		if ok := asNotFound(err, &nf); ok {
			r.Findings = append(r.Findings, Finding{
				Service: service, Ref: ref,
				Reason: "the registry has no such image or tag (" + nf.Error() + ")",
				Fix: "check the spelling and the tag, and that the image was actually pushed — " +
					"a `docker buildx build … --push` run from a directory with no Dockerfile fails " +
					"and leaves the old tag in place, which is how the first outside run spent " +
					"twenty minutes on a v3 that had never existed",
			})
			return
		}
		r.Skipped = append(r.Skipped, Skip{Service: service, Ref: ref,
			Reason: "could not ask the registry: " + err.Error()})
		return
	}
	if m.Publishes(node) {
		return
	}
	r.Findings = append(r.Findings, Finding{
		Service: service, Ref: ref,
		Reason: fmt.Sprintf(
			"it publishes %s and this platform's nodes are %s — the pod would sit in "+
				"ImagePullBackOff with `no match for platform in manifest: not found`",
			m.List(), node),
		Fix: fmt.Sprintf(
			"rebuild it for this architecture: docker buildx build --platform linux/amd64,linux/arm64 "+
				"-t %s --push .  (run from the directory holding the Dockerfile). An image built on an "+
				"Apple Silicon machine without --platform publishes arm64 only", ref),
	})
}

func asNotFound(err error, target *NotFoundError) bool {
	if nf, ok := err.(NotFoundError); ok {
		*target = nf
		return true
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
