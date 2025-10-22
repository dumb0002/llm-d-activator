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
	"github.com/llm-d-incubation/llm-d-activator/pkg/activator/datastore"
	v1 "sigs.k8s.io/gateway-api-inference-extension/api/v1"
	errutil "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/util/error"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/util/logging"

	autoscaling "k8s.io/api/autoscaling/v1"
	"k8s.io/client-go/discovery"
	cached "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/scale"
)

const (
	ObjectApiVersionKey         = "activator.llm-d.ai/target-apiversion"
	ObjectkindKey               = "activator.llm-d.ai/target-kind"
	ObjectNameKey               = "activator.llm-d.ai/target-name"
	ScaleFromZeroGracePeriodKey = "activator.llm-d.ai/scale-from-zero-grace-period" // Optional annotation
)

type ScaledObjectData struct {
	name             string
	scaleGracePeriod time.Duration
	numReplicas      int32
	scaleObject      *autoscaling.Scale
}

type Activator struct {
	DynamicClient *dynamic.DynamicClient
	ScaleClient   scale.ScalesGetter
	Mapper        meta.RESTMapper
	datastore     datastore.Datastore

	// DefaultScaleToZeroGracePeriod is the time we will wait for a scale-to-zero decision to complete
	DefaultScaleToZeroGracePeriod time.Duration

	// DefaultScaleFromZeroGracePeriod is the time we will wait for a scale-from-zero decision to complete
	DefaultScaleFromZeroGracePeriod time.Duration

	// DefaultScaleDownDelay is the amount of time that must pass before a scale-down decision is applied
	DefaultScaleDownDelay time.Duration

	// ScaleToZeroRequestRetentionPeriod it is the amount of time we will wait before releasing the request after a scale from zero event
	ScaleToZeroRequestRetentionPeriod time.Duration
}

func NewActivatorWithConfig(config *rest.Config, datastore datastore.Datastore) (*Activator, error) {
	scaleClient, mapper, err := InitScaleClient(config)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &Activator{
		datastore:                         datastore,
		DynamicClient:                     dynamicClient,
		Mapper:                            mapper,
		ScaleClient:                       scaleClient,
		DefaultScaleToZeroGracePeriod:     60 * time.Second,
		DefaultScaleFromZeroGracePeriod:   60 * time.Second,
		DefaultScaleDownDelay:             300 * time.Second,
		ScaleToZeroRequestRetentionPeriod: 5 * time.Second}, nil
}

// MayActivate checks if the inferencePool associated with the request is scaled to one or more replicas
func (a *Activator) MayActivate(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Get InferencePool Info
	pool, err := a.datastore.PoolGet()
	if err != nil {
		return err
	}

	logger.V(logutil.TRACE).Info("InferencePool found", "name", pool.Name, "namespace", pool.Namespace)

	if ready := a.InferencePoolReady(ctx, pool); !ready {
		return errutil.Error{Code: errutil.ServiceUnavailable, Msg: "failed to find active candidate pods in the inferencePool for serving the request"}
	}

	// TODO:
	//    1. Extend Datastore to keep track of the timestamp when an inferencePool receives a request
	//       - This value will be used to later scale down the deployment associated with an inferencePool if no requests are received after x seconds
	//    2. Add a client responsible to scale down an inferencePool if it does not receive any request after x seconds
	//    3. Add a queue to store pending requests - global queue or a local queue?
	return nil
}

func (a *Activator) InferencePoolReady(ctx context.Context, pool *v1.InferencePool) bool {
	logger := log.FromContext(ctx)
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

	gvr, err := types.GetResourceForKind(a.Mapper, pool.Annotations[ObjectApiVersionKey], pool.Annotations[ObjectkindKey])
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
	return a.ScaleInferencePool(ctx, logger, namespace, scaleData, gr, gvr)
}

func (a *Activator) InferencePoolPodsReady(ctx context.Context, logger logr.Logger, namespace, objname string, numReplicas int32, scaleGracePeriod int, gr schema.GroupResource, gvr schema.GroupVersionResource) bool {
	// Check if Scale Object for target inferencePool is Ready
	count := 0
	for {
		unstructuredObj, err := a.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, objname, metav1.GetOptions{})
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

func (a *Activator) ScaleInferencePool(ctx context.Context, logger logr.Logger, namespace string, objData ScaledObjectData, gr schema.GroupResource, gvr schema.GroupVersionResource) bool {
	// Modify the desired replicas
	objData.scaleObject.Spec.Replicas = objData.numReplicas

	// Update the Scale object
	_, err := a.ScaleClient.Scales(namespace).Update(ctx, gr, objData.scaleObject, metav1.UpdateOptions{})
	if err != nil {
		logger.Error(err, "Error increasing Scale Object number of replicas to one")
		return false
	}
	logger.V(logutil.VERBOSE).Info(fmt.Sprintf("Scale Object %s in namespace %s scaled up to %d replicas with scale grace period %d \n", objData.name, namespace, objData.numReplicas, int(objData.scaleGracePeriod)))

	return a.InferencePoolPodsReady(ctx, logger, namespace, objData.name, objData.numReplicas, int(objData.scaleGracePeriod), gr, gvr)
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

func (a *Activator) verifyPoolObjectAnnotations(logger logr.Logger, pool *v1.InferencePool) bool {
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

func (a *Activator) getOptionalPoolAnnotation(logger logr.Logger, annotationKey string, pool *v1.InferencePool) (string, bool) {
	if value, ok := pool.Annotations[annotationKey]; ok {
		return value, true
	}
	logger.Info(fmt.Sprintf("Annotation '%s' not found on pool '%s'", ObjectApiVersionKey, pool.Name))
	return "", false
}
