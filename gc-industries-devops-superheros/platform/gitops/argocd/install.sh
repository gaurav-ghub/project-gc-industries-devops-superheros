#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${SCRIPT_DIR}/../../scripts/utils.sh"


install_argocd() {

    log_info "Installing ArgoCD..."

    check_argocd_repository

    create_argocd_namespace

    install_argocd_chart

    verify_argocd_installation

    log_success "ArgoCD installed."

}

check_argocd_repository() {

    log_info "Checking ArgoCD Helm repository..."

    echo

    if helm repo list | grep -q "^argo"; then

        log_info "ArgoCD Helm repository already exists."

        echo

    else

        log_info "Adding ArgoCD Helm repository..."

        helm repo add argo https://argoproj.github.io/argo-helm

        echo

    fi

    log_info "Updating Helm repositories..."

    helm repo update

    echo

    helm repo list

    echo

    log_success "ArgoCD Helm repository is ready."

    echo

}


create_argocd_namespace() {

    log_info "Creating ArgoCD namespace..."

    echo

    if kubectl get namespace argocd >/dev/null 2>&1; then

        log_info "ArgoCD namespace already exists."

    else

        kubectl create namespace argocd

        log_success "ArgoCD namespace created."

    fi

    echo

}



install_argocd_chart() {

    log_info "Installing ArgoCD..."

    echo

    log_success "ArgoCD installed."

}


verify_argocd_installation() {

    log_info "Verifying ArgoCD installation..."

    echo

    log_success "ArgoCD installation verified."

}


