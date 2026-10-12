package kube

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakekube "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

var (
	gvrPV          = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}
	gvrPVC         = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	gvrService     = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	gvrSecret      = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	gvrDeployment  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	gvrApplication = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
)

// fakeClient returns a Client backed by a fake discovery (with kubectl short names) and a
// fake dynamic client seeded with objects.
func fakeClient(t *testing.T, objects ...runtime.Object) (*Client, *fakedynamic.FakeDynamicClient) {
	t.Helper()
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "persistentvolumes", Kind: "PersistentVolume", ShortNames: []string{"pv"}, Verbs: allVerbs},
				{Name: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true, ShortNames: []string{"pvc"}, Verbs: allVerbs},
				{Name: "services", Kind: "Service", Namespaced: true, ShortNames: []string{"svc"}, Verbs: allVerbs},
				{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: allVerbs},
			},
		},
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				{Name: "deployments", Kind: "Deployment", Namespaced: true, ShortNames: []string{"deploy"}, Verbs: allVerbs},
			},
		},
		{
			GroupVersion: "argoproj.io/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "applications", Kind: "Application", Namespaced: true, ShortNames: []string{"app"}, Verbs: allVerbs},
			},
		},
	}}}

	listKinds := map[schema.GroupVersionResource]string{
		gvrPV:          "PersistentVolumeList",
		gvrPVC:         "PersistentVolumeClaimList",
		gvrService:     "ServiceList",
		gvrSecret:      "SecretList",
		gvrDeployment:  "DeploymentList",
		gvrApplication: "ApplicationList",
	}
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...)
	return &Client{dyn: dyn, mapper: newRESTMapper(disc)}, dyn
}

var allVerbs = metav1.Verbs{"get", "list", "create", "update", "patch", "delete"}

func object(apiVersion, kind, namespace, name string, fields map[string]interface{}) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{}}
	for k, v := range fields {
		obj.Object[k] = v
	}
	obj.SetAPIVersion(apiVersion)
	obj.SetKind(kind)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

type appliedObject struct {
	resource, namespace, name string
	body                      map[string]interface{}
}

// recordApplies captures server-side apply patches instead of tracking them.
func recordApplies(t *testing.T, dyn *fakedynamic.FakeDynamicClient) *[]appliedObject {
	t.Helper()
	var applied []appliedObject
	dyn.PrependReactor("patch", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		patch := action.(clienttesting.PatchAction)
		require.Equal(t, types.ApplyPatchType, patch.GetPatchType())
		body := map[string]interface{}{}
		require.NoError(t, json.Unmarshal(patch.GetPatch(), &body))
		applied = append(applied, appliedObject{patch.GetResource().Resource, patch.GetNamespace(), patch.GetName(), body})
		return true, &unstructured.Unstructured{Object: body}, nil
	})
	return &applied
}

func TestApplyBytes_MultiDocumentWithCRD(t *testing.T) {
	cli, dyn := fakeClient(t)
	applied := recordApplies(t, dyn)

	manifest := `---
apiVersion: v1
kind: Secret
metadata:
  name: areo-secret
stringData:
  token: "a---b"
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: areo-dags
  namespace: argocd
spec:
  project: default
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: postgres-data-pv
---
`
	require.NoError(t, cli.ApplyBytes(context.Background(), "airflow", []byte(manifest)))

	require.Len(t, *applied, 3)
	assert.Equal(t, appliedObject{"secrets", "airflow", "areo-secret", (*applied)[0].body}, (*applied)[0])
	assert.Equal(t, "a---b", (*applied)[0].body["stringData"].(map[string]interface{})["token"], "--- inside a value must not split documents")
	assert.Equal(t, "airflow", (*applied)[0].body["metadata"].(map[string]interface{})["namespace"], "default namespace is written into the object")
	assert.Equal(t, [3]string{"applications", "argocd", "areo-dags"}, [3]string{(*applied)[1].resource, (*applied)[1].namespace, (*applied)[1].name})
	assert.Equal(t, [3]string{"persistentvolumes", "", "postgres-data-pv"}, [3]string{(*applied)[2].resource, (*applied)[2].namespace, (*applied)[2].name})
}

