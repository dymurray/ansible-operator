/*
Copyright 2018 The Kubernetes Authors.

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

package client

import (
	"reflect"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// clientCache creates and caches rest clients and metadata for Kubernetes types
type clientCache struct {
	// config is the rest.Config to talk to an apiserver
	config *rest.Config

	// scheme maps go structs to GroupVersionKinds
	scheme *runtime.Scheme

	// mapper maps GroupVersionKinds to Resources
	mapper meta.RESTMapper

	// codecs are used to create a REST client for a gvk
	codecs serializer.CodecFactory

	muByType sync.RWMutex
	// resourceByType caches type metadata
	resourceByType map[reflect.Type]*resourceMeta

	muByGVK sync.RWMutex
	// resourceByGVK caches type metadata for unstructured
	unstructuredResourceByGVK map[schema.GroupVersionKind]*resourceMeta
}

// newResource maps obj to a Kubernetes Resource and constructs a client for that Resource.
// If the object is a list, the resource represents the item's type instead.
func (c *clientCache) newResource(obj runtime.Object, isUnstructured bool) (*resourceMeta, error) {
	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return nil, err
	}

	if strings.HasSuffix(gvk.Kind, "List") && meta.IsListType(obj) {
		// if this was a list, treat it as a request for the item's resource
		gvk.Kind = gvk.Kind[:len(gvk.Kind)-4]
	}

	var client rest.Interface
	if isUnstructured {
		client, err = apiutil.RESTUnstructuredClientForGVK(gvk, c.config)
	} else {
		client, err = apiutil.RESTClientForGVK(gvk, c.config, c.codecs)
	}
	if err != nil {
		return nil, err
	}
	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}
	return &resourceMeta{Interface: client, mapping: mapping, gvk: gvk}, nil
}

func (c *clientCache) getUnstructuredResourceByGVK(obj runtime.Object) (*resourceMeta, error) {
	// It's better to do creation work twice than to not let multiple
	// people make requests at once
	c.muByGVK.RLock()
	r, known := c.unstructuredResourceByGVK[obj.GetObjectKind().GroupVersionKind()]
	c.muByGVK.RUnlock()

	if known {
		return r, nil
	}

	// Initialize a new Client
	c.muByGVK.Lock()
	defer c.muByGVK.Unlock()
	r, err := c.newResource(obj, true)
	if err != nil {
		return nil, err
	}
	c.unstructuredResourceByGVK[obj.GetObjectKind().GroupVersionKind()] = r
	return r, err
}

func (c *clientCache) getResourceByType(obj runtime.Object) (*resourceMeta, error) {
	typ := reflect.TypeOf(obj)

	// It's better to do creation work twice than to not let multiple
	// people make requests at once
	c.muByType.RLock()
	r, known := c.resourceByType[typ]
	c.muByType.RUnlock()

	if known {
		return r, nil
	}

	// Initialize a new Client
	c.muByType.Lock()
	defer c.muByType.Unlock()
	r, err := c.newResource(obj, false)
	if err != nil {
		return nil, err
	}
	c.resourceByType[typ] = r
	return r, err
}

// getResource returns the resource meta information for the given type of object.
// If the object is a list, the resource represents the item's type instead.
func (c *clientCache) getResource(obj runtime.Object) (*resourceMeta, error) {
	_, isUnstructured := obj.(*unstructured.Unstructured)
	_, isUnstructuredList := obj.(*unstructured.UnstructuredList)
	if isUnstructured || isUnstructuredList {
		return c.getUnstructuredResourceByGVK(obj)
	}
	return c.getResourceByType(obj)
}

// getObjMeta returns objMeta containing both type and object metadata and state
func (c *clientCache) getObjMeta(obj runtime.Object) (*objMeta, error) {
	r, err := c.getResource(obj)
	if err != nil {
		return nil, err
	}
	m, err := meta.Accessor(obj)
	if err != nil {
		return nil, err
	}
	return &objMeta{resourceMeta: r, Object: m}, err
}

// resourceMeta caches state for a Kubernetes type.
type resourceMeta struct {
	// client is the rest client used to talk to the apiserver
	rest.Interface
	// gvk is the GroupVersionKind of the resourceMeta
	gvk schema.GroupVersionKind
	// mapping is the rest mapping
	mapping *meta.RESTMapping
}

// isNamespaced returns true if the type is namespaced
func (r *resourceMeta) isNamespaced() bool {
	if r.mapping.Scope.Name() == meta.RESTScopeNameRoot {
		return false
	}
	return true
}

// resource returns the resource name of the type
func (r *resourceMeta) resource() string {
	return r.mapping.Resource
}

// objMeta stores type and object information about a Kubernetes type
type objMeta struct {
	// resourceMeta contains type information for the object
	*resourceMeta

	// Object contains meta data for the object instance
	v1.Object
}
