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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/gc-ghub/endurance/internal/deploy"
	"github.com/gc-ghub/endurance/internal/features"
	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/image"
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

// DefaultTimeout is how long the deploy phase keeps polling ArgoCD, mirroring
// deploy.DefaultTimeout so `endurance init --timeout` and `endurance deploy
// --timeout` default to the same duration without one importing the other's
// constant into its flag help text.
//
// Six minutes because that is comfortably longer than a first sync of a
// single-service application and comfortably shorter than the patience of
// somebody watching a terminal. A timeout is not a failure: it ends on the
// success screen with whatever was actually observed, which for a run still
// waiting on a push is exactly the right screen.
const DefaultTimeout = deploy.DefaultTimeout

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
	NoMesh  bool

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

	// InspectImage is the registry lookup the image preflight uses, forwarded to
	// onboard.
	//
	// It is threaded through rather than resolved inside onboard because a
	// package-level default is a call every test makes by accident. That it is
	// *forwarded* is the part with a rule in it: every init test injects a fake
	// Onboard, so nothing about what init passes onboard is exercised by the
	// happy path — which is how v0.10.1 shipped an init that onboarded without a
	// GitopsRepo. There is a test that asserts on this field's arrival.
	InspectImage image.Inspector

	// SkipImageCheck is break glass for the image preflight, forwarded the same
	// way.
	SkipImageCheck bool

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
	// Deploy is the shared verb (14.2) — nil means deploy.Run, the same function
	// `endurance deploy <app>` calls on its own.
	Deploy  func(deploy.Options) (bool, error)
	Inspect func(root string) platform.ClusterState
	Health  func(root string) platform.Health
	Now     func() time.Time
	sleep   func(time.Duration)
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

	// Env is the first service's environment. issues.md §5b: the spec format
	// has rendered per-service env since Phase 1 (values.yaml documents it,
	// deployment.yaml renders it) and neither interactive form ever asked for
	// it, so portfolio's backend exited at startup over a missing
	// GITHUB_USERNAME and the platform explained nothing — a container that
	// exits in under a second with a one-line reason on stdout is the easiest
	// failure in Kubernetes to diagnose, and asking here is cheaper than that.
	Env []spec.EnvVar

	// ExtraServices is every service beyond the first — the N in "N services"
	// (14.1). Each is asked in its own small form, the same shape `onboard`'s
	// removed serviceForm used: a huh.Form cannot repeat a group an unknown
	// number of times, so the loop is a Go for over separate form.Run() calls,
	// exactly like the "Add another service?" loop onboard's interactive path
	// had before this item removed it.
	ExtraServices []ServiceAnswer

	// Mesh is Istio membership, and it is the answer that used to be missing
	// rather than defaulted. Nothing asked, so nothing ever said yes, so every
	// pod of every application anybody onboarded ran without a sidecar on a
	// platform built entirely out of Istio (Phase 13).
	Mesh bool

	AI          bool
	OpenAIKey   string
	AISlackHook string

	Slack     bool
	SlackHook string
}

