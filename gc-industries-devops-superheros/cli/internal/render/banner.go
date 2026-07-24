package render

import "github.com/charmbracelet/lipgloss"

// Banner prints the Endurance product banner: the ring-ship logo — a nod to the
// Interstellar Endurance, a ring of modules around a central hub — beside the
// wordmark, the tagline and the version.
//
// Boxless on purpose. A box around a banner is a 2009 installer; art plus two
// quiet lines is a product.
func (r *Renderer) Banner(version string) {
	ring := lipgloss.JoinVertical(lipgloss.Left,
		r.head.Render("    · ─ ◦ ─ ·"),
		r.head.Render("  ◦           ◦"),
		r.head.Render(" ·      ")+r.brand.Render("✦")+r.head.Render("      · "),
		r.head.Render("  ◦           ◦"),
		r.head.Render("    · ─ ◦ ─ ·"),
	)
	word := lipgloss.JoinVertical(lipgloss.Left,
		"",
		r.brand.Render("E N D U R A N C E"),
		r.head.Render(Tagline),
		r.faint.Render("deploy any app to kind · "+version),
		"",
	)
	r.emit("\n" + lipgloss.JoinHorizontal(lipgloss.Center, ring, "    ", word) + "\n")
}
