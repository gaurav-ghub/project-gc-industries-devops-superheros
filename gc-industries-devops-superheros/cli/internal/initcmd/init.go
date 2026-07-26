// Package initcmd implements `endurance init` — the guided first run.
//
// # What it is
//
// One command that takes somebody who has never seen this platform from a
// cloned repo to their own application running in a browser: welcome, preflight,
// a handful of questions, a confirmation screen, the platform install, the
// onboarding, the deploy, and the success screen. Everything it does, some other
// command already did — `doctor`, `bootstrap`, `onboard`, `status <app>`. Init's
// job is the sequence and the questions, and it adds no installation logic, no
// second generator and no second success screen.
//
// # The two structural decisions
//
// **The phases run in sequence, not nested.** `bootstrap` draws a seven-step
// render.Progress and the deploy draws a two-step one, and they run one after
// the other rather than one inside the other. Nesting was the alternative and it
// is worse in a specific way: a Progress owns a counter, a bar and a verdict, so
// a nested chain means two counters on one line and a bar that means two
// different things at once. What ties the run together instead is a titled rule
// per phase — a rule opens a phase of work, which is exactly the grammar Phase 8
// wrote down.
//
// **Init never asks a question it can answer itself.** The namespace is the
// application's name. The owner is `git config user.name`. The port is the
// image's. Whether to run a bootstrap at all is decided by asking the cluster.
// The URL path defaults to `/`, unless another registered application already
// claimed it, in which case it defaults to `/<app>`. Every prompt that survives
// is one where a stranger genuinely knows something the tool does not.
//
// # The confirmation screen is load-bearing
//
// This command creates a Docker container that will live on the machine, writes
// files into the repo, and commits them. A run that starts doing that before the
// user has seen the list is a run they cannot consent to. So the plan is printed
// in full — what will be created, what will be written, and what is being
// skipped, with skipping named out loud rather than left as an absence — and
// then it asks. `--dry-run` prints exactly that plan and stops.
//
// # The one thing it asks a human to do
//
// It does not push. "Endurance never pushes" is a decision this project made in
// Phase 5 and made structural — `gitops.Commit` works in a repo with no remote —
// and a guided first run is not the place to quietly reverse it. ArgoCD only
// ever sees pushed state, so when the commit init just made is not on the
// branch ArgoCD watches, the deploy step says so and waits: it names the commit,
// names the remote, and prints the one command. Everything else in the run is
// automatic; this is the step where somebody else's credentials are needed, and
// pretending otherwise would mean either a silent failure or a push nobody
// authorised.
package initcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/gc-ghub/endurance/internal/features"
	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/onboard"
	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/prompt"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
	"github.com/gc-ghub/endurance/internal/success"
	"github.com/gc-ghub/endurance/internal/version"
	"gopkg.in/yaml.v3"
)

// The defaults a stranger gets by pressing enter.
//
// The image is not a placeholder. It has to survive this platform's own policy
// gate — registry-qualified, not `:latest`, and able to run as a non-root user
// with every capability dropped — and the ordinary `nginx` image cannot, because
// it binds port 80 and writes to /var/cache as root. nginx-unprivileged is the
// same server built to run as an arbitrary UID on 8080, which is exactly the
// posture Phase 3 materialises into every values file. A default that got
// rejected by the gate three screens later would make the gate look like an
// obstacle rather than the point.
const (
	DefaultName  = "my-app"
	DefaultImage = "docker.io/nginxinc/nginx-unprivileged"
	DefaultTag   = "stable-alpine"
	DefaultPort  = 8080
)

// waitPoll is how often the deploy step asks ArgoCD where it has got to, and
// DefaultTimeout is how long it keeps asking.
//
// Six minutes because that is comfortably longer than a first sync of a
// single-service application and comfortably shorter than the patience of
// somebody watching a terminal. A timeout is not a failure: it ends on the
// success screen with whatever was actually observed, which for a run still
// waiting on a push is exactly the right screen.
const (
	waitPoll       = 5 * time.Second
	DefaultTimeout = 6 * time.Minute
)

// Options configures a guided first run.
type Options struct {
	Root string

	// The answers, when they come from flags instead of prompts. Anything left
	// empty is asked for, or defaulted. Supplying all of them plus Yes is what
	// makes init runnable without a terminal.
	Name    string
	Image   string
	Tag     string
	Owner   string
	Repo    string
	Path    string
	Port    int
	NoRoute bool

	// GitopsRepo is the URL of the platform repo ArgoCD watches — not the
	// developer's application source repo above. Empty means "work it out":
	// this repo's origin remote, which is the right answer for the stranger who
	// forked it, falling back to gitops.DefaultRepo.
	GitopsRepo string

	Yes           bool // accept the plan without asking
	DryRun        bool // print the plan and stop
	SkipBootstrap bool // the platform is already up and you know it
	NoWait        bool // do not wait for ArgoCD; end on whatever is running now
	Timeout       time.Duration

	// The edges of the world. Tests replace them; the CLI never sets any.
	//
	// Ask covers every question, credentials included — they are one form now,
	// so there is one seam rather than a second one for the masked fields.
	//
	// Doctor was the edge nobody noticed was an edge. The real one asks *this*
	// machine whether docker, kind, kubectl, helm and istioctl are installed, so
	// every test that left it alone passed or failed according to what happened
	// to be on the laptop running it. The whole suite was green on the machine
	// this was written on and red the first time it ran anywhere else — which
	// was the v0.11.0 release workflow, on a runner with none of those tools.
	// A test that asks the host a question it does not control is not a test.
	Confirm   func(question string) (Decision, error)
	Ask       func(a *Answers, defaults Answers) error
	Doctor    func(platform.DoctorOptions) error
	Kubectl   func(args ...string) (string, error)
	Bootstrap func(platform.BootstrapOptions) error
	Onboard   func(onboard.Options) error
	Inspect   func(root string) platform.ClusterState
	Health    func(root string) platform.Health
	Now       func() time.Time
	sleep     func(time.Duration)
}

