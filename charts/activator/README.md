# Activator Chart

A chart to deploy the activator HTTP filter for an InferenceGateway and RBAC for per route activator deployments.

## Install

To install an activator named `activator`, you can run the following command:

```txt
$ helm install activator ./charts/activator
```

## Uninstall

Run the following command to uninstall the chart:

```txt
$ helm uninstall activator
```

## Configuration

The following table list the configurable parameters of the chart.

| **Parameter Name**                          | **Description**                                                                                    |
|---------------------------------------------|----------------------------------------------------------------------------------------------------|
| `name`                   | Name of the activator RBAC resources. Defaults to `activator`.  |

## Notes

This chart should only be deployed once.