func TestApplyBytes_Errors(t *testing.T) {
	cli, dyn := fakeClient(t)
	recordApplies(t, dyn)
	ctx := context.Background()

	err := cli.ApplyBytes(ctx, "", []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: s\n"))
	assert.True(t, errors.Is(err, ErrorMissingNamespace), "namespaced object without a namespace: %v", err)

	err = cli.ApplyBytes(ctx, "airflow", []byte("apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\n"))
	assert.ErrorContains(t, err, "Widget w")

	err = cli.ApplyBytes(ctx, "airflow", []byte("apiVersion: v1\nkind: Secret\n"))
	assert.ErrorContains(t, err, "metadata.name")
}

func TestGetResource_ShortAndQualifiedNames(t *testing.T) {
	app := object("argoproj.io/v1alpha1", "Application", "argocd", "areo-dags", map[string]interface{}{
		"status": map[string]interface{}{"sync": map[string]interface{}{"status": "Synced"}},
	})
	cli, _ := fakeClient(t, app)
	ctx := context.Background()

	for _, resource := range []string{"app", "application", "applications.argoproj.io", "applications.v1alpha1.argoproj.io"} {
		got, err := cli.GetResource(ctx, resource, "argocd", "areo-dags")
		require.NoError(t, err, resource)
		status, _, _ := unstructured.NestedString(got.Object, "status", "sync", "status")
		assert.Equal(t, "Synced", status, resource)
	}

	_, err := cli.GetResource(ctx, "app", "", "areo-dags")
	assert.True(t, errors.Is(err, ErrorMissingNamespace))
	_, err = cli.GetResource(ctx, "widgets", "argocd", "x")
	assert.ErrorContains(t, err, `resource "widgets"`)
}

func TestPatchMetadata_SetsAndRemoves(t *testing.T) {
	pvc := object("v1", "PersistentVolumeClaim", "airflow", "airflow-dags", nil)
	pvc.SetAnnotations(map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "{}", "keep": "me"})
	cli, _ := fakeClient(t, pvc)
	ctx := context.Background()

	err := cli.PatchMetadata(ctx, "pvc", "airflow", "airflow-dags", MetadataPatch{
		Labels:            map[string]string{"app.kubernetes.io/managed-by": "Helm"},
		Annotations:       map[string]string{"meta.helm.sh/release-name": "areo"},
		RemoveAnnotations: []string{"kubectl.kubernetes.io/last-applied-configuration"},
	})
	require.NoError(t, err)

	got, err := cli.GetResource(ctx, "pvc", "airflow", "airflow-dags")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"app.kubernetes.io/managed-by": "Helm"}, got.GetLabels())
	assert.Equal(t, map[string]string{"meta.helm.sh/release-name": "areo", "keep": "me"}, got.GetAnnotations())

	assert.Error(t, cli.PatchMetadata(ctx, "pvc", "airflow", "airflow-dags", MetadataPatch{}), "empty patch")
}

func TestMergePatch_ClearsClaimRef(t *testing.T) {
	pv := object("v1", "PersistentVolume", "", "postgres-data-pv", map[string]interface{}{
		"spec": map[string]interface{}{"claimRef": map[string]interface{}{"name": "old"}, "capacity": "10Gi"},
	})
	cli, _ := fakeClient(t, pv)

	got, err := cli.MergePatch(context.Background(), "pv", "", "postgres-data-pv", []byte(`{"spec":{"claimRef":null}}`))
	require.NoError(t, err)
	_, found, _ := unstructured.NestedMap(got.Object, "spec", "claimRef")
	assert.False(t, found, "claimRef must be removed")
	capacity, _, _ := unstructured.NestedString(got.Object, "spec", "capacity")
	assert.Equal(t, "10Gi", capacity)
}