// A Decision is what the user chose at the confirmation screen.
//
// Three answers, not two, because "no" was hiding two different intentions. A
// user who reads the plan and finds one wrong line wants that line changed, not
// the run abandoned — and until v0.10.3 the only way to change it was to cancel,
// lose every other answer, and start again. Edit sends them back to the same
// form with everything they typed still in it.
type Decision int

const (
	// Create is the plan accepted as printed.
	Create Decision = iota
	// Edit reopens the questions with the current answers filled in.
	Edit
	// Cancel is esc, ctrl+c, or choosing Cancel. Nothing is created.
	Cancel
)

// Answers is everything init needs from a human.
//
// The two secrets are held here for the length of the run and written to their
// git-ignored files before the bootstrap starts, because that is when the bash
// modules read them. They are never printed, never logged, and never put in an
// error message — see internal/features for why that rule is stricter here than
// it is for ArgoCD's and Grafana's own logins.
type Answers struct {
	Name  string
	Image string
	Tag   string
	Port  int
	Owner string
	Repo  string
	Path  string
	Route bool

	AI          bool
	OpenAIKey   string
	AISlackHook string

	Slack     bool
	SlackHook string
}

// Run is the guided first run.
func Run(opts Options) error {
	render.Banner(version.Current)

	root, err := platform.FindRoot(opts.Root)
	if err != nil {
		return err
	}
	if opts.Timeout == 0 {
		opts.Timeout = DefaultTimeout
	}

	welcome(root)

	// 1 · the machine. Same checks bootstrap gates on, run here so a missing
	// tool is found before anybody has answered a question about their app.
	doctor := opts.Doctor
	if doctor == nil {
		doctor = platform.Doctor
	}
	render.Section("1 of 4 · Checking this machine")
	if err := doctor(platform.DoctorOptions{Root: root, NoBanner: true}); err != nil {
		render.Blank()
		render.Info("`endurance init` needs all of these — fix what is named above and run it again")
		return err
	}

	cluster := inspect(opts, root)
	if err := refuseUnreachableCluster(cluster); err != nil {
		return err
	}
	health := checkHealth(opts, root)
	needBootstrap := !opts.SkipBootstrap && !(cluster.Exists && cluster.Published && health.Complete())

	// 2 · the application.
	render.Section("2 of 4 · Your application")
	answers, err := collect(opts, root)
	if err != nil {
		return err
	}

	// Plan, read, amend, plan again. The loop is the point: the confirmation
	// screen is the first time the answers are visible together, which makes it
	// the first place a wrong one is obvious, so it has to be a place the user
	// can act on that rather than only accept or abandon.
	for {
		// An application that already has a spec file is onboarded from that
		// file, not from the answers — writeSpec leaves it alone, because the
		// input is the user's. So the plan has to describe the spec, or the
		// screen consented to is not the run that happens: "1 service,
		// nginx-unprivileged" above a re-run of a five-service application is
		// exactly the kind of untruth this screen exists to prevent.
		existing, reusing := existingSpec(root, answers.Name)

		plan := buildPlan(root, cluster, health, answers, needBootstrap, opts, existing, reusing)
		printPlan(plan)

		if opts.DryRun {
			render.Blank()
			render.Info("--dry-run · nothing was created, written or committed")
			render.Info("drop --dry-run to run it")
			return nil
		}
		if opts.Yes {
			break
		}
		confirm := opts.Confirm
		if confirm == nil {
			confirm = askConfirm
		}
		decision, err := confirm("Go ahead?")
		if err != nil {
			return err
		}
		if decision == Cancel {
			render.Info("nothing was created — no cluster, no files, no commit")
			return nil
		}
		if decision == Create {
			break
		}
		if answers, err = reask(opts, root, answers); err != nil {
			return err
		}
	}

	// The optional credentials go to disk before the bootstrap, because the bash
	// modules read those two files while they install. Written first, applied by
	// the module, never printed.
	if err := writeSecrets(root, answers, opts); err != nil {
		return err
	}

	// 3 · the platform.
	render.Section("3 of 4 · The platform")
	if needBootstrap {
		bootstrap := opts.Bootstrap
		if bootstrap == nil {
			bootstrap = platform.Bootstrap
		}
		// The preflight already ran, as step 1 of this command. Running it twice
		// would print the same eight checks twice in one transcript.
		if err := bootstrap(platform.BootstrapOptions{
			Root: root, NoBanner: true, SkipPreflight: true,
		}); err != nil {
			return err
		}
	} else {
		render.Info("the platform is already installed and healthy — nothing to do")
		render.Detail(fmt.Sprintf("cluster %s · %d of %d components ready",
			cluster.Name, health.Ready, health.Total))
		render.Detail("`endurance bootstrap` re-runs the modules if you want them re-run")
	}

	// 4 · the application, onboarded and deployed.
	render.Section("4 of 4 · " + answers.Name)
	if err := writeSpec(root, answers); err != nil {
		return err
	}
	run := opts.Onboard
	if run == nil {
		run = onboard.Run
	}
	if err := run(onboard.Options{
		Root:       root,
		GitopsRepo: gitopsRepo(root, opts),
		From:       gitops.SpecPath(root, answers.Name),
		Commit:     true,
		NoBanner:   true,
	}); err != nil {
		return err
	}

	deployed, err := deploy(root, answers, opts)
	if err != nil {
		return err
	}

	// The last screen is the real one, from real pod state — the same function
	// `endurance status <app>` calls. An application still syncing says so.
	//
	// The same kubectl the deploy used, so the whole run asks one cluster: a
	// screen that resolved its own would be a second answer to the question the
	// step above just asked.
	render.Section("Your application")
	if err := success.Screen(success.Options{
		Root: root, App: answers.Name, Kubectl: opts.Kubectl,
	}); err != nil {
		return err
	}
	// The closing line is a claim, so it is only made when there is something to
	// claim. A run that ended waiting for a push has already said what it is
	// waiting for, and a ✓ under it would be arguing with the screen above.
	if deployed {
		render.Blank()
		render.Success("that is the whole platform — `endurance urls` reopens the dashboards")
	}
	return nil
}

