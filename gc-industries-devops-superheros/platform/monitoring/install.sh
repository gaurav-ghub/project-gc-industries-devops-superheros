#!/usr/bin/env bash

###############################################################################
# File: install.sh
#
# Description:
# Public entry point for the Monitoring module of the
# GC Industries Cloud Native Platform.
#
# Purpose:
# This script installs all monitoring capabilities required
# by the platform.
#
# Responsibilities:
# - Install Metrics Platform
# - Verify Monitoring Components
# - Prepare platform observability for customer applications
#
# Philosophy:
# The platform owns observability.
# Customer applications consume monitoring capabilities.
#
# Author: Gaurav Chaurasia
###############################################################################

MONITORING_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${MONITORING_DIR}/../scripts/utils.sh"

source "${MONITORING_DIR}/prometheus/install.sh"
source "${MONITORING_DIR}/verify.sh"

install_monitoring() {

    log_info "Installing platform observability..."

    echo

    install_metrics_platform

    echo

    log_success "Platform observability installed."

}

install_metrics_platform() {

    log_info "Installing metrics platform..."

    echo

    install_prometheus

    echo

    log_success "Metrics platform installed."

}

main() {

    install_monitoring

}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main
fi