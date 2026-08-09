package image

import (
	"fmt"
	"os/exec"

	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
)

// Gate is the image preflight as onboarding runs it: print what was checked, and
// refuse before anything is written.
//
// **A refusal is only real if it happens before the write.** A check that runs
// after apps/<app>/ is generated is a slower version of what the cluster already
// told the tester — so this is called from onboard's finish, above
// gitops.Generate, and it returns an error the caller abandons the run on.
func Gate(app spec.App, opts Options) error {
	render.Section("Image preflight")

	rep := Check(app, opts)
	Print(rep, opts.node())

	if rep.OK() {
		return nil
	}
	if opts.Skip {
		render.Warn(fmt.Sprintf("--skip-image-check: continuing despite %d image(s) that cannot run here",
			len(rep.Findings)))
		render.Warn("the cluster will still refuse to start them — you are only deferring the failure")
		return nil
	}
	return fmt.Errorf("image preflight failed: %d image(s) cannot run on this platform — nothing was written",
		len(rep.Findings))
}

// Print renders a report through the shared CLI look.
//
// Every finding carries its fix on the line below it, indented as a detail.
// "Something is wrong" is what the cluster said, ten minutes and forty-one
// backoff events later; the point of moving the check here is the sentence that
// says what to change.
func Print(rep Report, node Platform) {
	render.Info(fmt.Sprintf("%d check(s) · the cluster's nodes are %s", rep.Checked, node))

	for _, s := range rep.Skipped {
		render.Info("skipped " + s.Service + " · " + s.Ref + " — " + s.Reason)
	}
	for _, f := range rep.Findings {
		render.Error(f.String())
		render.Detail(f.Fix)
	}
	switch {
	case len(rep.Findings) > 0:
		render.Error(fmt.Sprintf("%d image(s) cannot run here", len(rep.Findings)))
	case len(rep.Skipped) > 0:
		// Deliberately not a ✓. Some of these images were not looked at, and a
		// tick over an unasked question is the shape of claim this project keeps
		// writing rules against.
		render.Warn(fmt.Sprintf("nothing blocking, but %d check(s) could not be made", len(rep.Skipped)))
	default:
		render.Success("every image exists and can run on " + node.String())
	}
}

// DefaultInspector is the lookup a command should use: the real one when docker
// is there, and nil when it is not.
//
// nil is a legitimate answer and not a failure. Onboarding is useful on a
// machine with no Docker — writing GitOps files is a git operation — and the
// report says which checks it could not make rather than pretending it made
// them.
func DefaultInspector() Inspector {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	return DockerInspector
}