func welcome(root string) {
	render.Section("Welcome to Endurance")
	render.Info("this one command stands up a Kubernetes platform on your laptop and")
	render.Detail("deploys an application onto it. No cloud account, no cost, nothing to sign up for.")
	render.Blank()
	render.Info("it will ask a few questions, then show you exactly what it is going to do")
	render.Detail("before it does any of it. Nothing is created until you say yes.")
	render.Blank()
	render.Info("everything it installs ends up on one address: " + render.Value(platform.BaseURL(root)))
	render.Detail("no port-forward, no second terminal, nothing to keep running")
	// The repo path is not printed here on purpose: the preflight two lines
	// below reports it as a check, and saying it twice in one screen is how a
	// welcome becomes a wall.
}

func inspect(opts Options, root string) platform.ClusterState {
	f := opts.Inspect
	if f == nil {
		f = platform.InspectCluster
	}
	return f(root)
}

func checkHealth(opts Options, root string) platform.Health {
	f := opts.Health
	if f == nil {
		f = platform.CheckHealth
	}
	return f(root)
}

// refuseUnreachableCluster is the Phase 10 trap, caught before ten minutes are
// spent falling into it.
//
// kind fixes extraPortMappings at cluster-creation time. A cluster created
// before the access layer existed installs all seven modules perfectly, reports
// every component healthy, and answers nothing on the host — so a first run that
// carried on would end by handing a stranger five dead links and a success
// screen. There is no fix but a recreate, and init refuses rather than
// pretending there might be.
func refuseUnreachableCluster(c platform.ClusterState) error {
	if !c.Exists || c.Published {
		return nil
	}
	render.Blank()
	render.Error("the kind cluster " + render.Value(c.Name) + " does not publish the platform's host port")
	render.Detail(fmt.Sprintf("nothing on this machine can reach it on port %d", c.HostPort))
	render.Detail("kind fixes port mappings when a cluster is created, so this one cannot be upgraded")
	render.Blank()
	render.Info("delete it and let init build a new one:")
	render.Detail("endurance destroy   then   endurance init")
	return fmt.Errorf("the existing cluster %q predates the access layer and has to be recreated", c.Name)
}

// --- the questions ---

// collect fills in everything init needs: flags first, then defaults it can
// work out for itself, then prompts for what is genuinely left.
func collect(opts Options, root string) (Answers, error) {
	defaults := Answers{
		Name:  firstNonEmpty(opts.Name, DefaultName),
		Image: firstNonEmpty(opts.Image, DefaultImage),
		Tag:   firstNonEmpty(opts.Tag, DefaultTag),
		Port:  DefaultPort,
		Owner: firstNonEmpty(opts.Owner, gitUser(root)),
		Repo:  opts.Repo,
		Route: !opts.NoRoute,
	}
	if opts.Port > 0 {
		defaults.Port = opts.Port
	}
	defaults.Path = firstNonEmpty(opts.Path, freePath(root, defaults.Name))

	a := defaults

	// --name is what decides whether there are questions at all: somebody who
	// named their application on the command line has already answered the one
	// thing init cannot default. --yes is a separate decision about the
	// confirmation, so `--name demo` alone still shows the plan and still asks.
	if opts.Name != "" {
		render.Info("--name was given, so nothing is asked — the plan below is the whole of it")
		render.Detail("anything not given on the command line took its default")
		return finalise(root, a)
	}

	ask := opts.Ask
	if ask == nil {
		ask = askApplication
	}
	if err := ask(&a, defaults); err != nil {
		return a, err
	}
	// The path default depends on the name, so it is recomputed once the name is
	// known — otherwise a user who renamed the app at the prompt gets a default
	// path computed for a name they did not choose.
	if opts.Path == "" && a.Name != defaults.Name {
		a.Path = freePath(root, a.Name)
	}
	return finalise(root, a)
}

// finalise fills the last of the blanks and validates before anything is shown
// as a plan. A plan that names a path the gate will refuse is a plan that lies.
func finalise(root string, a Answers) (Answers, error) {
	if a.Port <= 0 {
		a.Port = DefaultPort
	}
	if a.Route && a.Path == "" {
		a.Path = freePath(root, a.Name)
	}
	app := toSpec(a)
	app.ApplyDefaults()
	if err := app.Validate(); err != nil {
		return a, err
	}
	return a, nil
}

