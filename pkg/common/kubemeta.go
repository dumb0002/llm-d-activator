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

// Package common defines structs for referring to fully qualified k8s resources.
package common

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// GKNN represents a fully qualified k8s resource.
type GKNN struct {
	types.NamespacedName
	schema.GroupKind
}

// String implements Stringer.
func (g *GKNN) String() string {
	return fmt.Sprintf("%s %s", g.GroupKind.String(), g.NamespacedName.String())
}

// Compare returns the comparison of a and b where less than, equal, and greater than return -1, 0,
// and 1 respectively.
func Compare(a, b GKNN) int {
	if v := strings.Compare(a.Group, b.Group); v != 0 {
		return v
	}
	if v := strings.Compare(a.Kind, b.Kind); v != 0 {
		return v
	}
	if v := strings.Compare(a.Namespace, b.Namespace); v != 0 {
		return v
	}
	return strings.Compare(a.Name, b.Name)
}

// Less returns true if a is less than b.
func Less(a, b GKNN) bool {
	return Compare(a, b) < 0
}
