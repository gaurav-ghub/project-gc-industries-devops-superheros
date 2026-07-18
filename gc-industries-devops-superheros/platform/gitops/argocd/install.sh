#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${SCRIPT_DIR}/../../scripts/utils.sh"


install_argocd() {

    log_info "Installing ArgoCD..."

    log_success "ArgoCD installed."

}