// askApplication is every question init asks, as one form.
//
// One form, not eight, and that is the whole point. Each question used to be
// its own huh.Form, which meant "back" had nothing to go back to: a user who
// answered Yes to AI enrichment by accident could not un-answer it, because
// that form had already closed and the key prompt was a new one. Their only
// exit was ctrl+c, which kills a run that has not created anything yet but does
// not say so.
//
// Groups are what make it navigable. The two capability groups are hidden when
// their confirm says no, so declining still costs one keystroke and never shows
// a prompt for a credential the user does not want to give — and pressing ↑ at
// the key prompt goes back to the confirm that opened it, where Skip closes the
// group again. See [prompt] for the keys.
func askApplication(a *Answers, d Answers) error {
	if !render.Default().IsTTY() {
		return fmt.Errorf("`endurance init` asks questions and this is not a terminal — " +
			"pass --name/--image/--tag/--port and --yes to run it without prompts")
	}
	render.Detail(prompt.Hint)
	render.Detail("answer them all · the plan at the end has an Edit option, so nothing here is final")
	render.Blank()

	port := strconv.Itoa(d.Port)
	form := prompt.Form(
		huh.NewGroup(
			huh.NewNote().Title("Your application").
				Description("press enter to take the default · everything here can be changed later in specs/<app>.yaml"),
			huh.NewInput().Title("Name").
				Description("lowercase, DNS-safe · it becomes the namespace too").
				Value(&a.Name).Validate(validName),
			huh.NewInput().Title("Container image").
				Description("with its registry · the policy gate matches the literal string against docker.io/*").
				Value(&a.Image).Validate(nonEmpty("image")),
			huh.NewInput().Title("Tag").
				Description("`latest` is refused by the platform's own policy — pin it").
				Value(&a.Tag).Validate(validTag),
			huh.NewInput().Title("Container port").
				Value(&port).Validate(validPort),
			huh.NewInput().Title("URL path").
				Description("where it answers on http://localhost:8080 · leave as / for the root").
				Value(&a.Path).Validate(validPath),
			huh.NewInput().Title("Source repository").
				Description("optional · recorded in the registry entry, nothing is cloned from it").
				Value(&a.Repo),
		),

		// The two capabilities. Asked as a yes/no first so that declining costs
		// one keystroke, and so the confirmation screen can say "skipped" about
		// a decision the user made rather than a prompt they never saw.
		huh.NewGroup(
			huh.NewConfirm().Title("Enable AI alert enrichment?").
				Description("a model explains Prometheus alerts before they reach Slack · needs an OpenAI API key, which costs money").
				Affirmative("Yes").Negative("Skip").
				Value(&a.AI),
		),
		huh.NewGroup(
			features.SecretField("OpenAI API key",
				"masked · leave it empty to skip enrichment · written to platform/ai/secret.yaml, "+
					"which is git-ignored, and never printed back",
				false, &a.OpenAIKey),
			features.SecretField("Slack incoming-webhook URL for enriched alerts",
				"masked · optional, leave empty to enrich into the logs only",
				false, &a.AISlackHook).Validate(optionalWebhook),
		).WithHideFunc(func() bool { return !a.AI }),

		huh.NewGroup(
			huh.NewConfirm().Title("Send deploy notifications to Slack?").
				Description("ArgoCD posts deploying / healthy / failed · needs a Slack incoming-webhook URL").
				Affirmative("Yes").Negative("Skip").
				Value(&a.Slack),
		),
		huh.NewGroup(
			features.SecretField("Slack incoming-webhook URL",
				"masked · leave it empty to skip notifications · written to "+
					"platform/gitops/argocd/values.slack.yaml, which is git-ignored",
				false, &a.SlackHook).Validate(optionalWebhook),
		).WithHideFunc(func() bool { return !a.Slack }),
	)
	if err := form.Run(); err != nil {
		return err
	}
	a.Port, _ = strconv.Atoi(port)
	settle(a)
	return nil
}

// settle reconciles the capability switches with the credentials actually given.
//
// Split out of the form so it can be tested: a form cannot be driven without a
// terminal, and both of the bugs the live runs found were in code no test could
// reach. This is the part with a rule in it, so this is the part that gets one.
//
// # Why an empty credential turns its capability off
//
// Neither secret field is marked required, which is not laxness — it is the only
// way out of the group. huh refuses to leave a group *backwards* while any field
// in it has a validation error (Form.Update, prevGroupMsg), and moving off a
// field runs its validator. A required first field that is still empty is
// therefore a trap with no exit: ↑ blurs it, "required" appears at the bottom of
// the screen, and the keypress is swallowed. A user who said Yes to AI
// enrichment by accident was held at the key prompt by the very validator meant
// to help them, with ctrl+c the only way out.
//
// So empty means skipped. The plan says so out loud, and `endurance enable ai`
// is still there tomorrow.
func settle(a *Answers) {
	// A hidden group's fields keep whatever they were given before it was
	// hidden. Somebody who typed a key, went back with ↑ and chose Skip must end
	// up with no key — the plan says "skipping", and the plan must be true.
	if !a.AI || strings.TrimSpace(a.OpenAIKey) == "" {
		a.AI, a.OpenAIKey, a.AISlackHook = false, "", ""
	}
	if !a.Slack || strings.TrimSpace(a.SlackHook) == "" {
		a.Slack, a.SlackHook = false, ""
	}
}

// optionalWebhook validates a webhook that is allowed to be empty. The required
// ones use features.ValidateWebhook directly.
func optionalWebhook(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return features.ValidateWebhook(s)
}

// askConfirm is the last question, and the only one with three answers.
//
// A Select rather than a Confirm because there are three: huh's Confirm is a
// two-way toggle and Edit is not the negative of Create. Select also brings its
// own ↑/↓, which is why [prompt] leaves that field's keys exactly as huh ships
// them.
func askConfirm(question string) (Decision, error) {
	if !render.Default().IsTTY() {
		return Cancel, fmt.Errorf("init needs confirmation and this is not a terminal — re-run with --yes")
	}
	answer := Create
	err := prompt.Run(huh.NewSelect[Decision]().
		Title(question).
		Description("everything above · the cluster is deletable with `endurance destroy`").
		Options(
			huh.NewOption("Create it", Create),
			huh.NewOption("Edit answers", Edit),
			huh.NewOption("Cancel", Cancel),
		).
		Value(&answer))
	if prompt.Cancelled(err) {
		return Cancel, nil // esc at the confirmation is Cancel, not a crash
	}
	return answer, err
}

// reask reopens the questions with the current answers in them.
//
// The answers are passed as the defaults as well as the values, so every field
// arrives holding what the user last said rather than what init would have
// guessed — the point of Edit is to change one line without retyping the other
// six.
func reask(opts Options, root string, a Answers) (Answers, error) {
	ask := opts.Ask
	if ask == nil {
		ask = askApplication
	}
	edited := a
	if err := ask(&edited, a); err != nil {
		return a, err
	}
	return finalise(root, edited)
}

// --- the confirmation screen ---

// A Plan is what init is about to do, in the order it will do it.
//
// Will and Skip are separate lists rather than one list with a flag, because
// they are read differently: Will is what the user is consenting to, and Skip
// is what they are being told did not happen quietly.
type Plan struct {
	Will []Action
	Skip []string
	Warn []string
}

