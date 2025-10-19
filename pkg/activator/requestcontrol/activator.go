package requestcontrol

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/log"

	types "github.com/llm-d-incubation/llm-d-activator/api/v1"
	v1 "sigs.k8s.io/gateway-api-inference-extension/api/v1"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/util/logging"

	autoscaling "k8s.io/api/autoscaling/v1"
	"k8s.io/client-go/discovery"
	cached "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/scale"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	ObjectApiVersionKey         = "activator.llm-d.ai/target-apiversion"
	ObjectkindKey               = "activator.llm-d.ai/target-kind"
	ObjectNameKey               = "activator.llm-d.ai/target-name"
	ScaleFromZeroGracePeriodKey = "activator.llm-d.ai/scale-from-zero-grace-period" // Optional annotation
	ScaleDownDelayKey           = "activator.llm-d.ai/scale-down-delay"             // Optional annotation
)

type ScaledObjectData struct {
	name             string
	scaleGracePeriod time.Duration
	numReplicas      int32
	scaleObject      *autoscaling.Scale
}

type activator struct {
	DynamicClient *dynamic.DynamicClient
	ScaleClient   scale.ScalesGetter
	Mapper        meta.RESTMapper
	Datastore     *Datastore

	// DefaultScaleToZeroGracePeriod is the time we will wait for a scale-to-zero decision to complete
	DefaultScaleToZeroGracePeriod time.Duration

	// DefaultScaleFromZeroGracePeriod is the time we will wait for a scale-from-zero decision to complete
	DefaultScaleFromZeroGracePeriod time.Duration

	// DefaultScaleDownDelay is the amount of time that must pass before a scale-down decision is applied
	DefaultScaleDownDelay time.Duration

	// ScaleToZeroRequestRetentionPeriod it is the amount of time we will wait before releasing the request after a scale from zero event
	ScaleToZeroRequestRetentionPeriod time.Duration
}

func newActivator(datastore *Datastore) (*activator, error) {
	config, err := getKubeConfig()

	if err != nil {
		return nil, err
	}

	scaleClient, mapper, err := InitScaleClient(config)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &activator{
		DynamicClient:                     dynamicClient,
		Mapper:                            mapper,
		Datastore:                         datastore,
		ScaleClient:                       scaleClient,
		DefaultScaleToZeroGracePeriod:     60 * time.Second,
		DefaultScaleFromZeroGracePeriod:   60 * time.Second,
		DefaultScaleDownDelay:             20 * time.Second,
		ScaleToZeroRequestRetentionPeriod: 5 * time.Second}, nil
}

func (a *activator) InferencePoolReady(ctx context.Context) bool {
	logger := log.FromContext(ctx)
	ds := *(a.Datastore)

	// Get InferencePool Info
	pool, err := ds.PoolGet()
	if err != nil {
		return false
	}

	namespace := pool.Namespace
	logger.V(logutil.TRACE).Info("InferencePool found", "name", pool.Name, "namespace", namespace)

	// verify required inferencePool annotations
	valid := a.verifyPoolObjectAnnotations(logger, pool)
	if !valid {
		return false
	}

	// extract optional inferencePool annotation if it exists, otherwise use a default value
	var scaleGracePeriod int
	if value, found := a.getOptionalPoolAnnotation(logger, ScaleFromZeroGracePeriodKey, pool); !found {
		scaleGracePeriod, _ = strconv.Atoi(value)
	} else {
		scaleGracePeriod = int(a.DefaultScaleFromZeroGracePeriod)
	}

	gvr, err := types.ParseGVR(a.Mapper, pool.Annotations[ObjectApiVersionKey], pool.Annotations[ObjectkindKey])
	if err != nil {
		msg := "Failed to parse Group, Version, Kind, Resource"
		logger.Error(err, msg, "apiVersion", pool.Annotations[ObjectApiVersionKey], "kind", pool.Annotations[ObjectkindKey])
	}

	gr := gvr.GroupResource()
	scaleObject, err := a.ScaleClient.Scales(namespace).Get(ctx, gr, pool.Annotations[ObjectNameKey], metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "Error getting scale subresource object")
		return true
	}

	if scaleObject.Spec.Replicas > 0 {
		if a.InferencePoolPodsReady(ctx, logger, namespace, pool.Annotations[ObjectNameKey], scaleObject.Spec.Replicas, scaleGracePeriod, gr, gvr) {
			// Scale object exists and has no zero running replicas then do not scale it
			logger.V(logutil.DEBUG).Info(fmt.Sprintf("Scale Object %s have at least one replica ready. Skipping scaling from zero", scaleObject.Name))
			return true
		}
	}

	// Scale inferencePool workload from zero to one replicas
	numReplicas := int32(1)
	scaleData := ScaledObjectData{name: pool.Annotations[ObjectNameKey], scaleGracePeriod: a.DefaultScaleFromZeroGracePeriod, numReplicas: numReplicas, scaleObject: scaleObject}

	// Start ticker or go routine because we scale a inferencePool from zero to one
	if a.ScaleInferencePool(ctx, logger, namespace, scaleData, gr, gvr) {
		logger.Info("Start Monitoring InferencePool Idleness")
		go a.MonitorInferencePoolIdleness(pool, scaleData, gr, gvr)
		return true
	}
	return false
}

