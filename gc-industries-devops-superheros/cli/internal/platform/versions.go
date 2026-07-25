package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/version"
	"gopkg.in/yaml.v3"
)

// `endurance version` — what this build of the platform installs.
//
// The CLI version alone answers almost nothing: "which Endurance" matters much
// less than "which Istio, which Prometheus, which Kyverno". Those numbers live
// in the platform tree, in the files the installers themselves read, and this
// command reads the same files rather than keeping a second list that could
// drift. Where a component has no pinned version, that is what it says — an
// unpinned chart is a fact about the platform, not a blank to be filled in with
// something plausible.
//
// This is deliberately declarative and offline. What is *running* is a
// different question and `endurance status` answers it, from the cluster.

// A Component is one thing the platform installs and the version it declares.
type Component struct {
	Name     string
	Version  string // "" when nothing pins it
	Source   string // the file that says so, repo-relative
	Note     string // when Version is empty, why
	Optional bool   // not installed today (Kiali) — reported, never a warning
}

// versionsFile is the shape of the platform/*/versions.yaml files. Only the
// fields this command reads are declared; the files carry more.
type versionsFile struct {
	Istio struct {
		Version string `yaml:"version"`
	} `yaml:"istio"`
	Kiali struct {
		Chart   string `yaml:"chart"`
		Version string `yaml:"version"`
		WebRoot string `yaml:"web_root"`
	} `yaml:"kiali"`
	Prometheus struct {
		Chart   string `yaml:"chart"`
		Version string `yaml:"version"`
	} `yaml:"prometheus"`
	Grafana struct {
		Version string `yaml:"version"`
	} `yaml:"grafana"`
	AIAlertmanager struct {
		Image string `yaml:"image"`
		Tag   string `yaml:"tag"`
	} `yaml:"ai_alertmanager"`
}

func readVersions(root, rel string) (versionsFile, error) {
	var v versionsFile
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return v, err
	}
	if err := yaml.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("%s: %w", rel, err)
	}
	return v, nil
}

func declaredIstioVersion(root string) (string, error) {
	v, err := readVersions(root, "platform/networking/versions.yaml")
	return v.Istio.Version, err
}

// shellVar reads one `NAME="value"` assignment out of a bash file.
//
// Reading configuration out of a shell script is not something to do casually,
// and it is done here for exactly two values that have no other home: the
// cluster name and context in platform/lib/version.sh, and the Kyverno chart
// version in its installer. Duplicating either into Go would create the drift
// this platform keeps writing decisions about — a version file nobody reads is
// documentation, and a constant nobody checks is worse. The tests read the real
// files, so if a line moves the suite says so.
const shellAssign = `(?m)^[[:space:]]*(?:readonly[[:space:]]+)?%s=["']?([^"'\n#]*)["']?`

func shellVar(root, rel, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	re, err := regexp.Compile(fmt.Sprintf(shellAssign, regexp.QuoteMeta(name)))
	if err != nil {
		return "", err
	}
	m := re.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("%s: no %s assignment", rel, name)
	}
	return expandShell(string(b), string(m[1]), 0), nil
}

// shellRef matches a ${NAME} or $NAME reference inside a value.
var shellRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// expandShell resolves references to other variables in the same file.
//
// Needed because the values worth reading are written in terms of each other:
// platform/lib/version.sh says KUBERNETES_CONTEXT="kind-${CLUSTER_NAME}", and
// reading that literally is how `endurance status` came to announce it was
// talking to a cluster called `kind-${CLUSTER_NAME}`. Bounded recursion, since
// a shell file can perfectly well define a cycle and this is not a shell.
func expandShell(file, value string, depth int) string {
	if depth > 4 || !strings.Contains(value, "$") {
		return value
	}
	return shellRef.ReplaceAllStringFunc(value, func(ref string) string {
		m := shellRef.FindStringSubmatch(ref)
		name := m[1]
		if name == "" {
			name = m[2]
		}
		re, err := regexp.Compile(fmt.Sprintf(shellAssign, regexp.QuoteMeta(name)))
		if err != nil {
			return ref
		}
		sub := re.FindStringSubmatch(file)
		if sub == nil {
			return ref
		}
		return expandShell(file, sub[1], depth+1)
	})
}