// An Action is one thing that will happen, and the detail under it.
type Action struct {
	Summary string
	Details []string
}

// existingSpec reads specs/<app>.yaml when the application already has one.
//
// It is read for the plan, not for the run: `onboard --from` reads the same file
// a moment later. The point is that the screen the user consents to describes
// the file that will actually be used.
func existingSpec(root, name string) (spec.App, bool) {
	var app spec.App
	data, err := os.ReadFile(gitops.SpecPath(root, name))
	if err != nil {
		return app, false
	}
	if err := yaml.Unmarshal(data, &app); err != nil {
		return app, false
	}
	if app.Namespace == "" {
		app.Namespace = app.Name
	}
	app.ApplyDefaults()
	return app, app.Name != ""
}

// buildPlan turns the answers into the screen the user consents to.
//
// existing/reusing describe a spec file the application already has, which
// supersedes the answers for everything except the parts of the run that are not
// about the application.
func buildPlan(root string, cluster platform.ClusterState, health platform.Health,
	a Answers, needBootstrap bool, opts Options, existing spec.App, reusing bool) Plan {

	var p Plan
	base := platform.BaseURL(root)

	if needBootstrap {
		what := "Create the kind cluster " + cluster.Name + " and install the platform into it"
		if cluster.Exists {
			what = "Install the platform into the existing kind cluster " + cluster.Name
		}
		details := []string{
			fmt.Sprintf("%d modules · Istio, monitoring, AI enrichment, ArgoCD, Kyverno, access layer",
				len(platform.Chain)),
			"a Docker container that stays on this machine until `endurance destroy`",
			"about ten minutes",
		}
		if cluster.Exists && health.Reachable {
			details = append(details,
				fmt.Sprintf("%d of %d components are already healthy — the modules are safe to re-run",
					health.Ready, health.Total))
		}
		p.Will = append(p.Will, Action{Summary: what, Details: details})
	} else {
		p.Skip = append(p.Skip, "installing the platform — the cluster "+cluster.Name+
			" is up and all "+strconv.Itoa(health.Total)+" components are healthy")
	}

	summary := "Register " + a.Name + " and generate its GitOps files"
	var appDetails []string

	if reusing {
		// The spec on disk is what onboard will read, so it is what the plan
		// describes. Everything the answers said about the application is
		// irrelevant here, and printing it anyway would describe a different run.
		summary = "Re-register " + existing.Name + " from the spec it already has"
		appDetails = []string{
			plural(len(existing.Services), "service") + " · " +
				strings.Join(existing.ServiceNames(), ", "),
			"namespace " + existing.Namespace + " · owner " + orNone(existing.Owner),
		}
		if u := existing.URL(base); u != "" {
			appDetails = append(appDetails, "reachable at "+u+" via "+existing.Route.Service)
		}
		if c := existing.CanaryServices(); len(c) > 0 {
			appDetails = append(appDetails, "canary: "+strings.Join(c, ", "))
		}
		appDetails = append(appDetails,
			"specs/"+existing.Name+".yaml already exists and is left alone — it is the input, and it is yours",
			"apps/"+existing.Name+"/{app,application,values}.yaml is regenerated from it",
			"the manifests are checked against this platform's Kyverno policies first")
	} else {
		url := ""
		if a.Route {
			url = base + a.Path
			if a.Path == "/" {
				url = base + "/"
			}
		}
		appDetails = []string{
			"1 service · " + a.Image + ":" + a.Tag + " on port " + strconv.Itoa(a.Port),
			"namespace " + a.Name + " · owner " + orNone(a.Owner),
		}
		if url != "" {
			appDetails = append(appDetails, "reachable at "+url+" via "+a.Name)
		}
		appDetails = append(appDetails,
			"writes specs/"+a.Name+".yaml and apps/"+a.Name+"/{app,application,values}.yaml",
			"the manifests are checked against this platform's Kyverno policies first")
	}
	p.Will = append(p.Will, Action{Summary: summary, Details: appDetails})
	p.Will = append(p.Will, Action{
		Summary: "Commit those files to this repo",
		Details: []string{
			"Endurance never pushes — that stays yours",
			"ArgoCD only ever sees pushed state, which is why the next step may wait for you",
		},
	})
	deployDetails := []string{
		"applies apps/" + a.Name + "/application.yaml, the ArgoCD Application",
		"ArgoCD deploys it · nothing here applies a workload",
	}
	if opts.NoWait {
		deployDetails = append(deployDetails, "--no-wait · the screen shows whatever is running at that moment")
	} else {
		deployDetails = append(deployDetails,
			"waits up to "+opts.Timeout.String()+" for it to sync, then prints the success screen")
	}
	p.Will = append(p.Will, Action{Summary: "Hand " + a.Name + " to ArgoCD", Details: deployDetails})

	if a.AI {
		p.Will = append(p.Will, Action{
			Summary: "Enable AI alert enrichment",
			Details: []string{
				"writes platform/ai/secret.yaml — git-ignored, owner-readable, never printed back",
				"the key you gave is not shown again by any Endurance command",
			},
		})
	} else {
		p.Skip = append(p.Skip, "AI alert enrichment — no OpenAI key given · `endurance enable ai` later")
	}
	if a.Slack {
		p.Will = append(p.Will, Action{
			Summary: "Enable Slack deploy notifications",
			Details: []string{"writes platform/gitops/argocd/values.slack.yaml — git-ignored, never printed back"},
		})
	} else {
		p.Skip = append(p.Skip, "Slack notifications — no webhook given · `endurance enable slack` later")
	}
	if !a.Route {
		p.Skip = append(p.Skip, "a URL — the application is reachable inside the cluster only")
	}

	if needBootstrap {
		p.Warn = append(p.Warn, "this creates a Docker container and it outlives this command · `endurance destroy` removes it")
	}
	if _, declared := platform.HostPort(root); !declared {
		p.Warn = append(p.Warn, "kind-config.yaml does not declare the host port — the addresses above are a guess")
	}
	return p
}

