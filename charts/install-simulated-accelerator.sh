#!/bin/bash

# This is a script to automate installation of the simulated accelerators guide.

set +x
set -e
set -o pipefail

# Logging functions and ASCII color helpers.
COLOR_RESET=$'\e[0m'
COLOR_GREEN=$'\e[32m'
COLOR_RED=$'\e[31m'

log_success() {
  echo "${COLOR_GREEN}✅ $*${COLOR_RESET}"
}

log_error() {
  echo "${COLOR_RED}❌ $*${COLOR_RESET}" >&2
}

kind create cluster --name llm-d
log_success "Kind cluster 'llm-d' created."

TMP_DIR=$(mktemp -d)

git clone https://github.com/llm-d/llm-d.git ${TMP_DIR}
cd ${TMP_DIR}
git checkout v0.3
log_success "Cloned llm-d repository."

pushd guides/prereq/gateway-provider > /dev/null
./install-gateway-provider-dependencies.sh # Installs the CRDs
helmfile apply -f istio.helmfile.yaml
popd
log_success "Istio gateway installed."


pushd docs/monitoring > /dev/null
./install-prometheus-grafana.sh
popd
log_success "Prometheus and Grafana installed."

pushd guides/simulated-accelerators > /dev/null
export NAMESPACE=llm-d-sim
kubectl create namespace ${NAMESPACE} || echo "Namespace ${NAMESPACE} already exists."
helmfile apply -n ${NAMESPACE}
popd
log_success "Simulated accelerators installed in namespace ${NAMESPACE}."

kubectl -n ${NAMESPACE} set image deployment ms-sim-llm-d-modelservice-decode routing-proxy=ghcr.io/llm-d/llm-d-routing-sidecar:v0.3.0
log_success "Patched simulated-accelerator deployment to use image ghcr.io/llm-d/llm-d-routing-sidecar:v0.3.0."

log_success "Installation of simulated accelerators completed successfully."