// ServiceAnswer is one service beyond the application's first.
//
// Route is asked per extra service rather than once for the whole
// application, because a route names one service — `specs/superheros.yaml`
// routes `/` to its frontend and `/api/catalog` to catalog, and a multi-service
// application choosing which of its services face the platform's one host is
// exactly this question asked once per service.
type ServiceAnswer struct {
	Name  string
	Image string
	Tag   string
	Port  int
	Env   []spec.EnvVar

	Route bool
	Path  string
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

		Inspect:        opts.InspectImage,
		SkipImageCheck: opts.SkipImageCheck,
	}); err != nil {
		return err
	}

	deployed, err := deployApp(root, answers, opts)
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
		Mesh:  !opts.NoMesh,
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
	envStr := ""
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
			// A container that exits in under a second with a one-line reason on
			// stdout is the easiest failure in Kubernetes to explain, and a
			// missing required env var is the commonest one — portfolio's
			// backend in the first outside run exited at startup over a missing
			// GITHUB_USERNAME, and neither interactive form had ever asked.
			huh.NewInput().Title("Environment variables").
				Description("optional · KEY=value, comma-separated, e.g. LOG_LEVEL=info,PORT=8080").
				Value(&envStr).Validate(validEnv),
			// Asked, and asked here rather than among the capabilities, because
			// it is a property of the application rather than a credential to
			// hand over. Yes is the default and the answer almost everybody
			// wants: the platform is Istio, and an application outside the mesh
			// is invisible to Kiali and cannot shift traffic.
			huh.NewConfirm().Title("Put it in the service mesh?").
				Description("Istio sidecars · needed for canary and for Kiali to see it · adds one container per pod").
				Affirmative("Yes").Negative("No").
				Value(&a.Mesh),
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
			// Validated live, on the same field, the moment it is given
			// (14.8) — the OpenAI API rejecting this key used to surface
			// minutes later, inside an enriched Slack message about an
			// unrelated pod.
			features.SecretField("OpenAI API key",
				"masked · leave it empty to skip enrichment · written to platform/ai/secret.yaml, "+
					"which is git-ignored, and never printed back",
				false, &a.OpenAIKey).Validate(validOpenAIKeyLive),
			features.SecretField("Slack incoming-webhook URL for enriched alerts",
				"masked · optional, leave empty to enrich into the logs only",
				false, &a.AISlackHook).Validate(validWebhookLive),
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
				false, &a.SlackHook).Validate(validWebhookLive),
		).WithHideFunc(func() bool { return !a.Slack }),
	)
	if err := form.Run(); err != nil {
		return err
	}
	a.Port, _ = strconv.Atoi(port)
	a.Env, _ = parseEnv(envStr) // already validated by the form
	settle(a)

	// N services (14.1). huh cannot repeat a group an unknown number of
	// times, so — the same shape onboard's removed serviceForm loop used —
	// this is a plain Go loop over separate small forms, run after the main
	// one so the confirmation screen still describes every answer together.
	if err := askExtraServices(a); err != nil {
		return err
	}
	return nil
}

// askExtraServices loops "add another service?" until the answer is no. Each
// yes opens one small form for that service — name, image, tag, port, env,
// and whether it gets its own route, the same questions the main application
// asked about its first service.
func askExtraServices(a *Answers) error {
	for {
		more := false
		if err := prompt.Run(huh.NewConfirm().
			Title("Add another service?").
			Description(fmt.Sprintf("%d service(s) so far: %s", 1+len(a.ExtraServices), serviceNamesSoFar(a))).
			Affirmative("Yes").Negative("No, that's all").
			Value(&more)); err != nil {
			return err
		}
		if !more {
			return nil
		}
		svc, err := askOneService(len(a.ExtraServices) + 2)
		if err != nil {
			return err
		}
		a.ExtraServices = append(a.ExtraServices, svc)
	}
}

// serviceNamesSoFar lists the application's services for the "add another?"
// prompt, so a developer three services in can see what they have typed
// rather than only a count.
func serviceNamesSoFar(a *Answers) string {
	names := []string{orNone(a.Name)}
	for _, s := range a.ExtraServices {
		names = append(names, orNone(s.Name))
	}
	return strings.Join(names, ", ")
}

