// Copyright (c) 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils

import (
	"context"
	"fmt"
	"reflect"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// EvalGenericPredicate returns true if all predicates match for the given object.
func EvalGenericPredicate(obj client.Object, predicates ...predicate.Predicate) bool {
	e := event.GenericEvent{Object: obj}

	for _, p := range predicates {
		if !p.Generic(e) {
			return false
		}
	}

	return true
}

// EnsureFinalizer ensures that a finalizer of the given name is set on the given object.
// If the finalizer is not set, it adds it to the list of finalizers and updates the remote object.
func EnsureFinalizer(ctx context.Context, c client.Client, finalizerName string, obj client.Object) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}

		if controllerutil.ContainsFinalizer(obj, finalizerName) {
			return nil
		}

		beforePatch := obj.DeepCopyObject()

		controllerutil.AddFinalizer(obj, finalizerName)

		return c.Patch(ctx, obj, client.MergeFromWithOptions(beforePatch, client.MergeFromWithOptimisticLock{}))
	})
}

// DeleteFinalizer ensures that the given finalizer is not present anymore in the given object.
// If it is set, it removes it and issues an update.
func DeleteFinalizer(ctx context.Context, c client.Client, finalizerName string, obj client.Object) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); client.IgnoreNotFound(err) != nil {
			return err
		}

		if !controllerutil.ContainsFinalizer(obj, finalizerName) {
			return nil
		}

		beforePatch := obj.DeepCopyObject()

		controllerutil.RemoveFinalizer(obj, finalizerName)

		return c.Patch(ctx, obj, client.MergeFromWithOptions(beforePatch, client.MergeFromWithOptimisticLock{}))
	})
}

// TryUpdate tries to apply the given transformation function onto the given object, and to update it afterwards.
// It retries the update with an exponential backoff.
func TryUpdate(ctx context.Context, backoff wait.Backoff, c client.Client, obj client.Object, transform func() error) error {
	return tryUpdate(ctx, backoff, c, obj, c.Update, transform)
}

// TypedCreateOrUpdate is like controllerutil.CreateOrUpdate, it retrieves the current state of the object from the
// API server, applies the given mutate func and creates or updates it afterwards. In contrast to
// controllerutil.CreateOrUpdate it tries to create a new typed object of obj's kind (using the provided scheme)
// to make typed Get requests in order to leverage the client's cache.
func TypedCreateOrUpdate(ctx context.Context, c client.Client, scheme *runtime.Scheme, obj *unstructured.Unstructured, alwaysUpdate bool, mutate func() error) (controllerutil.OperationResult, error) {
	// client.DelegatingReader does not use its cache for unstructured.Unstructured objects, so we
	// create a new typed object of the object's type to use the cache for get calls before applying changes
	var current client.Object
	if typed, err := scheme.New(obj.GetObjectKind().GroupVersionKind()); err == nil {
		var ok bool
		current, ok = typed.(client.Object)
		if !ok {
			return controllerutil.OperationResultNone, fmt.Errorf("object type %q is unsupported", obj.GetObjectKind().GroupVersionKind().String())
		}
	} else {
		// fallback to unstructured request (type might not be registered in scheme)
		current = obj
	}

	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
		if !apierrors.IsNotFound(err) {
			return controllerutil.OperationResultNone, err
		}
		if err := mutate(); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultCreated, c.Create(ctx, obj)
	}

	var existing *unstructured.Unstructured

	// convert object back to unstructured for easy mutating/merging
	if _, isUnstructured := current.(*unstructured.Unstructured); !isUnstructured {
		u := &unstructured.Unstructured{}
		if err := scheme.Convert(current, u, nil); err != nil {
			return controllerutil.OperationResultNone, err
		}
		u.DeepCopyInto(obj)
		existing = u
	} else {
		existing = obj.DeepCopy()
	}

	if err := mutate(); err != nil {
		return controllerutil.OperationResultNone, err
	}

	if !alwaysUpdate && apiequality.Semantic.DeepEqual(existing, obj) {
		return controllerutil.OperationResultNone, nil
	}

	return controllerutil.OperationResultUpdated, c.Update(ctx, obj)
}

// TryUpdateStatus tries to apply the given transformation function onto the given object, and to update its
// status afterwards. It retries the status update with an exponential backoff.
func TryUpdateStatus(ctx context.Context, backoff wait.Backoff, c client.Client, obj client.Object, transform func() error) error {
	return tryUpdate(ctx, backoff, c, obj, c.Status().Update, transform)
}

func tryUpdate(ctx context.Context, backoff wait.Backoff, c client.Client, obj client.Object, updateFunc func(context.Context, client.Object, ...client.UpdateOption) error, transform func() error) error {
	key := client.ObjectKeyFromObject(obj)

	return exponentialBackoff(ctx, backoff, func() (bool, error) {
		if err := c.Get(ctx, key, obj); err != nil {
			return false, err
		}

		beforeTransform := obj.DeepCopyObject()
		if err := transform(); err != nil {
			return false, err
		}

		if reflect.DeepEqual(obj, beforeTransform) {
			return true, nil
		}

		if err := updateFunc(ctx, obj); err != nil {
			if apierrors.IsConflict(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
}

func exponentialBackoff(ctx context.Context, backoff wait.Backoff, condition wait.ConditionFunc) error {
	duration := backoff.Duration

	for i := 0; i < backoff.Steps; i++ {
		if ok, err := condition(); err != nil || ok {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			adjusted := duration
			if backoff.Jitter > 0.0 {
				adjusted = wait.Jitter(duration, backoff.Jitter)
			}
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * backoff.Factor)
		}

		i++
	}

	return wait.ErrWaitTimeout
}
