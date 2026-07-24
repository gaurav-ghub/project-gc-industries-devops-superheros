#!/usr/bin/env bash

#
# Endurance — platform plumbing output
#
# This file used to be half of Endurance's look: [INFO] tags in blue, [SUCCESS]
# in green, ==== rules between sections. Phase 8 ended that. There is one output
# library and it is the Go `render` package (cli/internal/render); the bash
# modules are plumbing, and plumbing does not get a visual identity of its own.
# Two products' worth of styling in one terminal is how the screenshots came to
# look like two different tools.
#
# So these functions no longer style anything. They emit one fact per line, and
# Endurance frames them: `endurance bootstrap` (Phase 9) runs these scripts as
# subprocesses piped into render.Stream(), which indents and mutes every line so
# the whole run reads in one voice.
#
# The contract with that renderer is deliberately tiny, small enough that it
# cannot drift:
#
#   * one fact per line — no banners, no rules, no boxes, no colour;
#   * a line that matters more than the rest opens with `warning:` or `error:`,
#     lowercase, so the Go side can promote it to ⚠ or ✗;
#   * warnings and errors go to stderr, everything else to stdout.
#
# The log_* names are kept because ~200 call sites use them; renaming them would
# have been churn with no reader on the other end.
#

log_info() {

    echo "$1"

}

log_success() {

    echo "$1"

}

log_warn() {

    echo "warning: $1" >&2

}

log_error() {

    echo "error: $1" >&2

}

###############################################################################
# Section helpers
#
# Kept so the ~30 call sites keep working. A section heading is a visual
# decision, and visual decisions live in Go now — there, the module a step
# belongs to is the step's own label. Here it is one plain line, and nothing at
# all when Endurance is doing the framing (ENDURANCE_FRAMED is set by the CLI).
###############################################################################

print_separator() {

    if [[ -z "${ENDURANCE_FRAMED:-}" ]]; then
        echo
    fi

}

print_section() {

    if [[ -n "${ENDURANCE_FRAMED:-}" ]]; then
        return
    fi

    echo
    echo "$1"

}

print_subsection() {

    if [[ -n "${ENDURANCE_FRAMED:-}" ]]; then
        return
    fi

    echo "$1"

}