// askOneService asks everything about one service beyond the first, as its
// own form — built through internal/prompt like every other form in this
// tool, sharing the same validators the main application form uses so a
// second service cannot pass a name or a tag the first would have been
// refused for.
func askOneService(n int) (ServiceAnswer, error) {
	var s ServiceAnswer
	port := ""
	envStr := ""
	form := prompt.Form(
		huh.NewGroup(
			huh.NewNote().Title(fmt.Sprintf("Service #%d", n)),
			huh.NewInput().Title("Service name").
				Description("lowercase, DNS-safe").
				Value(&s.Name).Validate(validName),
			huh.NewInput().Title("Container image").
				Description("with its registry").
				Value(&s.Image).Validate(nonEmpty("image")),
			huh.NewInput().Title("Tag").
				Description("`latest` is refused by the platform's own policy — pin it").
				Value(&s.Tag).Validate(validTag),
			huh.NewInput().Title("Container port").
				Value(&port).Validate(validPort),
			huh.NewInput().Title("Environment variables").
				Description("optional · KEY=value, comma-separated").
				Value(&envStr).Validate(validEnv),
			// A route names one service. specs/superheros.yaml routes `/` to
			// its frontend and `/api/catalog` to catalog — a multi-service
			// application choosing which services face the platform's one
			// host is this question, once per service.
			huh.NewConfirm().Title("Give it its own URL?").
				Description("a path on the platform's host, routed to this service").
				Affirmative("Yes").Negative("No").
				Value(&s.Route),
		),
		huh.NewGroup(
			huh.NewInput().Title("URL path").
				Description("where it answers on http://localhost:8080").
				Value(&s.Path).Validate(validPath),
		).WithHideFunc(func() bool { return !s.Route }),
	)
	if err := form.Run(); err != nil {
		return s, err
	}
	s.Port, _ = strconv.Atoi(port)
	s.Env, _ = parseEnv(envStr)
	if !s.Route {
		// The same rule settle() applies to a declined capability: a hidden
		// group keeps whatever it held before it was hidden, so declining the
		// route must clear the path or the plan would describe one that was
		// never asked for.
		s.Path = ""
	}
	return s, nil
}

// parseEnv reads "KEY=value,KEY2=value2" into the pairs the spec model wants.
// An empty string is zero pairs, not an error — env is optional everywhere it
// is asked.
func parseEnv(s string) ([]spec.EnvVar, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []spec.EnvVar
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("%q is not KEY=value", pair)
		}
		out = append(out, spec.EnvVar{Name: k, Value: strings.TrimSpace(v)})
	}
	return out, nil
}

func validEnv(s string) error {
	_, err := parseEnv(s)
	return err
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

// validOpenAIKeyLive asks OpenAI's own API whether the key works, the moment
// it is given (14.8) — this is the field `endurance enable ai` validates the
// same way, and this path is only ever reached from a real terminal: every
// test that exercises init's form replaces Options.Ask wholesale, so this
// makes exactly one real network call, and only when a human is at the
// keyboard to see the result of it.
func validOpenAIKeyLive(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil // empty means declined; settle() clears it
	}
	if err := features.DefaultKeyValidator()(s); err != nil {
		return fmt.Errorf("%w — leave it empty to skip enrichment, or fix it and try again", err)
	}
	return nil
}

