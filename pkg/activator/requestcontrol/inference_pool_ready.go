package requestcontrol

import (
	"context"
	"fmt"
	"time"

	errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/util/logging"

	"k8s.io/client-go/kubernetes"
)

func InferencePoolReady(ctx context.Context, ns, labelSelector string) bool {
	logger := log.FromContext(ctx)

	c, err := k8sClient()
	if err != nil {
		logger.V(logutil.VERBOSE).Error(err, "Error extracting kubeconfig")
		return false
	}

	dc := c.AppsV1().Deployments(ns)
	deployments, err := dc.List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})

	if err != nil {
		if errors.IsNotFound(err) {
			logger.V(logutil.VERBOSE).Error(err, "Deployment for candidate pods for serving the request does not exist.")
			return false
		} else {
			logger.V(logutil.VERBOSE).Error(err, "Error getting deployment for candidate pods for serving the request")
			return false
		}
	} else {
		oneReplicas := int32(1)

		if len(deployments.Items) == 0 {
			logger.V(logutil.VERBOSE).Error(err, "No deployment found for candidate pods for serving the request ")
			return false
		} else {
			deploy := deployments.Items[0] // assumption: only one deployment per inferencePool
			dpName := deploy.Name
			logger.V(logutil.VERBOSE).Info(fmt.Sprintf("Deployment '%s' in namespace '%s' found.\n", dpName, ns))

			// If candidate Pods deployment exists but has 0 replicas then set the number of replicas to 1
			if *deploy.Spec.Replicas == 0 {
				deploy.Spec.Replicas = &oneReplicas

				_, err = dc.Update(context.TODO(), &deploy, metav1.UpdateOptions{})
				if err != nil {
					logger.V(logutil.VERBOSE).Error(err, "Error scaling up the deployment replicas to one")
					return false
				}
				logger.V(logutil.VERBOSE).Info(fmt.Sprintf("Deployment '%s' in namespace '%s' scaled up to one replica.\n", dpName, ns))

				// Check if candidate Pods deployment for target inferencePool is Ready
				count := 0
				for {
					deploy, err := dc.Get(ctx, dpName, metav1.GetOptions{})
					if err != nil {
						panic(err)
					}

					if deploy != nil && *deploy.Spec.Replicas >= oneReplicas && *deploy.Spec.Replicas == deploy.Status.ReadyReplicas {
						logger.V(logutil.VERBOSE).Info("Deployment for candidate pods for serving the request is READY")
						return true
					} else {
						logger.V(logutil.VERBOSE).Info("Deployment for candidate pods for serving the request is NOT READY")
					}

					time.Sleep(1 * time.Second)
					count++
					if count > 30 {
						return false
					}
				}
			} else {
				// if candidate Pods deployment exists and has no zero replicas then do not scale the deployment
				logger.V(logutil.VERBOSE).Info(fmt.Sprintf("Deployment %s already exists and it has no zero replicas", dpName))
				return true
			}
		}
	}
}

func k8sClient() (*kubernetes.Clientset, error) {
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)
	return clientset, err
}