// printPlan draws the confirmation screen.
//
// ▸ for what will happen, · for what will not, ⚠ for what to think about. No new
// glyph and no box: a box closes a run, and this is a run that has not started.
// A ✓ here would be the exact untruth this project keeps writing rules about —
// nothing on this screen has been observed, because none of it has happened.
func printPlan(p Plan) {
	render.Section("This is what will happen")
	for _, a := range p.Will {
		render.Step(a.Summary)
		for _, d := range a.Details {
			render.Detail(d)
		}
	}
	if len(p.Skip) > 0 {
		render.Blank()
		for _, s := range p.Skip {
			render.Info("skipping: " + s)
		}
	}
	if len(p.Warn) > 0 {
		render.Blank()
		for _, w := range p.Warn {
			render.Warn(w)
		}
	}
	render.Blank()
}

// --- doing it ---

// writeSecrets puts the optional credentials on disk before the bootstrap runs.
//
// Order matters and is the reason this is not simply `endurance enable` called
// twice: platform/ai/install.sh applies secret.yaml if it finds one, and
// platform/gitops/argocd/install.sh passes values.slack.yaml to helm if it finds
// one. Written first, they are picked up by the modules that own them, and init
// installs nothing itself.
func writeSecrets(root string, a Answers, opts Options) error {
	when := now(opts).Format("2006-01-02")
	if a.AI {
		if _, err := features.WriteAI(root, a.OpenAIKey, a.AISlackHook, when); err != nil {
			return fmt.Errorf("writing the AI credentials: %w", err)
		}
		render.Success("wrote " + features.AISecretFile + " · git-ignored, and not printed back")
	}
	if a.Slack {
		if _, err := features.WriteSlack(root, a.SlackHook, when); err != nil {
			return fmt.Errorf("writing the Slack credentials: %w", err)
		}
		render.Success("wrote " + features.SlackFile + " · git-ignored, and not printed back")
	}
	return nil
}

// writeSpec writes specs/<app>.yaml — the durable input `onboard --from` reads.
//
// Init generates the input rather than the output, which is what makes the run
// repeatable: apps/<app>/ is regenerated from this file every time, so a fix
// applied there does not survive and a fix applied here does. It is also what
// lets init compose `onboard` instead of reimplementing the generator.
func writeSpec(root string, a Answers) error {
	path := gitops.SpecPath(root, a.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		render.Info("specs/" + a.Name + ".yaml already exists — leaving it alone and onboarding from it")
		return nil
	}
	if err := os.WriteFile(path, []byte(specYAML(a)), 0o644); err != nil {
		return err
	}
	render.Success("wrote specs/" + a.Name + ".yaml")
	return nil
}

// specYAML renders the spec file with the comments that make it editable.
//
// The comments are the point. This is the file the user comes back to when they
// want a second service or a different tag, and a bare four-line YAML document
// teaches them nothing about what else it could say.
func specYAML(a Answers) string {
	var b strings.Builder
	b.WriteString("# " + a.Name + " — the INPUT to `endurance onboard --from`.\n")
	b.WriteString("#\n")
	b.WriteString("# Written by `endurance init`. apps/" + a.Name + "/ is generated from this file\n")
	b.WriteString("# and overwritten on every onboard, so edit it here, not there:\n")
	b.WriteString("#\n")
	b.WriteString("#   endurance onboard --root . --from specs/" + a.Name + ".yaml\n")
	b.WriteString("#\n")
	b.WriteString("# resources and security are omitted on purpose — Endurance materialises its\n")
	b.WriteString("# platform defaults (non-root UID 10001, all capabilities dropped, CPU/memory\n")
	b.WriteString("# requests and limits) into apps/" + a.Name + "/values.yaml, which is what the\n")
	b.WriteString("# Kyverno gate then validates. Set them here only to override a default.\n\n")
	b.WriteString("name: " + a.Name + "\n")
	b.WriteString("namespace: " + a.Name + "\n")
	if a.Repo != "" {
		b.WriteString("repository: " + a.Repo + "\n")
	}
	if a.Owner != "" {
		b.WriteString("owner: " + yamlScalar(a.Owner) + "\n")
	}
	if a.Route {
		b.WriteString("\n# The application's public address. The platform owns one Istio Gateway and\n")
		b.WriteString("# deliberately knows nothing about what is behind any path on it — an\n")
		b.WriteString("# application asks, here, and charts/app renders a VirtualService bound to it.\n")
		b.WriteString("# `port` is omitted so it is read from the named service and cannot drift.\n")
		b.WriteString("# /argocd, /kiali, /grafana, /prometheus and /alertmanager are the platform's.\n")
		b.WriteString("route:\n")
		b.WriteString("  enabled: true\n")
		b.WriteString("  path: " + a.Path + "\n")
		b.WriteString("  service: " + a.Name + "\n")
	}
	b.WriteString("\n# One service. An application is a set of independently-deployable services;\n")
	b.WriteString("# this is the N=1 case. Add another entry here and re-run onboard to grow it.\n")
	b.WriteString("services:\n")
	b.WriteString("  - name: " + a.Name + "\n")
	b.WriteString("    image: " + a.Image + "\n")
	b.WriteString("    tag: " + a.Tag + "\n")
	b.WriteString("    port: " + strconv.Itoa(a.Port) + "\n")
	b.WriteString("    replicas: 1\n")
	return b.String()
}

// toSpec is the in-memory equivalent, for validating the answers before the
// plan is printed.
func toSpec(a Answers) spec.App {
	app := spec.App{
		Name:       a.Name,
		Namespace:  a.Name,
		Repository: a.Repo,
		Owner:      a.Owner,
		Services: []spec.Service{{
			Name: a.Name, Image: a.Image, Tag: a.Tag, Port: a.Port, Replicas: 1,
		}},
	}
	if a.Route {
		app.Route = spec.Route{Enabled: true, Path: a.Path, Service: a.Name}
	}
	return app
}

