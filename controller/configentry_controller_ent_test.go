// +build enterprise

package controller_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	logrtest "github.com/go-logr/logr/testing"
	"github.com/hashicorp/consul-k8s/api/common"
	"github.com/hashicorp/consul-k8s/api/v1alpha1"
	"github.com/hashicorp/consul-k8s/controller"
	capi "github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/sdk/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NOTE: We're not testing each controller type here because that's done in
// the OSS tests and it would result in too many permutations. Instead
// we're only testing with the ServiceDefaults and ProxyDefaults controller which will exercise
// all the namespaces code for config entries that are namespaced and those that
// exist in the global namespace.

func TestConfigEntryController_createsConfigEntry_consulNamespaces(tt *testing.T) {
	tt.Parallel()

	cases := map[string]struct {
		Mirror       bool
		MirrorPrefix string
		SourceKubeNS string
		DestConsulNS string
		ExpConsulNS  string
	}{
		"SourceKubeNS=default, DestConsulNS=default": {
			SourceKubeNS: "default",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, DestConsulNS=default": {
			SourceKubeNS: "kube",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=default, DestConsulNS=other": {
			SourceKubeNS: "default",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=kube, DestConsulNS=other": {
			SourceKubeNS: "kube",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=default, Mirror=true": {
			SourceKubeNS: "default",
			Mirror:       true,
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, Mirror=true": {
			SourceKubeNS: "kube",
			Mirror:       true,
			ExpConsulNS:  "kube",
		},
		"SourceKubeNS=default, Mirror=true, Prefix=prefix": {
			SourceKubeNS: "default",
			Mirror:       true,
			MirrorPrefix: "prefix-",
			ExpConsulNS:  "prefix-default",
		},
	}

	for name, c := range cases {
		configEntryKinds := map[string]struct {
			ConsulKind        string
			ConsulNamespace   string
			KubeResource      common.ConfigEntryResource
			GetController     func(client client.Client, logger logr.Logger, scheme *runtime.Scheme, cont *controller.ConfigEntryController) reconcile.Reconciler
			AssertValidConfig func(entry capi.ConfigEntry) bool
		}{
			"namespaced": {
				ConsulKind: capi.ServiceDefaults,
				KubeResource: &v1alpha1.ServiceDefaults{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo",
						Namespace: c.SourceKubeNS,
					},
					Spec: v1alpha1.ServiceDefaultsSpec{
						Protocol: "http",
					},
				},
				GetController: func(client client.Client, logger logr.Logger, scheme *runtime.Scheme, cont *controller.ConfigEntryController) reconcile.Reconciler {
					return &controller.ServiceDefaultsController{
						Client:                client,
						Log:                   logger,
						Scheme:                scheme,
						ConfigEntryController: cont,
					}
				},
				AssertValidConfig: func(cfg capi.ConfigEntry) bool {
					configEntry, ok := cfg.(*capi.ServiceConfigEntry)
					if !ok {
						return false
					}
					return configEntry.Protocol == "http"
				},
				ConsulNamespace: c.ExpConsulNS,
			},
			"global": {
				ConsulKind: capi.ProxyDefaults,
				KubeResource: &v1alpha1.ProxyDefaults{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "global",
						Namespace: c.SourceKubeNS,
					},
					Spec: v1alpha1.ProxyDefaultsSpec{
						MeshGateway: v1alpha1.MeshGatewayConfig{
							Mode: "remote",
						},
					},
				},
				GetController: func(client client.Client, logger logr.Logger, scheme *runtime.Scheme, cont *controller.ConfigEntryController) reconcile.Reconciler {
					return &controller.ProxyDefaultsController{
						Client:                client,
						Log:                   logger,
						Scheme:                scheme,
						ConfigEntryController: cont,
					}
				},
				AssertValidConfig: func(cfg capi.ConfigEntry) bool {
					configEntry, ok := cfg.(*capi.ProxyConfigEntry)
					if !ok {
						return false
					}
					return configEntry.MeshGateway.Mode == capi.MeshGatewayModeRemote
				},
				ConsulNamespace: common.DefaultConsulNamespace,
			},
		}

		for kind, in := range configEntryKinds {
			tt.Run(fmt.Sprintf("%s : %s", name, kind), func(t *testing.T) {
				req := require.New(t)
				s := runtime.NewScheme()
				s.AddKnownTypes(v1alpha1.GroupVersion, in.KubeResource)
				ctx := context.Background()

				consul, err := testutil.NewTestServerConfigT(t, nil)
				req.NoError(err)
				defer consul.Stop()
				consulClient, err := capi.NewClient(&capi.Config{
					Address: consul.HTTPAddr,
				})
				req.NoError(err)

				fakeClient := fake.NewFakeClientWithScheme(s, in.KubeResource)

				r := in.GetController(
					fakeClient,
					logrtest.TestLogger{T: t},
					s,
					&controller.ConfigEntryController{
						ConsulClient:               consulClient,
						EnableConsulNamespaces:     true,
						EnableNSMirroring:          c.Mirror,
						NSMirroringPrefix:          c.MirrorPrefix,
						ConsulDestinationNamespace: c.DestConsulNS,
					},
				)

				resp, err := r.Reconcile(ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: c.SourceKubeNS,
						Name:      in.KubeResource.Name(),
					},
				})
				req.NoError(err)
				req.False(resp.Requeue)

				cfg, _, err := consulClient.ConfigEntries().Get(in.ConsulKind, in.KubeResource.Name(), &capi.QueryOptions{
					Namespace: in.ConsulNamespace,
				})
				req.NoError(err)

				result := in.AssertValidConfig(cfg)
				req.True(result)

				// Check that the status is "synced".
				err = fakeClient.Get(ctx, types.NamespacedName{
					Namespace: c.SourceKubeNS,
					Name:      in.KubeResource.Name(),
				}, in.KubeResource)
				req.NoError(err)
				conditionSynced := in.KubeResource.SyncedConditionStatus()
				req.Equal(conditionSynced, corev1.ConditionTrue)
			})
		}
	}
}