// validWebhookLive is optionalWebhook plus the same live check
// features.DefaultWebhookValidator does for `enable ai` and `enable slack` —
// syntax first, so a plainly-wrong URL is not sent anywhere.
func validWebhookLive(s string) error {
	if err := optionalWebhook(s); err != nil {
		return err
	}
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if err := features.DefaultWebhookValidator()(s); err != nil {
		return fmt.Errorf("%w — leave it empty to skip, or fix it and try again", err)
	}
	return nil
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
		appDetails = append(appDetails, meshDetail(existing.Mesh.On()))
		if c := existing.CanaryServices(); len(c) > 0 {
			appDetails = append(appDetails, "canary: "+strings.Join(c, ", "))
		}
		appDetails = append(appDetails,
			"specs/"+existing.Name+".yaml already exists and is left alone — it is the input, and it is yours",
			"apps/"+existing.Name+"/{app,application,values}.yaml is regenerated from it",
			"the manifests are checked against this platform's Kyverno policies first")
	} else {
		// Built from the same function that will validate and write the spec,
		// so the plan describes the exact application the rest of the run
		// acts on — N services and every route, not just the first of each.
		app := toSpec(a)
		svcLine := "1 service · " + a.Image + ":" + a.Tag + " on port " + strconv.Itoa(a.Port)
		if n := len(app.Services); n > 1 {
			svcLine = fmt.Sprintf("%d services · %s", n, strings.Join(app.ServiceNames(), ", "))
		}
		appDetails = []string{
			svcLine,
			"namespace " + a.Name + " · owner " + orNone(a.Owner),
		}
		if routes := app.RouteList(); len(routes) == 1 {
			appDetails = append(appDetails, "reachable at "+app.URL(base)+" via "+routes[0].Service)
		} else if len(routes) > 1 {
			appDetails = append(appDetails, fmt.Sprintf("%d routes, front door %s", len(routes), app.URL(base)))
		}
		appDetails = append(appDetails,
			meshDetail(a.Mesh),
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

// meshDetail is the plan's one line about Istio.
//
// It is a plan line rather than a skip line in both directions on purpose. Being
// in the mesh changes what runs — a second container in every pod — and the
// confirmation screen is where a user finds out what they are about to get. The
// previous behaviour was to get neither the sidecar nor the sentence.
func meshDetail(on bool) string {
	if on {
		return "in the Istio mesh · a sidecar per pod, so pods read 2/2 · visible in Kiali"
	}
	return "outside the Istio mesh · no sidecar, no Kiali graph, no weighted routing"
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
//
// It renders from toSpec(a) rather than from the answers field by field, so the
// file on disk and the app the plan was validated against can never disagree
// about how many services or routes there are — the same reason `finalise`
// validates the same App this function goes on to write.
func specYAML(a Answers) string {
	app := toSpec(a)
	routes := app.RouteList()

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
		b.WriteString("\n# Where this application's own code lives. Recorded in the registry entry\n")
		b.WriteString("# and never cloned — the platform builds no images and reads no application\n")
		b.WriteString("# source. It is NOT the repo ArgoCD watches: that is the platform repo, and\n")
		b.WriteString("# it is `--gitops-repo`, which defaults to this repo's origin remote.\n")
		b.WriteString("sourceRepo: " + a.Repo + "\n")
	}
	if a.Owner != "" {
		b.WriteString("owner: " + yamlScalar(a.Owner) + "\n")
	}
	b.WriteString("\n# Istio. On unless you say otherwise: the platform routes everything through\n")
	b.WriteString("# the Istio ingress gateway and ships Kiali to visualise the mesh, so an\n")
	b.WriteString("# application outside it is invisible there and cannot shift traffic between\n")
	b.WriteString("# weighted versions. Every pod gains a sidecar container, so pods read 2/2.\n")
	b.WriteString("mesh:\n")
	b.WriteString("  enabled: " + strconv.FormatBool(a.Mesh) + "\n")

	switch len(routes) {
	case 0:
		// Nothing written, same as before this item: a disabled route keeps no
		// fields at all, so a file that asked for none carries no fossil.
	case 1:
		b.WriteString("\n# The application's public address. The platform owns one Istio Gateway and\n")
		b.WriteString("# deliberately knows nothing about what is behind any path on it — an\n")
		b.WriteString("# application asks, here, and charts/app renders a VirtualService bound to it.\n")
		b.WriteString("# `port` is omitted so it is read from the named service and cannot drift.\n")
		b.WriteString("# /argocd, /kiali, /grafana, /prometheus and /alertmanager are the platform's.\n")
		b.WriteString("route:\n")
		b.WriteString("  enabled: true\n")
		b.WriteString("  path: " + routes[0].Path + "\n")
		b.WriteString("  service: " + routes[0].Service + "\n")
	default:
		// `routes:` is a list because a route names one service, and a
		// multi-service application publishes some services and keeps most of
		// them internal. specs/superheros.yaml routes `/` to its frontend and
		// `/api/catalog` to catalog, which is this shape with the answers typed
		// instead of hand-written.
		b.WriteString("\n# This application's public addresses — one entry per exposed service, most\n")
		b.WriteString("# specific path first so a `/` rule can never shadow one beneath it. The\n")
		b.WriteString("# platform owns one Istio Gateway and knows nothing behind any path on it.\n")
		b.WriteString("routes:\n")
		for _, r := range routes {
			b.WriteString("  - path: " + r.Path + "\n")
			b.WriteString("    service: " + r.Service + "\n")
		}
	}

	if len(app.Services) == 1 {
		b.WriteString("\n# One service. An application is a set of independently-deployable services;\n")
		b.WriteString("# this is the N=1 case. Add another entry here and re-run onboard to grow it.\n")
	} else {
		fmt.Fprintf(&b,
			"\n# %d services. An application is a set of independently-deployable\n"+
				"# services — add another entry here and re-run onboard to grow it further.\n",
			len(app.Services))
	}
	b.WriteString("services:\n")
	for _, s := range app.Services {
		b.WriteString("  - name: " + s.Name + "\n")
		b.WriteString("    image: " + s.Image + "\n")
		b.WriteString("    tag: " + s.Tag + "\n")
		b.WriteString("    port: " + strconv.Itoa(s.Port) + "\n")
		b.WriteString("    replicas: 1\n")
		if len(s.Env) > 0 {
			b.WriteString("    env:\n")
			for _, e := range s.Env {
				b.WriteString("      - name: " + e.Name + "\n")
				b.WriteString("        value: " + yamlScalar(e.Value) + "\n")
			}
		}
	}
	return b.String()
}

// meshAnswer turns init's yes/no into the spec's tri-state. Always an explicit
// answer, never absent: a user who was asked has answered, and the spec file is
// the record of what they said.
func meshAnswer(on bool) spec.Mesh {
	if on {
		return spec.MeshOn()
	}
	return spec.MeshOff()
}

// toSpec is the in-memory equivalent, for validating the answers before the
// plan is printed and before the spec file is written — both read this same
// function, so the two can never disagree about what N services and which
// routes the answers describe.
func toSpec(a Answers) spec.App {
	app := spec.App{
		Name:       a.Name,
		Namespace:  a.Name,
		SourceRepo: a.Repo,
		Owner:      a.Owner,
		Mesh:       meshAnswer(a.Mesh),
		Services:   answerServices(a),
	}
	if routes := answerRoutes(a); len(routes) == 1 {
		app.Route = routes[0]
	} else if len(routes) > 1 {
		app.Routes = routes
	}
	return app
}

// answerServices is the application's first service plus every ExtraServices
// entry, in the order they were asked — the N in "N services".
func answerServices(a Answers) []spec.Service {
	out := []spec.Service{{
		Name: a.Name, Image: a.Image, Tag: a.Tag, Port: a.Port, Replicas: 1, Env: a.Env,
	}}
	for _, e := range a.ExtraServices {
		out = append(out, spec.Service{
			Name: e.Name, Image: e.Image, Tag: e.Tag, Port: e.Port, Replicas: 1, Env: e.Env,
		})
	}
	return out
}

// answerRoutes is every route a human asked for, in the order they were asked
// — the app's own (if Route) followed by each extra service that said yes.
// toSpec collapses this to `route:` for one and `routes:` for more than one,
// matching how the spec format itself distinguishes the two.
func answerRoutes(a Answers) []spec.Route {
	var out []spec.Route
	if a.Route {
		out = append(out, spec.Route{Enabled: true, Path: a.Path, Service: a.Name})
	}
	for _, e := range a.ExtraServices {
		if e.Route {
			out = append(out, spec.Route{Enabled: true, Path: e.Path, Service: e.Name})
		}
	}
	return out
}

// deployApp hands the application to ArgoCD and waits for it.
//
// Sequential with the bootstrap's progress chain, never nested inside it. The
// work itself — apply the Application, poll ArgoCD — lives in internal/deploy
// since Phase 14 (14.2): `endurance deploy <app>` and this guided run are one
// function now, not one of them reimplementing it privately. Injectable so a
// test can assert on what init passes it, the same rule that caught v0.10.1's
// missing GitopsRepo.
func deployApp(root string, a Answers, opts Options) (bool, error) {
	run := opts.Deploy
	if run == nil {
		run = deploy.Run
	}
	return run(deploy.Options{
		Root: root, App: a.Name,
		NoWait: opts.NoWait, Timeout: opts.Timeout,
		Kubectl: opts.Kubectl, Now: opts.Now, Sleep: opts.sleep,
	})
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