// deploy hands the application to ArgoCD and waits for it.
//
// Two steps, one small progress chain of its own — sequential with the
// bootstrap's, never nested inside it.
//
// The first step applies apps/<app>/application.yaml. That is the ArgoCD
// Application: the registration that tells ArgoCD which repo to watch and where
// to sync it. It is not a workload, and nothing in this file applies one — the
// invariant that ArgoCD is the only deployer is exactly as intact as it was
// before this command existed.
// The bool result is whether ArgoCD actually reported the application synced
// and healthy, so the caller can close the run with a claim or without one.
func deploy(root string, a Answers, opts Options) (bool, error) {
	kube := resolveKubectl(opts.Kubectl)
	appFile := filepath.Join(gitops.AppDir(root, a.Name), "application.yaml")

	p := render.NewProgress("Deploying "+a.Name,
		"Registering "+a.Name+" with ArgoCD", "Waiting for ArgoCD")

	step := p.Start("Registering " + a.Name + " with ArgoCD")
	if kube == nil {
		step.Skip("kubectl is not on PATH")
		p.Finish()
		render.Warn("nothing was applied — `kubectl apply -f " + relPath(root, appFile) + "` registers it")
		return false, nil
	}
	if out, err := kube("apply", "-f", appFile); err != nil {
		// kubectl puts the diagnosis on the lines *after* the headline — an
		// invalid Application says "is invalid:" and then names each field. A
		// first-line-only report throws away the only part worth reading.
		for _, l := range outputLines(out) {
			step.Detail(l)
		}
		err = step.Fail(fmt.Errorf("kubectl apply: %s", reason(out)))
		p.Finish()
		return false, err
	}
	step.Rename(a.Name + " registered with ArgoCD")
	step.Done()

	wait := p.Start("Waiting for ArgoCD")
	if opts.NoWait {
		wait.Skip("--no-wait")
		p.Finish()
		return false, nil
	}
	state := waitForSync(wait, root, a.Name, kube, opts)
	switch {
	case state.synced:
		wait.Rename("ArgoCD synced " + a.Name)
		wait.Done()
	default:
		// Not a failure. ArgoCD may still be working, or it may be waiting for a
		// push, and either way the success screen below reports what is actually
		// running rather than this step guessing.
		wait.Warn(state.why)
	}
	p.Finish()
	if !state.synced && state.needsPush {
		render.Blank()
		render.Warn("ArgoCD only ever sees pushed state, and this commit is not pushed")
		render.Detail(state.pushDetail)
		render.Detail("Endurance never pushes — that is yours to run:")
		render.Detail("  git push")
		render.Detail("then `endurance status " + a.Name + "` · ArgoCD picks it up on its own")
	}
	return state.synced, nil
}

// syncState is what the wait concluded.
type syncState struct {
	synced     bool
	needsPush  bool
	why        string
	pushDetail string
}

// waitForSync polls ArgoCD until the Application is Synced and Healthy.
//
// It reports what it is waiting for, and re-reports only when the answer
// changes: a step that prints the same line every five seconds is a step nobody
// reads. The push check happens once, up front, because "your commit is not on
// the remote" is the difference between waiting usefully and waiting forever.
func waitForSync(step *render.LiveStep, root, app string,
	kube func(args ...string) (string, error), opts Options) syncState {

	st := syncState{}
	// The push check is deliberately not made up front. An application ArgoCD
	// has already synced needs no push, and announcing one anyway would be a
	// warning immediately contradicted by the ✓ underneath it — so it is asked
	// the first time a poll comes back unsynced, and asked once.
	askedAboutPush := false
	checkPush := func() {
		if askedAboutPush {
			return
		}
		askedAboutPush = true
		if ahead, detail := pushState(root); ahead {
			st.needsPush, st.pushDetail = true, detail
			step.Detail(detail)
			step.Detail("Endurance never pushes · run `git push` and this picks it up")
		}
	}

	// The loop is bounded twice over, by a deadline and by a count of attempts.
	// Belt and braces on purpose: the wall clock is injectable so a test can pin
	// it, and a pinned clock never reaches a deadline — so a wait with only the
	// first bound is a wait that can run forever on a frozen clock. Found by
	// writing the test, which is the cheap way to find it.
	deadline := now(opts).Add(opts.Timeout)
	attempts := int(opts.Timeout / waitPoll)
	if attempts < 1 {
		attempts = 1
	}
	last := ""
	for attempt := 1; ; attempt++ {
		sync, health, err := argoStatus(kube, app)
		switch {
		case err != nil:
			describe(step, &last, "ArgoCD has not reported on "+app+" yet")
			checkPush()
		case sync == "Synced" && health == "Healthy":
			st.synced = true
			return st
		default:
			describe(step, &last, "sync "+orUnknown(sync)+" · health "+orUnknown(health))
			checkPush()
		}
		if attempt >= attempts || !now(opts).Before(deadline) {
			st.why = "still " + orUnknown(last) + " after " + opts.Timeout.String()
			if st.needsPush {
				st.why = "waiting for a push"
			}
			return st
		}
		pause(opts, waitPoll)
	}
}

// pause is the wait between polls. Injectable so a test that wants to watch
// three state transitions does not take fifteen seconds to do it.
func pause(opts Options, d time.Duration) {
	if opts.sleep != nil {
		opts.sleep(d)
		return
	}
	time.Sleep(d)
}

func describe(step *render.LiveStep, last *string, msg string) {
	if msg == *last {
		return
	}
	*last = msg
	step.Detail(msg)
}

// argoApp is the slice of ArgoCD's Application status this command reads.
type argoApp struct {
	Status struct {
		Sync struct {
			Status string `json:"status"`
		} `json:"sync"`
		Health struct {
			Status string `json:"status"`
		} `json:"health"`
	} `json:"status"`
}

