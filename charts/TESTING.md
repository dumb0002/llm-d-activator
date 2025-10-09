# TESTING.md

This file describes how to test the Helm charts.

1. Install the [simulated-accelerator llm-d guide](https://github.com/llm-d/llm-d/tree/main/guides/simulated-accelerators) by following the instructions in the guide. You can also use the provided script:

   ```bash
   $ export NAMESPACE=llm-d-sim
   $ ./install-simulated-accelerator.sh
   ```

1. (temporary) Add the InferencePool label selector to the `ms-sim-llm-d-modelservice-decode` deployment:

   ```bash
   $ kubectl label deployment ms-sim-llm-d-modelservice-decode llm-d.ai/inferenceServing="true" -n ${NAMESPACE}
   ```

1. Scale all model server deployments to zero replicas:

   ```bash
   $ kubectl scale deployment ms-sim-llm-d-modelservice-decode --replicas=0 -n ${NAMESPACE}
   $ kubectl scale deployment ms-sim-llm-d-modelservice-prefill --replicas=0 -n ${NAMESPACE}
   ```

1. Patch the `ms-sim-llm-d-modelservice-decode` deployment to reduce the startup probe periodSeconds to 5 seconds (instead of 30 seconds):

   ```bash
   $ kubectl patch deployment ms-sim-llm-d-modelservice-decode \
       --type='json' \
       -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/startupProbe/periodSeconds", "value": 5}]' \
       -n ${NAMESPACE}
   ```
1. Patch the `ms-sim-llm-d-modelservice-decode` deployment to reduce the the startup probe  initialDelaySeconds to 5 seconds (instead of 30 seconds):

   ```bash
   $ kubectl patch deployment ms-sim-llm-d-modelservice-decode \
       --type='json' \
       -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/startupProbe/initialDelaySeconds", "value": 5}]' \
       -n ${NAMESPACE}
   ```

1. Build and load the activator image into your kind cluster:

   ```bash
   $ make image-kind \
       KIND_CLUSTER="llm-d" \
       STAGING_IMAGE_REGISTRY="kind.local" \
       GIT_TAG="testing" \
       TARGETARCH=arm64 # or TARGETARCH=amd64 depending on your machine
   ```

1. Install the `activator-filter` chart:

   ```bash
   $ helm install activator-filter ./activator-filter \
       --set name=activator \
       --namespace ${NAMESPACE}
   ```

1. Install the `activator` chart for the `ms-sim-llm-d-modelservice` HTTP route:

   ```bash
   $ helm install activator ./activator \
       --set name=activator-route \
       --set activator.image.registry=kind.local/llm-d-activator \
       --set activator.image.name=activator \
       --set activator.image.tag=testing \
       --set activator.image.pullPolicy=Never \
       --set inferencePool.name=gaie-sim \
       --set route.name=ms-sim-llm-d-modelservice
    ```
1. Forward the gateway port to your local machine:

   ```bash
   $ GATEWAY_SVC=$(kubectl get svc -n "${NAMESPACE}" -o yaml | yq '.items[] | select(.metadata.name | test(".*-inference-gateway(-.*)?$")).metadata.name' | head -n1)
   $ kubectl port-forward -n "${NAMESPACE}" svc/"${GATEWAY_SVC}" 8000:80
   ```

1. Send a request to the gateway:

   ```bash
   $  curl -X POST localhost:8000/v1/completions \
            -H 'Content-Type: application/json' \
            -d '{
                  "model": "random",
                  "prompt": "How are you today?"
                }' | jq
   ```
   Observe that the request is completed successfully after 5 seconds and that the number of replicas for the `ms-sim-llm-d-modelservice-decode` deployment has been scaled to 1.
