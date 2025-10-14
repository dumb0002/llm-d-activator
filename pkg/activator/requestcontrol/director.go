/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package requestcontrol defines the Director component responsible for orchestrating request processing after initial
// parsing.
package requestcontrol

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llm-d-incubation/llm-d-activator/pkg/activator/handlers"
	v1 "sigs.k8s.io/gateway-api-inference-extension/api/v1"
	errutil "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/util/error"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/util/logging"
)

// Datastore defines the interface required by the Director.
type Datastore interface {
	PoolGet() (*v1.InferencePool, error)
}

// NewDirectorWithConfig creates a new Director instance with all dependencies.
func NewDirectorWithConfig(datastore Datastore) *Director {
	activator, _ := newActivator()
	return &Director{
		datastore:       datastore,
		defaultPriority: 0, // define default priority explicitly
		activator:       activator,
	}
}

// Director orchestrates the request handling flow, including scheduling.
type Director struct {
	datastore Datastore
	// we just need a pointer to an int variable since priority is a pointer in InferenceObjective
	// no need to set this in the constructor, since the value we want is the default int val
	// and value types cannot be nil
	defaultPriority int
	activator       *activator
}

// HandleRequest orchestrates the request lifecycle.
// It always returns the requestContext even in the error case, as the request context is used in error handling.
func (d *Director) HandleRequest(ctx context.Context, reqCtx *handlers.RequestContext) (*handlers.RequestContext, error) {
	logger := log.FromContext(ctx)

	// Parse Request, Resolve Target Models, and Determine Parameters
	requestBodyMap := reqCtx.Request.Body
	var ok bool
	reqCtx.IncomingModelName, ok = requestBodyMap["model"].(string)

	if !ok {
		return reqCtx, errutil.Error{Code: errutil.BadRequest, Msg: "model not found in request body"}
	}
	if reqCtx.TargetModelName == "" {
		// Default to incoming model name
		reqCtx.TargetModelName = reqCtx.IncomingModelName
	}
	reqCtx.Request.Body["model"] = reqCtx.TargetModelName

	logger.V(logutil.VERBOSE).Info("Incoming Request info", "objectiveKey", reqCtx.ObjectiveKey, "incomingModelName", reqCtx.IncomingModelName, "targetModelName", reqCtx.TargetModelName)

	// Get InferencePool Info
	pool, err := d.datastore.PoolGet()
	if err != nil {
		return reqCtx, err
	}

	logger.V(logutil.VERBOSE).Info("InferencePool found", "name", pool.Name, "namespace", pool.Namespace)

	if ready := d.activator.InferencePoolReady(ctx, pool); !ready {
		return reqCtx, errutil.Error{Code: errutil.ServiceUnavailable, Msg: "failed to find active candidate pods in the inferencePool for serving the request"}
	}

	// TODO:
	//    1. Extend Datastore to keep track of the timestamp when an inferencePool receives a request
	//       - This value will be used to later scale down the deployment associated with an inferencePool if no requests are received after x seconds
	//    2. Add a client responsible to scale down an inferencePool if it does not receive any request after x seconds
	//    3. Add a queue to store pending requests - global queue or a local queue?
	return reqCtx, nil
}

func (d *Director) HandleResponse(ctx context.Context, reqCtx *handlers.RequestContext) (*handlers.RequestContext, error) {
	return reqCtx, nil
}
