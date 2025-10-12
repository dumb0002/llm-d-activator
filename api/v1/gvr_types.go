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

package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersionResource provides unified structure for schema.GroupVersion and Resource
type GroupVersionResource struct {
	Group    string `json:"group"`
	Version  string `json:"version"`
	Resource string `json:"resource"`
}

// GroupVersionKind returns the group, version and kind of GroupVersionKindResource
func (gvr GroupVersionResource) GroupVersionResource() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: gvr.Group, Version: gvr.Version, Resource: gvr.Resource}
}

// GroupVersion returns the group and version of GroupVersionKindResource
func (gvr GroupVersionResource) GroupVersion() schema.GroupVersion {
	return schema.GroupVersion{Group: gvr.Group, Version: gvr.Version}
}

// GroupResource returns the group and resource of GroupVersionKindResource
func (gvr GroupVersionResource) GroupResource() schema.GroupResource {
	return schema.GroupResource{Group: gvr.Group, Resource: gvr.Resource}
}

// GVKString returns the group, version and resource in string format
func (gvr GroupVersionResource) GVKString() string {
	return gvr.Group + "/" + gvr.Version + "." + gvr.Resource
}
