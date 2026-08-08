#!/usr/bin/env bash

#
# Endurance DevX Platform
# Platform Information
#

readonly PLATFORM_NAME="Endurance"
readonly PLATFORM_TAGLINE="Mission Control for Every Application"
readonly PLATFORM_VERSION="1.0.0"
readonly PLATFORM_ENVIRONMENT="kind"
readonly PLATFORM_CUSTOMER="SuperHeroes"

#
# The cluster is named after the platform, not after an application on it.
#
# It was "superheros" until Phase 13, which is the name of the reference
# application — so a run that deployed `stark`, `portfolio` and `bad-app`
# announced `Cluster kind-superheros` on every screen and left the reader to
# work out that `superheros` was none of the three. One cluster, all
# applications, named for the thing that owns it.
#
# CHANGING THIS REQUIRES A CLUSTER RECREATE. kind fixes a cluster's name at
# creation time, so an existing kind-superheros is not renamed by editing this
# file — it is simply no longer the cluster Endurance is talking about. Run
# `endurance destroy` before `endurance bootstrap`, exactly as Phase 10's port
# mappings needed.
readonly CLUSTER_NAME="endurance"
readonly KUBERNETES_CONTEXT="kind-${CLUSTER_NAME}"