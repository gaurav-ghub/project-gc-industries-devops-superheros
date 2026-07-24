#!/usr/bin/env bash

#
# Endurance — colour is Go's, not bash's
#
# Endurance has one palette and it is declared in cli/internal/render/render.go:
# purple for the product, cyan for headings and values, green ok, yellow warn,
# red error, grey for everything secondary — including every line these scripts
# print, which the CLI renders muted inside its own frame (Phase 8 decision:
# "Go owns all rendering").
#
# A second palette here could only ever disagree with that one. So these names
# still exist — `set -u` would kill any script that still expands one — but they
# expand to nothing. Removing them entirely is a Phase 13 tidy-up, once nothing
# outside this repo sources the file.
#

RESET=""

BLACK=""
RED=""
GREEN=""
YELLOW=""
BLUE=""
PURPLE=""
CYAN=""
WHITE=""

BOLD=""
BOLD_RED=""
BOLD_GREEN=""
BOLD_YELLOW=""
BOLD_BLUE=""
BOLD_PURPLE=""
BOLD_CYAN=""
BOLD_WHITE=""