func TestConfigEntryController_updatesConfigEntry_consulNamespaces(tt *testing.T) {
	tt.Parallel()

	cases := map[string]struct {
		Mirror       bool
		MirrorPrefix string
		SourceKubeNS string
		DestConsulNS string
		ExpConsulNS  string
	}{
		"SourceKubeNS=default, DestConsulNS=default": {
			SourceKubeNS: "default",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, DestConsulNS=default": {
			SourceKubeNS: "kube",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=default, DestConsulNS=other": {
			SourceKubeNS: "default",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=kube, DestConsulNS=other": {
			SourceKubeNS: "kube",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=default, Mirror=true": {
			SourceKubeNS: "default",
			Mirror:       true,
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, Mirror=true": {
			SourceKubeNS: "kube",
			Mirror:       true,
			ExpConsulNS:  "kube",
		},
		"SourceKubeNS=default, Mirror=true, Prefix=prefix": {
			SourceKubeNS: "default",
			Mirror:       true,
			MirrorPrefix: "prefix-",
			ExpConsulNS:  "prefix-default",
		},
	}

	for name, c := range cases {
		configEntryKinds := map[string]struct {
			ConsulKind            string
			ConsulNamespace       string
			KubeResource          common.ConfigEntryResource
			GetControllerFunc     func(client client.Client, logger logr.Logger, scheme *runtime.Scheme, cont *controller.ConfigEntryController) reconcile.Reconciler
			AssertValidConfigFunc func(entry capi.ConfigEntry) bool
			WriteConfigEntryFunc  func(consulClient *capi.Client, namespace string) error
			UpdateResourceFunc    func(client client.Client, ctx context.Context, in common.ConfigEntryResource) error
		}{
			"namespaced": {
				ConsulKind: capi.ServiceDefaults,
				KubeResource: &v1alpha1.ServiceDefaults{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "foo",
						Namespace:  c.SourceKubeNS,
						Finalizers: []string{controller.FinalizerName},
					},
					Spec: v1alpha1.ServiceDefaultsSpec{
						Protocol: "http",
					},
				},
				ConsulNamespace: c.ExpConsulNS,
				GetControllerFunc: func(client client.Client, logger logr.Logger, scheme *runtime.Scheme, cont *controller.ConfigEntryController) reconcile.Reconciler {
					return &controller.ServiceDefaultsController{
						Client:                client,
						Log:                   logger,
						Scheme:                scheme,
						ConfigEntryController: cont,
					}
				},
				WriteConfigEntryFunc: func(consulClient *capi.Client, namespace string) error {
					_, _, err := consulClient.ConfigEntries().Set(&capi.ServiceConfigEntry{
						Kind:     capi.ServiceDefaults,
						Name:     "foo",
						Protocol: "http",
					}, &capi.WriteOptions{Namespace: namespace})
					return err
				},
				UpdateResourceFunc: func(client client.Client, ctx context.Context, in common.ConfigEntryResource) error {
					svcDefault := in.(*v1alpha1.ServiceDefaults)
					svcDefault.Spec.Protocol = "tcp"
					return client.Update(ctx, svcDefault)
				},
				AssertValidConfigFunc: func(cfg capi.ConfigEntry) bool {
					configEntry, ok := cfg.(*capi.ServiceConfigEntry)
					if !ok {
						return false
					}
					return configEntry.Protocol == "tcp"
				},
			},
			"global": {
				ConsulKind: capi.ProxyDefaults,
				KubeResource: &v1alpha1.ProxyDefaults{
					ObjectMeta: metav1.ObjectMeta{
						Name:       common.Global,
						Namespace:  c.SourceKubeNS,
						Finalizers: []string{controller.FinalizerName},
					},
					Spec: v1alpha1.ProxyDefaultsSpec{
						MeshGateway: v1alpha1.MeshGatewayConfig{
							Mode: "remote",
						},
					},
				},
				ConsulNamespace: common.DefaultConsulNamespace,
				GetControllerFunc: func(client client.Client, logger logr.Logger, scheme *runtime.Scheme, cont *controller.ConfigEntryController) reconcile.Reconciler {
					return &controller.ProxyDefaultsController{
						Client:                client,
						Log:                   logger,
						Scheme:                scheme,
						ConfigEntryController: cont,
					}
				},
				WriteConfigEntryFunc: func(consulClient *capi.Client, namespace string) error {
					_, _, err := consulClient.ConfigEntries().Set(&capi.ProxyConfigEntry{
						Kind: capi.ProxyDefaults,
						Name: common.Global,
						MeshGateway: capi.MeshGatewayConfig{
							Mode: capi.MeshGatewayModeRemote,
						},
					}, &capi.WriteOptions{Namespace: namespace})
					return err
				},
				UpdateResourceFunc: func(client client.Client, ctx context.Context, in common.ConfigEntryResource) error {
					proxyDefaults := in.(*v1alpha1.ProxyDefaults)
					proxyDefaults.Spec.MeshGateway.Mode = "local"
					return client.Update(ctx, proxyDefaults)
				},
				AssertValidConfigFunc: func(cfg capi.ConfigEntry) bool {
					configEntry, ok := cfg.(*capi.ProxyConfigEntry)
					if !ok {
						return false
					}
					return configEntry.MeshGateway.Mode == capi.MeshGatewayModeLocal
				},
			},
		}
		for kind, in := range configEntryKinds {
			tt.Run(fmt.Sprintf("%s : %s", name, kind), func(t *testing.T) {
				req := require.New(t)
				s := runtime.NewScheme()
				s.AddKnownTypes(v1alpha1.GroupVersion, in.KubeResource)
				ctx := context.Background()

				consul, err := testutil.NewTestServerConfigT(t, nil)
				req.NoError(err)
				defer consul.Stop()
				consulClient, err := capi.NewClient(&capi.Config{
					Address: consul.HTTPAddr,
				})
				req.NoError(err)

				fakeClient := fake.NewFakeClientWithScheme(s, in.KubeResource)

				r := in.GetControllerFunc(
					fakeClient,
					logrtest.TestLogger{T: t},
					s,
					&controller.ConfigEntryController{
						ConsulClient:               consulClient,
						EnableConsulNamespaces:     true,
						EnableNSMirroring:          c.Mirror,
						NSMirroringPrefix:          c.MirrorPrefix,
						ConsulDestinationNamespace: c.DestConsulNS,
					},
				)

				// We haven't run reconcile yet so ensure it's created in Consul.
				{
					if in.ConsulNamespace != "default" {
						_, _, err := consulClient.Namespaces().Create(&capi.Namespace{
							Name: in.ConsulNamespace,
						}, nil)
						req.NoError(err)
					}

					err := in.WriteConfigEntryFunc(consulClient, in.ConsulNamespace)
					req.NoError(err)
				}

				// Now update it.
				{
					// First get it so we have the latest revision number.
					err = fakeClient.Get(ctx, types.NamespacedName{
						Namespace: c.SourceKubeNS,
						Name:      in.KubeResource.Name(),
					}, in.KubeResource)
					req.NoError(err)

					// Update the resource.
					err := in.UpdateResourceFunc(fakeClient, ctx, in.KubeResource)
					req.NoError(err)

					resp, err := r.Reconcile(ctrl.Request{
						NamespacedName: types.NamespacedName{
							Namespace: c.SourceKubeNS,
							Name:      in.KubeResource.Name(),
						},
					})
					req.NoError(err)
					req.False(resp.Requeue)

					cfg, _, err := consulClient.ConfigEntries().Get(in.ConsulKind, in.KubeResource.Name(), &capi.QueryOptions{
						Namespace: in.ConsulNamespace,
					})
					req.NoError(err)
					req.True(in.AssertValidConfigFunc(cfg))
				}
			})
		}
	}
}

