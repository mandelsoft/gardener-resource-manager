// Copyright (c) 2020 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package managedresources_test

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	resourcesv1alpha1 "github.com/gardener/gardener-resource-manager/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener-resource-manager/pkg/controller/managedresources"
	mockclient "github.com/gardener/gardener-resource-manager/pkg/mock/controller-runtime/client"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
)

var _ = Describe("SecretReconciler", func() {
	var (
		ctrl *gomock.Controller
		c    *mockclient.MockClient

		r         *managedresources.SecretReconciler
		filter    *managedresources.ClassFilter
		secret    *corev1.Secret
		secretReq reconcile.Request
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		c = mockclient.NewMockClient(ctrl)

		filter = managedresources.NewClassFilter("seed")
		r = managedresources.NewSecretReconciler(log.NullLogger{}, filter)

		Expect(inject.ClientInto(c, r)).To(BeTrue())

		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "mr-ns",
				Name:      "mr-secret",
			},
		}
		secretReq = reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: secret.Namespace,
			Name:      secret.Name,
		}}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Describe("#InjectStopChannel", func() {
		It("should be able to inject a stop channel", func() {
			Expect(inject.StopChannelInto(context.TODO().Done(), r)).To(BeTrue())
		})
	})

	Describe("#Reconcile", func() {
		It("should do nothing if the secret has been deleted", func() {
			c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
				Return(apierrors.NewNotFound(corev1.Resource("secrets"), secret.Name))

			res, err := r.Reconcile(secretReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{
				Requeue: false,
			}))
		})

		It("should do nothing if secret get fails", func() {
			fakeErr := fmt.Errorf("fake")

			c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
				Return(fakeErr)

			_, err := r.Reconcile(secretReq)
			Expect(err).To(MatchError(ContainSubstring("fake")))
		})

		It("should do nothing if MR list fails", func() {
			fakeErr := fmt.Errorf("fake")

			gomock.InOrder(
				c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
					DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						secret.DeepCopyInto(obj.(*corev1.Secret))
						return nil
					}),
				c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
					Return(fakeErr),
			)

			_, err := r.Reconcile(secretReq)
			Expect(err).To(MatchError(ContainSubstring("fake")))
		})

		It("should do nothing if there is no MR in namespace", func() {
			gomock.InOrder(
				c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
					DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						secret.DeepCopyInto(obj.(*corev1.Secret))
						return nil
					}),
				c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
					Return(nil),
			)

			res, err := r.Reconcile(secretReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{
				Requeue: false,
			}))
		})

		It("should do nothing if there is no MR which we are responsible for", func() {
			mrs := []resourcesv1alpha1.ManagedResource{{
				Spec: resourcesv1alpha1.ManagedResourceSpec{
					Class: pointer.StringPtr("other"),
					SecretRefs: []corev1.LocalObjectReference{{
						Name: "foo",
					}},
				},
			}}

			gomock.InOrder(
				c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
					DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						secret.DeepCopyInto(obj.(*corev1.Secret))
						return nil
					}),
				c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
					DoAndReturn(func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
						list.(*resourcesv1alpha1.ManagedResourceList).Items = mrs
						return nil
					}),
			)

			res, err := r.Reconcile(secretReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{
				Requeue: false,
			}))
		})

		It("should do nothing if there is no MR referencing this secret", func() {
			mrs := []resourcesv1alpha1.ManagedResource{{
				Spec: resourcesv1alpha1.ManagedResourceSpec{
					Class: pointer.StringPtr(filter.ResourceClass()),
					SecretRefs: []corev1.LocalObjectReference{{
						Name: "foo",
					}},
				},
			}}

			gomock.InOrder(
				c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
					DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						secret.DeepCopyInto(obj.(*corev1.Secret))
						return nil
					}),
				c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
					DoAndReturn(func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
						list.(*resourcesv1alpha1.ManagedResourceList).Items = mrs
						return nil
					}),
			)

			res, err := r.Reconcile(secretReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{
				Requeue: false,
			}))
		})

		It("should do nothing if finalizer was already added", func() {
			secret.Finalizers = []string{filter.FinalizerName()}

			mrs := []resourcesv1alpha1.ManagedResource{{
				Spec: resourcesv1alpha1.ManagedResourceSpec{
					Class: pointer.StringPtr(filter.ResourceClass()),
					SecretRefs: []corev1.LocalObjectReference{{
						Name: secret.Name,
					}},
				},
			}}

			c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
				DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
					secret.DeepCopyInto(obj.(*corev1.Secret))
					return nil
				})
			c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
				DoAndReturn(func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
					list.(*resourcesv1alpha1.ManagedResourceList).Items = mrs
					return nil
				})

			res, err := r.Reconcile(secretReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{
				Requeue: false,
			}))
		})

		It("should add finalizer to secret if referenced by MR", func() {
			mrs := []resourcesv1alpha1.ManagedResource{{
				Spec: resourcesv1alpha1.ManagedResourceSpec{
					Class: pointer.StringPtr(filter.ResourceClass()),
					SecretRefs: []corev1.LocalObjectReference{{
						Name: secret.Name,
					}},
				},
			}}

			c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
				DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
					secret.DeepCopyInto(obj.(*corev1.Secret))
					return nil
				}).Times(2)
			c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
				DoAndReturn(func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
					list.(*resourcesv1alpha1.ManagedResourceList).Items = mrs
					return nil
				})
			c.EXPECT().Update(nil, gomock.AssignableToTypeOf(secret)).
				DoAndReturn(func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
					s := obj.(*corev1.Secret)
					Expect(s.Finalizers).To(ConsistOf(filter.FinalizerName()))
					return nil
				})

			res, err := r.Reconcile(secretReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{
				Requeue: false,
			}))
		})

		It("should do nothing if finalizer was already removed", func() {
			mrs := []resourcesv1alpha1.ManagedResource{{
				Spec: resourcesv1alpha1.ManagedResourceSpec{
					Class:      pointer.StringPtr(filter.ResourceClass()),
					SecretRefs: []corev1.LocalObjectReference{},
				},
			}}

			c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
				DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
					secret.DeepCopyInto(obj.(*corev1.Secret))
					return nil
				})
			c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
				DoAndReturn(func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
					list.(*resourcesv1alpha1.ManagedResourceList).Items = mrs
					return nil
				})

			res, err := r.Reconcile(secretReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{
				Requeue: false,
			}))
		})

		It("should remove finalizer from secret if reference was removed", func() {
			secret.Finalizers = []string{filter.FinalizerName()}

			mrs := []resourcesv1alpha1.ManagedResource{{
				Spec: resourcesv1alpha1.ManagedResourceSpec{
					Class:      pointer.StringPtr(filter.ResourceClass()),
					SecretRefs: []corev1.LocalObjectReference{},
				},
			}}

			c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
				DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
					secret.DeepCopyInto(obj.(*corev1.Secret))
					return nil
				}).Times(2)
			c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
				DoAndReturn(func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
					list.(*resourcesv1alpha1.ManagedResourceList).Items = mrs
					return nil
				})
			c.EXPECT().Update(nil, gomock.AssignableToTypeOf(secret)).
				DoAndReturn(func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
					s := obj.(*corev1.Secret)
					Expect(s.Finalizers).To(BeEmpty())
					return nil
				})

			res, err := r.Reconcile(secretReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{
				Requeue: false,
			}))
		})

		It("should remove finalizer from secret if class changed", func() {
			secret.Finalizers = []string{filter.FinalizerName()}

			mrs := []resourcesv1alpha1.ManagedResource{{
				Spec: resourcesv1alpha1.ManagedResourceSpec{
					Class: pointer.StringPtr("other"),
					SecretRefs: []corev1.LocalObjectReference{{
						Name: secret.Name,
					}},
				},
			}}

			c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
				DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
					secret.DeepCopyInto(obj.(*corev1.Secret))
					return nil
				}).Times(2)
			c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
				DoAndReturn(func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
					list.(*resourcesv1alpha1.ManagedResourceList).Items = mrs
					return nil
				})
			c.EXPECT().Update(nil, gomock.AssignableToTypeOf(secret)).
				DoAndReturn(func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
					s := obj.(*corev1.Secret)
					Expect(s.Finalizers).To(BeEmpty())
					return nil
				})

			res, err := r.Reconcile(secretReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{
				Requeue: false,
			}))
		})

		It("should fail if secret update fails", func() {
			fakeErr := fmt.Errorf("fake")

			secret.Finalizers = []string{filter.FinalizerName()}

			mrs := []resourcesv1alpha1.ManagedResource{{
				Spec: resourcesv1alpha1.ManagedResourceSpec{
					Class: pointer.StringPtr("other"),
					SecretRefs: []corev1.LocalObjectReference{{
						Name: secret.Name,
					}},
				},
			}}

			c.EXPECT().Get(nil, secretReq.NamespacedName, gomock.AssignableToTypeOf(&corev1.Secret{})).
				DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
					secret.DeepCopyInto(obj.(*corev1.Secret))
					return nil
				}).Times(2)
			c.EXPECT().List(nil, gomock.AssignableToTypeOf(&resourcesv1alpha1.ManagedResourceList{}), client.InNamespace(secret.Namespace)).
				DoAndReturn(func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
					list.(*resourcesv1alpha1.ManagedResourceList).Items = mrs
					return nil
				})
			c.EXPECT().Update(nil, gomock.AssignableToTypeOf(secret)).
				DoAndReturn(func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
					return fakeErr
				})

			res, err := r.Reconcile(secretReq)
			Expect(err).To(MatchError(ContainSubstring("fake")))
			Expect(res).To(Equal(reconcile.Result{
				RequeueAfter: 5 * time.Second,
			}))
		})
	})
})
