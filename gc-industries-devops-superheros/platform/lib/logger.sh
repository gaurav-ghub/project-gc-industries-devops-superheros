#!/usr/bin/env bash

#
# LaunchPad DevX Platform
# Logging Framework
#

###############################################################################
# Logging Functions
###############################################################################

log_info() {

    echo -e "${BLUE}[INFO]${RESET} $1"

}

log_success() {

    echo -e "${GREEN}[SUCCESS]${RESET} $1"

}

log_warn() {

    echo -e "${YELLOW}[WARNING]${RESET} $1"

}

log_error() {

    echo -e "${RED}[ERROR]${RESET} $1"

}

###############################################################################
# Section Helpers
###############################################################################

print_separator() {

    echo
    printf '%*s\n' 57 '' | tr ' ' '='
    echo

}

print_section() {

    print_separator

    echo "$1"

    print_separator

}

print_subsection() {

    echo
    echo "---------------------------------------------------------"
    echo "$1"
    echo "---------------------------------------------------------"

}