// ClusterName and ContextName are the kind cluster the platform lives in, read
// from platform/lib/version.sh so bash and Go cannot disagree about which
// cluster `destroy` deletes.
func ClusterName(root string) string {
	if v, err := shellVar(root, versionFile, "CLUSTER_NAME"); err == nil && v != "" {
		return v
	}
	return "superheros"
}

func ContextName(root string) string {
	if v, err := shellVar(root, versionFile, "KUBERNETES_CONTEXT"); err == nil && v != "" {
		return v
	}
	return "kind-" + ClusterName(root)
}

// Components returns what the platform declares it installs, in bootstrap order.
func Components(root string) []Component {
	net, _ := readVersions(root, "platform/networking/versions.yaml")
	mon, _ := readVersions(root, "platform/monitoring/versions.yaml")
	ai, _ := readVersions(root, "platform/ai/versions.yaml")
	acc, _ := readVersions(root, accessVersions)

	platformVersion, _ := shellVar(root, versionFile, "PLATFORM_VERSION")
	kyverno, kerr := shellVar(root, "platform/security/kyverno/install.sh", "KYVERNO_CHART_VERSION")

	// The ArgoCD chart is installed with a bare `helm upgrade --install
	// argo/argo-cd`: no --version, and platform/gitops/versions.yaml is an
	// empty file. Whatever the Helm repo holds on the day is what a bootstrap
	// gets. Reported as unpinned rather than guessed at.
	argocd := Component{
		Name: "argo-cd", Source: "platform/gitops/versions.yaml",
		Note: "not pinned — the installer takes the repo's latest chart",
	}
	kyvernoComp := Component{
		Name: "kyverno", Version: kyverno,
		Source: "platform/security/kyverno/install.sh",
	}
	if kerr != nil {
		kyvernoComp.Note = "could not read the chart version from the installer"
	}
	// Kiali is installed by platform/access since Phase 10, and its version
	// lives in that module's own versions.yaml — the file its installer reads.
	// Until then it was declared in two files, installed by neither, and
	// reported as `· not installed`, which is what it was.
	kiali := Component{
		Name: "kiali", Version: acc.Kiali.Version, Source: accessVersions,
	}
	if kiali.Version == "" {
		kiali.Note = "no version pinned in " + accessVersions
	}

	return []Component{
		{Name: "endurance CLI", Version: version.Current, Source: "cli/internal/version"},
		{Name: "platform", Version: platformVersion, Source: versionFile},
		{Name: "istio", Version: net.Istio.Version, Source: "platform/networking/versions.yaml"},
		{Name: "kube-prometheus-stack", Version: mon.Prometheus.Version, Source: "platform/monitoring/versions.yaml"},
		{Name: "ai-alertmanager", Version: ai.AIAlertmanager.Tag, Source: "platform/ai/versions.yaml"},
		argocd,
		kyvernoComp,
		kiali,
	}
}

// Version prints `endurance version`. Short prints the one line a script wants
// and nothing else.
func Version(root string, short bool) error {
	line := "endurance " + version.Current
	if short {
		render.Print(line)
		return nil
	}

	render.Banner(version.Current)
	render.Section("Components")

	dir, err := FindRoot(root)
	if err != nil {
		render.Print(line)
		render.Info("no platform repo found here — component versions live in its versions.yaml files")
		return nil
	}

	checks := make([]render.Check, 0, len(Components(dir)))
	for _, c := range Components(dir) {
		checks = append(checks, c.check())
	}
	render.Checks(checks)
	render.Blank()
	render.Info("these are the versions this platform installs · `endurance status` shows what is running")
	return nil
}

// check renders one component: pinned is a ✓, unpinned is a ⚠ because it is a
// real gap in the platform's reproducibility, and a component that is not
// installed at all is a · rather than either.
func (c Component) check() render.Check {
	switch {
	case c.Optional:
		note := c.Version
		if c.Note != "" {
			note += " · " + c.Note
		}
		return render.Check{Name: c.Name, State: render.StatePending, Note: note}
	case c.Version == "":
		return render.Check{Name: c.Name, State: render.StateWarn, Note: c.Note}
	default:
		return render.Check{Name: c.Name, State: render.StateReady, Note: c.Version}
	}
}
