#!/usr/bin/env bash

#
# Endurance — run context
#
# There is exactly one Endurance banner and it is the ring ship in
# cli/internal/render/banner.go. A product with two banners has two identities,
# so this file no longer draws one: the ==== block with the rocket is gone, and
# what is left is the run's context as plain facts.
#
# When the CLI is framing the run (ENDURANCE_FRAMED, Phase 9) even those are
# suppressed — the Go banner has already said all of it.
#

print_banner() {

    if [[ -n "${ENDURANCE_FRAMED:-}" ]]; then
        return
    fi

    echo "${PLATFORM_NAME} ${PLATFORM_VERSION} · ${PLATFORM_ENVIRONMENT} · customer ${PLATFORM_CUSTOMER}"
    echo

}
