// Package render is LaunchPad's terminal look — a restrained, Claude/Vercel-style
// visual system: a product banner, semantic icons, a small color palette, section
// rules, a step stream, and an end-of-run dashboard. Every user-facing line in the
// CLI goes through here so the whole tool feels like one product.
package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Semantic palette — deliberately small.
var (
	brand  = lipgloss.Color("99")  // purple — the product
	accent = lipgloss.Color("45")  // cyan — headings / active
	ok     = lipgloss.Color("42")  // green — success
	warn   = lipgloss.Color("214") // yellow — warning
	bad    = lipgloss.Color("196") // red — error
	muted  = lipgloss.Color("245") // gray — secondary
	faint  = lipgloss.Color("240") // dim — rules / hints
)

var (
	brandStyle = lipgloss.NewStyle().Foreground(brand).Bold(true)
	headStyle  = lipgloss.NewStyle().Foreground(accent).Bold(true)
	okStyle    = lipgloss.NewStyle().Foreground(ok)
	warnStyle  = lipgloss.NewStyle().Foreground(warn)
	badStyle   = lipgloss.NewStyle().Foreground(bad)
	mutedStyle = lipgloss.NewStyle().Foreground(muted)
	faintStyle = lipgloss.NewStyle().Foreground(faint)
)

// Banner prints the product logo box.
func Banner(version string) {
	title := brandStyle.Render("▸ LaunchPad") + mutedStyle.Render("  Internal Developer Platform")
	sub := faintStyle.Render("deploy any app to Kubernetes · " + version)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(brand).
		Padding(0, 2).
		Render(title + "\n" + sub)
	fmt.Println(box)
}

// Section prints a titled horizontal rule.
func Section(title string) {
	head := headStyle.Render("── " + title + " ")
	width := 58 - lipgloss.Width(head)
	if width < 0 {
		width = 0
	}
	fmt.Println("\n" + head + faintStyle.Render(strings.Repeat("─", width)))
}

// Step, Success, Warn, Error, Info are the semantic log primitives.
func Step(msg string)    { fmt.Println(headStyle.Render("▸") + " " + msg) }
func Success(msg string) { fmt.Println(okStyle.Render("✓") + " " + msg) }
func Warn(msg string)    { fmt.Println(warnStyle.Render("⚠") + " " + msg) }
func Error(msg string)   { fmt.Println(badStyle.Render("✗") + " " + msg) }
func Info(msg string)    { fmt.Println(mutedStyle.Render("·") + " " + msg) }

// Detail prints an indented secondary line under a step.
func Detail(msg string) { fmt.Println("  " + mutedStyle.Render(msg)) }

// Dashboard prints a boxed end-of-run summary: title, key/value rows, and
// next-step lines.
func Dashboard(title string, rows [][2]string, next []string) {
	var b strings.Builder
	b.WriteString(brandStyle.Render(title) + "\n\n")
	keyW := 0
	for _, r := range rows {
		if len(r[0]) > keyW {
			keyW = len(r[0])
		}
	}
	for _, r := range rows {
		key := mutedStyle.Render(fmt.Sprintf("%-*s", keyW, r[0]))
		b.WriteString(key + "  " + r[1] + "\n")
	}
	if len(next) > 0 {
		b.WriteString("\n" + headStyle.Render("Next") + "\n")
		for _, n := range next {
			b.WriteString(faintStyle.Render("→ ") + n + "\n")
		}
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(faint).
		Padding(0, 2).
		Render(strings.TrimRight(b.String(), "\n"))
	fmt.Println("\n" + box)
}

// Value styles a value for inline use in dashboards/logs.
func Value(s string) string { return lipgloss.NewStyle().Foreground(accent).Render(s) }