func TestDeleteAll_OnlyListedResourcesInNamespace(t *testing.T) {
	cli, dyn := fakeClient(t,
		object("apps/v1", "Deployment", "airflow", "scheduler", nil),
		object("v1", "Service", "airflow", "api-server", nil),
		object("v1", "Service", "other", "keep-svc", nil),
		object("v1", "PersistentVolumeClaim", "airflow", "airflow-dags", nil),
	)
	ctx := context.Background()

	require.NoError(t, cli.DeleteAll(ctx, "airflow", "deployment", "svc"))

	_, err := dyn.Resource(gvrDeployment).Namespace("airflow").Get(ctx, "scheduler", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	_, err = dyn.Resource(gvrService).Namespace("airflow").Get(ctx, "api-server", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	_, err = dyn.Resource(gvrService).Namespace("other").Get(ctx, "keep-svc", metav1.GetOptions{})
	assert.NoError(t, err, "other namespaces are untouched")
	_, err = dyn.Resource(gvrPVC).Namespace("airflow").Get(ctx, "airflow-dags", metav1.GetOptions{})
	assert.NoError(t, err, "unlisted resources are untouched")

	err = cli.DeleteAll(ctx, "airflow", "widgets")
	assert.ErrorContains(t, err, `resource "widgets"`)
}

func TestMetadataPatchJSON(t *testing.T) {
	data, err := metadataPatchJSON(MetadataPatch{Labels: map[string]string{"a": "1"}, RemoveLabels: []string{"b"}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"metadata":{"labels":{"a":"1","b":null}}}`, string(data))
}

func pod(name string, ready corev1.ConditionStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "airflow", Labels: map[string]string{"component": "postgres"}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: ready}},
		},
	}
}

func TestWaitForPodsReady(t *testing.T) {
	old := PollInterval
	PollInterval = 10 * time.Millisecond
	defer func() { PollInterval = old }()

	short := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 100*time.Millisecond)
	}

	cli := &Client{cli: fakekube.NewSimpleClientset(pod("airflow-postgres-0", corev1.ConditionTrue))}
	ctx, cancel := short()
	defer cancel()
	assert.NoError(t, cli.WaitForPodsReady(ctx, "airflow", "component=postgres"))

	cli = &Client{cli: fakekube.NewSimpleClientset(pod("airflow-postgres-0", corev1.ConditionFalse))}
	ctx2, cancel2 := short()
	defer cancel2()
	err := cli.WaitForPodsReady(ctx2, "airflow", "component=postgres")
	assert.ErrorContains(t, err, "airflow-postgres-0 not ready")

	cli = &Client{cli: fakekube.NewSimpleClientset()}
	ctx3, cancel3 := short()
	defer cancel3()
	err = cli.WaitForPodsReady(ctx3, "airflow", "component=postgres")
	assert.ErrorContains(t, err, "no pod matched yet")
}

func TestDefaultContainer(t *testing.T) {
	p := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "istio-proxy"}, {Name: "postgres"}}}}
	assert.Equal(t, "istio-proxy", defaultContainer(p))
	p.Annotations = map[string]string{"kubectl.kubernetes.io/default-container": "postgres"}
	assert.Equal(t, "postgres", defaultContainer(p))
	assert.Equal(t, "", defaultContainer(&corev1.Pod{}))
}

func TestRootPackageDoesNotEmbedKubectl(t *testing.T) {
	// guards the binary size of importers: k8s.io/kubectl belongs in the kubectl subpackage
	out, err := runGoList(t)
	require.NoError(t, err)
	for _, dep := range strings.Fields(out) {
		assert.False(t, strings.HasPrefix(dep, "k8s.io/kubectl") || dep == "k8s.io/component-base/cli", "root package imports %s", dep)
	}
}

func runGoList(t *testing.T) (string, error) {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	return string(out), err
}