func argoStatus(kube func(args ...string) (string, error), app string) (sync, health string, err error) {
	out, err := kube("-n", "argocd", "get", "application", app, "-o", "json")
	if err != nil {
		return "", "", fmt.Errorf("%s", firstLine(out))
	}
	var a argoApp
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		return "", "", err
	}
	return a.Status.Sync.Status, a.Status.Health.Status, nil
}

// pushState reports whether this repo has commits ArgoCD cannot see.
//
// Three answers, and they are different: there is no upstream at all (nothing
// to push to — a fresh clone with no remote, or a branch never pushed), there
// are commits ahead of it, or everything is published. Only the first two make
// the wait futile, and each needs a different sentence.
func pushState(root string) (needsPush bool, detail string) {
	branch := strings.TrimSpace(git(root, "rev-parse", "--abbrev-ref", "HEAD"))
	head := strings.TrimSpace(git(root, "rev-parse", "--short", "HEAD"))
	upstream := strings.TrimSpace(git(root, "rev-parse", "--abbrev-ref", "@{upstream}"))
	if upstream == "" {
		return true, "branch " + orUnknown(branch) + " has no upstream — nothing has been pushed anywhere"
	}
	ahead := strings.TrimSpace(git(root, "rev-list", "--count", "@{upstream}..HEAD"))
	if ahead == "" || ahead == "0" {
		return false, ""
	}
	return true, "commit " + head + " is " + ahead + " ahead of " + upstream
}

func git(root string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// gitUser is the owner default: the name git already knows, so init does not
// ask a stranger to type something the machine can tell it.
func gitUser(root string) string {
	return strings.TrimSpace(git(root, "config", "user.name"))
}

// freePath picks a URL path nobody has taken.
//
// `/` is what anybody wants for their first application, and it is also what
// superheros already has on this repo's own platform. Istio merges every
// VirtualService bound to one gateway into a single route table, so two
// applications both claiming `/` is not an error anywhere — it is one of them
// silently never being reached. Defaulting around it is cheaper than explaining
// it.
func freePath(root, name string) string {
	taken := map[string]bool{}
	apps, err := gitops.List(root)
	if err == nil {
		for _, a := range apps {
			if a.Route.Enabled && a.Name != name {
				taken[a.Route.Path] = true
			}
		}
	}
	if !taken["/"] {
		return "/"
	}
	return "/" + name
}

// --- small helpers ---

func resolveKubectl(k func(args ...string) (string, error)) func(args ...string) (string, error) {
	if k != nil {
		return k
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil
	}
	return func(args ...string) (string, error) {
		out, err := exec.Command("kubectl", args...).CombinedOutput()
		return string(out), err
	}
}

func now(opts Options) time.Time {
	if opts.Now != nil {
		return opts.Now()
	}
	return time.Now()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// gitopsRepo is the URL ArgoCD will be told to watch. The flag wins; otherwise
// this repo's origin remote, which is what makes a fork deploy from the fork;
// otherwise the upstream default. It is never empty — an empty repoURL renders
// an Application the API server rejects.
func gitopsRepo(root string, opts Options) string {
	if opts.GitopsRepo != "" {
		return opts.GitopsRepo
	}
	if url := gitops.OriginURL(root); url != "" {
		return url
	}
	return gitops.DefaultRepo
}

// maxReasons bounds how much of a command's complaint is folded into a
// one-line error. Every line is still shown as a detail above it.
const maxReasons = 4

// outputLines returns the non-empty, trimmed lines of a command's output.
func outputLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// reason folds a multi-line complaint into one sentence: the headline, then the
// specifics it introduced. "The Application "x" is invalid:" on its own names
// no fault anybody can act on; with "spec.sources[0].repoURL: Required value"
// after it, it does.
func reason(out string) string {
	lines := outputLines(out)
	if len(lines) == 0 {
		return "no output"
	}
	head := lines[0]
	rest := lines[1:]
	if len(rest) == 0 {
		return head
	}
	head = strings.TrimSuffix(head, ":")
	more := ""
	if len(rest) > maxReasons {
		rest, more = rest[:maxReasons], ", …"
	}
	for i, r := range rest {
		rest[i] = strings.TrimPrefix(r, "* ")
	}
	return head + ": " + strings.Join(rest, "; ") + more
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// yamlScalar quotes a value that YAML would otherwise read as something else.
// An owner called "Yes" or one containing a colon is rare and silently wrong.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, `:#"'{}[]&*?|<>=!%@`+"`") || strings.TrimSpace(s) != s {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return s
}

// --- validators, shared with the form ---

func validName(v string) error {
	t := spec.App{Name: v, Namespace: v, Services: []spec.Service{{Name: v, Image: "i", Port: 1, Replicas: 1}}}
	if err := t.Validate(); err != nil {
		return fmt.Errorf("must be lowercase letters, digits and '-'")
	}
	return nil
}

func nonEmpty(field string) func(string) error {
	return func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
}

func validTag(v string) error {
	if v == "latest" {
		return fmt.Errorf("the platform's own policy refuses `latest` — pin a version")
	}
	return spec.ValidateTag(v)
}

func validPort(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port must be 1-65535")
	}
	return nil
}

// validPath refuses a platform prefix at the prompt rather than three screens
// later, when the same rule is enforced by spec.Validate and the run is already
// half done.
func validPath(v string) error {
	if v == "" {
		return nil
	}
	if !strings.HasPrefix(v, "/") {
		return fmt.Errorf("a path on http://localhost:8080, so it starts with /")
	}
	for _, reserved := range spec.ReservedPaths {
		if v == reserved || strings.HasPrefix(v, reserved+"/") {
			return fmt.Errorf("%s belongs to the platform's own dashboards (%s)",
				reserved, strings.Join(spec.ReservedPaths, ", "))
		}
	}
	return nil
}
