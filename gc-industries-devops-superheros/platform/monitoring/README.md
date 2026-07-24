# Monitoring Platform

This module provisions the complete observability stack for the Endurance.

## Components

- Prometheus
- Grafana
- Loki
- Tempo
- Alertmanager
- Kiali

## Supported Environments

- Kind
- Amazon EKS

## Installation

Installation is performed using the scripts in the `scripts/` directory.

Environment-specific configuration is located under:

- values/base
- values/kind
- values/eks