func TestConfigEntryController_deletesConfigEntry_consulNamespaces(tt *testing.T) {
	tt.Parallel()

	cases := map[string]struct {
		Mirror       bool
		MirrorPrefix string
		SourceKubeNS string
		DestConsulNS string
		ExpConsulNS  string
	}{
		"SourceKubeNS=default, DestConsulNS=default": {
			SourceKubeNS: "default",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, DestConsulNS=default": {
			SourceKubeNS: "kube",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=default, DestConsulNS=other": {
			SourceKubeNS: "default",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=kube, DestConsulNS=other": {
			SourceKubeNS: "kube",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=default, Mirror=true": {
			SourceKubeNS: "default",
			Mirror:       true,
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, Mirror=true": {
			SourceKubeNS: "kube",
			Mirror:       true,
			ExpConsulNS:  "kube",
		},
		"SourceKubeNS=default, Mirror=true, Prefix=prefix": {
			SourceKubeNS: "default",
			Mirror:       true,
			MirrorPrefix: "prefix-",
			ExpConsulNS:  "prefix-default",
		},
	}

	for name, c := range cases {
		configEntryKinds := map[string]struct {
			ConsulKind           string
			ConsulNamespace      string
			KubeResource         common.ConfigEntryResource
			GetControllerFunc    func(client client.Client, logger logr.Logger, scheme *runtime.Scheme, cont *controller.ConfigEntryController) reconcile.Reconciler
			WriteConfigEntryFunc func(consulClient *capi.Client, namespace string) error
		}{
			"namespaced": {
				ConsulKind: capi.ServiceDefaults,
				// Create it with the deletion timestamp set to mimic that it's already
				// been marked for deletion.
				KubeResource: &v1alpha1.ServiceDefaults{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "foo",
						Namespace:         c.SourceKubeNS,
						Finalizers:        []string{controller.FinalizerName},
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
					Spec: v1alpha1.ServiceDefaultsSpec{
						Protocol: "http",
					},
				},
				ConsulNamespace: c.ExpConsulNS,
				GetControllerFunc: func(client client.Client, logger logr.Logger, scheme *runtime.Scheme, cont *controller.ConfigEntryController) reconcile.Reconciler {
					return &controller.ServiceDefaultsController{
						Client:                client,
						Log:                   logger,
						Scheme:                scheme,
						ConfigEntryController: cont,
					}
				},
				WriteConfigEntryFunc: func(consulClient *capi.Client, namespace string) error {
					_, _, err := consulClient.ConfigEntries().Set(&capi.ServiceConfigEntry{
						Kind:     capi.ServiceDefaults,
						Name:     "foo",
						Protocol: "http",
					}, &capi.WriteOptions{Namespace: namespace})
					return err
				},
			},
			"global": {
				ConsulKind: capi.ProxyDefaults,
				// Create it with the deletion timestamp set to mimic that it's already
				// been marked for deletion.
				KubeResource: &v1alpha1.ProxyDefaults{
					ObjectMeta: metav1.ObjectMeta{
						Name:              common.Global,
						Namespace:         c.SourceKubeNS,
						Finalizers:        []string{controller.FinalizerName},
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
					Spec: v1alpha1.ProxyDefaultsSpec{
						MeshGateway: v1alpha1.MeshGatewayConfig{
							Mode: "remote",
						},
					},
				},
				ConsulNamespace: common.DefaultConsulNamespace,
				GetControllerFunc: func(client client.Client, logger logr.Logger, scheme *runtime.Scheme, cont *controller.ConfigEntryController) reconcile.Reconciler {
					return &controller.ProxyDefaultsController{
						Client:                client,
						Log:                   logger,
						Scheme:                scheme,
						ConfigEntryController: cont,
					}
				},
				WriteConfigEntryFunc: func(consulClient *capi.Client, namespace string) error {
					_, _, err := consulClient.ConfigEntries().Set(&capi.ServiceConfigEntry{
						Kind: capi.ServiceDefaults,
						Name: common.Global,
						MeshGateway: capi.MeshGatewayConfig{
							Mode: capi.MeshGatewayModeRemote,
						},
					}, &capi.WriteOptions{Namespace: namespace})
					return err
				},
			},
		}
		for kind, in := range configEntryKinds {
			tt.Run(fmt.Sprintf("%s : %s", name, kind), func(t *testing.T) {
				req := require.New(t)

				s := runtime.NewScheme()
				s.AddKnownTypes(v1alpha1.GroupVersion, in.KubeResource)

				consul, err := testutil.NewTestServerConfigT(t, nil)
				req.NoError(err)
				defer consul.Stop()
				consulClient, err := capi.NewClient(&capi.Config{
					Address: consul.HTTPAddr,
				})
				req.NoError(err)

				fakeClient := fake.NewFakeClientWithScheme(s, in.KubeResource)

				r := in.GetControllerFunc(
					fakeClient,
					logrtest.TestLogger{T: t},
					s,
					&controller.ConfigEntryController{
						ConsulClient:               consulClient,
						EnableConsulNamespaces:     true,
						EnableNSMirroring:          c.Mirror,
						NSMirroringPrefix:          c.MirrorPrefix,
						ConsulDestinationNamespace: c.DestConsulNS,
					},
				)

				// We haven't run reconcile yet so ensure it's created in Consul.
				{
					if in.ConsulNamespace != "default" {
						_, _, err := consulClient.Namespaces().Create(&capi.Namespace{
							Name: in.ConsulNamespace,
						}, nil)
						req.NoError(err)
					}

					err := in.WriteConfigEntryFunc(consulClient, in.ConsulNamespace)
					req.NoError(err)
				}

				// Now run reconcile. It's marked for deletion so this should delete it.
				{
					resp, err := r.Reconcile(ctrl.Request{
						NamespacedName: types.NamespacedName{
							Namespace: c.SourceKubeNS,
							Name:      in.KubeResource.Name(),
						},
					})
					req.NoError(err)
					req.False(resp.Requeue)

					_, _, err = consulClient.ConfigEntries().Get(in.ConsulKind, in.KubeResource.Name(), &capi.QueryOptions{
						Namespace: in.ConsulNamespace,
					})
					req.EqualError(err, fmt.Sprintf(`Unexpected response code: 404 (Config entry not found for "%s" / "%s")`, in.ConsulKind, in.KubeResource.Name()))
				}
			})
		}
	}
}