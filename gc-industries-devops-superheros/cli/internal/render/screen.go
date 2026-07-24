package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The end-of-run surfaces. A titled rule opens a phase of work; a box closes
// one. That is the whole grammar — if output is inside a box, the command is
// over and this is what it did.

// A URL is one addressable thing the platform put in front of the user.
type URL struct {
	Label string // "App", "ArgoCD", "Grafana"
	Addr  string // the URL itself
	Note  string // optional: credentials hint, "not installed", …
}

// A Pod is one workload's state on the success screen.
type Pod struct {
	Name   string
	Status string // "Running", "ContainerCreating", "CrashLoopBackOff"
	State  State
}

// A Hint is a command worth running next.
type Hint struct {
	Command string
	Note    string
}

// A Result is the end-of-run success screen: what was deployed, where it is,
// whether it is actually up yet, and what to type next.
//
// Phase 10 fills it from a live cluster. Phase 8 only owns what it looks like —
// and one rule about what it may say: Pods carry a [State], and a pod that is
// not Running yet renders as pending, not as a ✓. A success screen that claims
// health it has not observed is the fastest way to make a platform untrusted,
// so Footer exists to say the quiet part ("2 of 3 pods ready — ArgoCD is still
// syncing") rather than round it up.
type Result struct {
	Title  string
	Rows   [][2]string // Namespace / Cluster / Image / Replicas …
	Pods   []Pod
	URLs   []URL
	Hints  []Hint
	Footer string
}

// SuccessScreen prints the end-of-run screen.
func (r *Renderer) SuccessScreen(res Result) {
	var b strings.Builder
	b.WriteString(r.ok.Render(IconOK) + " " + r.brand.Render(res.Title) + "\n")

	if len(res.Rows) > 0 {
		b.WriteString("\n")
		for _, l := range r.kvLines(res.Rows) {
			b.WriteString(l + "\n")
		}
	}
	if len(res.Pods) > 0 {
		b.WriteString("\n" + r.head.Render("Pods") + "\n")
		for _, l := range r.podLines(res.Pods) {
			b.WriteString(l + "\n")
		}
	}
	if len(res.URLs) > 0 {
		b.WriteString("\n" + r.head.Render("URLs") + "\n")
		for _, l := range r.urlLines(res.URLs) {
			b.WriteString(l + "\n")
		}
	}
	if len(res.Hints) > 0 {
		b.WriteString("\n" + r.head.Render("Try") + "\n")
		for _, l := range r.hintLines(res.Hints) {
			b.WriteString(l + "\n")
		}
	}
	if res.Footer != "" {
		b.WriteString("\n" + r.muted.Render(res.Footer) + "\n")
	}
	r.emit("\n" + r.box(b.String()))
}

// URLBlock prints the URL block on its own, boxless — `endurance urls` is a
// lookup, not the end of a run, and only a run earns a box.
func (r *Renderer) URLBlock(title string, urls []URL) {
	r.Section(title)
	if len(urls) == 0 {
		r.Info("nothing is exposed yet — run `endurance bootstrap` first")
		return
	}
	for _, l := range r.urlLines(urls) {
		r.emit(l)
	}
}

// Dashboard prints a compact boxed summary: title, key/value rows, and
// next-step lines. It is the terse sibling of SuccessScreen, used by onboard,
// release and canary, where there is no cluster state to report yet.
func (r *Renderer) Dashboard(title string, rows [][2]string, next []string) {
	var b strings.Builder
	b.WriteString(r.brand.Render(title) + "\n\n")
	for _, l := range r.kvLines(rows) {
		b.WriteString(l + "\n")
	}
	if len(next) > 0 {
		b.WriteString("\n" + r.head.Render("Next") + "\n")
		for _, n := range next {
			b.WriteString(r.faint.Render(IconNext) + " " + n + "\n")
		}
	}
	r.emit("\n" + r.box(b.String()))
}

// box draws the closing border. It grows to fit its content but is never
// narrower than a section rule — a stubby box beside a full-width rule reads as
// a rendering bug, and the two must look like one system.
func (r *Renderer) box(s string) string {
	const borderAndPadding = 6 // │ + 2 spaces, twice
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	inner := r.width - borderAndPadding
	for _, line := range lines {
		if w := lipgloss.Width(line); w > inner {
			inner = w
		}
	}
	// Pad rather than setting a width on the style: lipgloss would wrap a long
	// line to fit, and a wrapped image reference or URL is worse than a wide box.
	for i, line := range lines {
		lines[i] = pad(line, inner)
	}
	return r.lip.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cFaint).
		Padding(0, 2).
		Render(strings.Join(lines, "\n"))
}

// kvLines aligns key/value rows on the value column.
func (r *Renderer) kvLines(rows [][2]string) []string {
	w := 0
	for _, kv := range rows {
		if len(kv[0]) > w {
			w = len(kv[0])
		}
	}
	out := make([]string, 0, len(rows))
	for _, kv := range rows {
		out = append(out, r.muted.Render(pad(kv[0], w))+"  "+kv[1])
	}
	return out
}

func (r *Renderer) podLines(pods []Pod) []string {
	w := 0
	for _, p := range pods {
		if len(p.Name) > w {
			w = len(p.Name)
		}
	}
	out := make([]string, 0, len(pods))
	for _, p := range pods {
		line := r.Icon(p.State) + " " + pad(p.Name, w)
		if p.Status != "" {
			line += "  " + r.muted.Render(p.Status)
		}
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}

func (r *Renderer) urlLines(urls []URL) []string {
	w := 0
	for _, u := range urls {
		if len(u.Label) > w {
			w = len(u.Label)
		}
	}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		line := r.muted.Render(pad(u.Label, w)) + "  " + r.Value(u.Addr)
		if u.Note != "" {
			line += "  " + r.faint.Render(u.Note)
		}
		out = append(out, line)
	}
	return out
}

func (r *Renderer) hintLines(hints []Hint) []string {
	w := 0
	for _, h := range hints {
		if len(h.Command) > w {
			w = len(h.Command)
		}
	}
	out := make([]string, 0, len(hints))
	for _, h := range hints {
		line := r.faint.Render(IconNext) + " " + pad(h.Command, w)
		if h.Note != "" {
			line += "  " + r.muted.Render(h.Note)
		}
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}