func (a *activator) InferencePoolPodsReady(ctx context.Context, logger logr.Logger, namespace, objname string, numReplicas int32, scaleGracePeriod int, gr schema.GroupResource, gvr types.GroupVersionResource) bool {
	// Check if Scale Object for target inferencePool is Ready
	count := 0
	for {
		unstructuredObj, err := a.DynamicClient.Resource(gvr.GroupVersionResource()).Namespace(namespace).Get(ctx, objname, metav1.GetOptions{})
		if err != nil {
			logger.Error(err, "Error getting unstructured object")
		}

		if readyReplicas, ok := unstructuredObj.Object["status"].(map[string]interface{})["readyReplicas"].(int64); !ok {
			logger.Info("Object status.readyReplicas field is not set yet - candidate pods for serving the request are NOT READY ")
			continue
		} else {
			if numReplicas == int32(readyReplicas) {
				logger.Info(fmt.Sprintf("Candidate pods are READY - waiting ScaleToZeroRequestRetentionPeriod of '%s' before releasing the request", a.ScaleToZeroRequestRetentionPeriod))
				time.Sleep(a.ScaleToZeroRequestRetentionPeriod)
				return true
			} else {
				logger.Info("Candidate pods are NOT READY")
			}

			time.Sleep(1 * time.Second)
			count++
			if count > scaleGracePeriod {
				return false
			}
		}
	}
}

func (a *activator) ScaleInferencePool(ctx context.Context, logger logr.Logger, namespace string, objData ScaledObjectData, gr schema.GroupResource, gvr types.GroupVersionResource) bool {
	// Modify the desired replicas
	objData.scaleObject.Spec.Replicas = objData.numReplicas

	// Update the Scale object
	_, err := a.ScaleClient.Scales(namespace).Update(ctx, gr, objData.scaleObject, metav1.UpdateOptions{})
	if err != nil {
		logger.Error(err, "Error increasing Scale Object number of replicas to one")
		return false
	}
	logger.V(logutil.VERBOSE).Info(fmt.Sprintf("Scale Object %s in namespace %s scaled up to %d replicas with scale grace period %d \n", objData.name, namespace, objData.numReplicas, int(objData.scaleGracePeriod)))

	if objData.numReplicas == 0 {
		time.Sleep(objData.scaleGracePeriod)
		return true
	}

	return a.InferencePoolPodsReady(ctx, logger, namespace, objData.name, objData.numReplicas, int(objData.scaleGracePeriod), gr, gvr)
}

func getKubeConfig() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfigPath := clientcmd.RecommendedHomeFile
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, err
		}
	}
	return config, err
}

func InitScaleClient(config *rest.Config) (scale.ScalesGetter, meta.RESTMapper, error) {
	clientset, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	cachedDiscoveryClient := cached.NewMemCacheClient(clientset)
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscoveryClient)

	return scale.New(
		clientset.RESTClient(), restMapper,
		dynamic.LegacyAPIPathResolverFunc,
		scale.NewDiscoveryScaleKindResolver(clientset),
	), restMapper, nil
}

func (a *activator) verifyPoolObjectAnnotations(logger logr.Logger, pool *v1.InferencePool) bool {
	if _, ok := pool.Annotations[ObjectApiVersionKey]; !ok {
		logger.Info(fmt.Sprintf("Annotation '%s' not found on pool '%s'", ObjectApiVersionKey, pool.Name))
		return false
	}
	if _, ok := pool.Annotations[ObjectkindKey]; !ok {
		logger.Info(fmt.Sprintf("Annotation '%s' not found on pool '%s'", ObjectkindKey, pool.Name))
		return false
	}
	if _, ok := pool.Annotations[ObjectNameKey]; !ok {
		logger.Info(fmt.Sprintf("Annotation '%s' not found on pool '%s'", ObjectNameKey, pool.Name))
		return false
	}
	return true
}

func (a *activator) getOptionalPoolAnnotation(logger logr.Logger, annotationKey string, pool *v1.InferencePool) (string, bool) {
	if value, ok := pool.Annotations[annotationKey]; ok {
		return value, true
	}
	logger.Info(fmt.Sprintf("Annotation '%s' not found on pool '%s'", ObjectApiVersionKey, pool.Name))
	return "", false
}

func (a *activator) MonitorInferencePoolIdleness(pool *v1.InferencePool, objData ScaledObjectData, gr schema.GroupResource, gvr types.GroupVersionResource) {
	// Create a context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cancel is called when main exits

	logger := log.FromContext(ctx)

	ds := *(a.Datastore)
	objData.numReplicas = 0
	objData.scaleGracePeriod = a.DefaultScaleToZeroGracePeriod
	scaleDownDelay := a.DefaultScaleDownDelay

	ticker := time.NewTicker(time.Second * 30)
	defer ticker.Stop() // Ensure the ticker is stopped when the function exits

	for range ticker.C {
		logger.Info(fmt.Sprintf("Activator Time check for inferencePool idleness: %s", time.Now().Format("15:04:05")))

		reqRecordTime := ds.PoolGetRequestTime()
		if time.Since(reqRecordTime) > scaleDownDelay {

			scaleObject, err := a.ScaleClient.Scales(pool.Namespace).Get(ctx, gr, pool.Annotations[ObjectNameKey], metav1.GetOptions{})
			if err != nil {
				logger.Error(err, "Error getting scale subresource object")
			}
			objData.scaleObject = scaleObject

			// Scale inferencePool to zero replicas
			success := a.ScaleInferencePool(ctx, logger, pool.Namespace, objData, gr, gvr)
			if success {
				logger.Info(fmt.Sprintf("InferencePool '%s' was sucessfully scale down to zero replicas", pool.Name))
				//ticker.Stop()
				return
			} else {
				logger.Info(fmt.Sprintf("InferencePool '%s' was not sucessfully scale down to zero replicas", pool.Name))
			}
		}

	}
}
