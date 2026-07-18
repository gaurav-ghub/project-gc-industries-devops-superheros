#!/usr/bin/env bash

MONITORING_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${MONITORING_DIR}/../../scripts/utils.sh"

install_prometheus() {

    if prometheus_installed; then

        log_warn "Prometheus is already installed."

        echo

        verify_prometheus_installation

        return

    fi

    perform_prometheus_installation

}

prometheus_installed() {

    helm status prometheus \
        --namespace monitoring \
        >/dev/null 2>&1

}

###############################################################################
# Installation
###############################################################################

perform_prometheus_installation() {

    ensure_prometheus_repo

    ensure_monitoring_namespace

    install_prometheus_chart

    wait_for_prometheus

    verify_prometheus_installation

}

###############################################################################
# Helm Repository
###############################################################################

ensure_prometheus_repo() {

    log_info "Checking Prometheus Helm repository..."

    echo

    if helm repo list | awk '{print $1}' | grep -qx "prometheus-community"; then

        log_info "Prometheus Helm repository already exists."

    else

        log_info "Adding Prometheus Helm repository..."

        if ! helm repo add prometheus-community \
            https://prometheus-community.github.io/helm-charts; then

            log_error "Failed to add Prometheus Helm repository."
            exit 1

        fi

        log_success "Prometheus Helm repository added."

    fi

    echo

    log_info "Updating Helm repositories..."

    if ! helm repo update; then

        log_error "Failed to update Helm repositories."
        exit 1
    fi

    echo

    helm repo list

    echo

    log_success "Prometheus Helm repository is ready."

}

###############################################################################
# Namespace
###############################################################################

ensure_monitoring_namespace() {

    if kubectl get namespace monitoring >/dev/null 2>&1; then

        log_info "Monitoring namespace already exists."

        return

    fi

    log_info "Creating monitoring namespace..."

    if ! kubectl create namespace monitoring; then

        log_error "Failed to create monitoring namespace."

        exit 1

    fi

    log_success "Monitoring namespace created."

}

###############################################################################
# Install Chart
###############################################################################

install_prometheus_chart() {

    log_info "Installing Prometheus..."

    echo

    if helm upgrade \
        --install prometheus \
        prometheus-community/kube-prometheus-stack \
        --namespace monitoring \
        --create-namespace \
        --values "${MONITORING_DIR}/../values/${PLATFORM_ENVIRONMENT}/prometheus-values.yaml"
    then

        echo

        log_success "Prometheus installed."

    else

        log_error "Failed to install Prometheus."

        exit 1

    fi

}

###############################################################################
# Wait
###############################################################################

wait_for_prometheus() {

    log_info "Waiting for Prometheus components to become Ready..."

    echo

    kubectl rollout status \
        deployment/prometheus-kube-prometheus-operator \
        -n monitoring \
        --timeout=300s

    kubectl rollout status \
        deployment/prometheus-grafana \
        -n monitoring \
        --timeout=300s

    echo

    log_success "Prometheus components are Ready."

}

###############################################################################
# Verification
###############################################################################

verify_prometheus_installation() {

    log_info "Verifying Prometheus installation..."

    echo

    verify_monitoring_namespace

    echo

    verify_prometheus_release

    echo

    verify_prometheus_pods

    echo

    wait_for_prometheus

    echo

    display_monitoring_information

    log_success "Prometheus installation verified."

}

verify_monitoring_namespace() {

    log_info "Checking monitoring namespace..."

    echo

    if ! kubectl get namespace monitoring; then

        log_error "Monitoring namespace not found."

        exit 1

    fi

    echo

    log_success "Monitoring namespace verified."

}

verify_prometheus_release() {

    log_info "Checking Prometheus Helm release..."

    echo

    if ! helm list \
        --namespace monitoring \
        --filter '^prometheus$'; then

        log_error "Prometheus Helm release not found."

        exit 1

    fi

    echo

    log_success "Prometheus Helm release verified."

}

verify_prometheus_pods() {

    log_info "Checking Prometheus pods..."

    echo

    kubectl get pods -n monitoring

    echo

    log_success "Prometheus pods are healthy."

}

###############################################################################
# Display Information
###############################################################################

display_monitoring_information() {

    print_section "Monitoring Information"

    echo

    display_monitoring_namespace

    echo

    display_grafana_information

    echo

    display_prometheus_information

    echo

    display_alertmanager_information

    echo

    display_useful_commands

    echo

    log_success "Monitoring information displayed."

}

display_monitoring_namespace() {

    print_subsection "Monitoring Namespace"

    echo

    kubectl get namespace monitoring

    echo

    log_success "Monitoring namespace displayed."

}

display_grafana_information() {

    print_subsection "Grafana"

    echo

    local password

    password=$(kubectl get secret prometheus-grafana \
        -n monitoring \
        -o jsonpath="{.data.admin-password}" \
        | base64 --decode)

    echo "URL:"
    echo "http://localhost:3000"

    echo

    echo "Username:"
    echo "admin"

    echo

    echo "Password:"
    echo "${password}"

    echo

    echo "Port Forward:"
    echo "kubectl port-forward svc/prometheus-grafana -n monitoring 3000:80"

    echo

    log_success "Grafana information displayed."

}


display_prometheus_information() {

    print_subsection "Prometheus"

    echo

    echo "URL:"
    echo "http://localhost:9090"

    echo

    echo "Port Forward:"
    echo "kubectl port-forward svc/prometheus-kube-prometheus-prometheus -n monitoring 9090:9090"

    echo

    log_success "Prometheus information displayed."

}


display_alertmanager_information() {

    print_subsection "Alertmanager"

    echo

    echo "URL:"
    echo "http://localhost:9093"

    echo

    echo "Port Forward:"
    echo "kubectl port-forward svc/prometheus-kube-prometheus-alertmanager -n monitoring 9093:9093"

    echo

    log_success "Alertmanager information displayed."

}

display_useful_commands() {

    print_subsection "Useful Commands"

    echo

    echo "View Monitoring Pods"
    echo "kubectl get pods -n monitoring"

    echo

    echo "View Monitoring Services"
    echo "kubectl get svc -n monitoring"

    echo

    echo "Grafana"
    echo "kubectl port-forward svc/prometheus-grafana -n monitoring 3000:80"

    echo

    echo "Prometheus"
    echo "kubectl port-forward svc/prometheus-kube-prometheus-prometheus -n monitoring 9090:9090"

    echo

    echo "Alertmanager"
    echo "kubectl port-forward svc/prometheus-kube-prometheus-alertmanager -n monitoring 9093:9093"

    echo

    log_success "Useful commands displayed."

}