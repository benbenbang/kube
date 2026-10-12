package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

// FieldManager is the server-side apply field manager used by ApplyBytes.
const FieldManager = "kube"

// MetadataPatch sets and removes labels and annotations, like
// `kubectl label/annotate --overwrite` and their `key-` form.
type MetadataPatch struct {
	Labels            map[string]string
	Annotations       map[string]string
	RemoveLabels      []string
	RemoveAnnotations []string
}

// Dynamic returns the dynamic client, built on first use.
func (c *Client) Dynamic() (dynamic.Interface, error) {
	if c.dyn != nil {
		return c.dyn, nil
	}
	cfg, err := c.RestConfig()
	if err != nil {
		return nil, err
	}
	if c.dyn, err = dynamic.NewForConfig(cfg); err != nil {
		return nil, err
	}
	return c.dyn, nil
}

// RESTMapper returns a discovery-backed mapper, built on first use. It resolves kubectl
// short names (pv, pvc, sa, ...) and custom resources.
func (c *Client) RESTMapper() (meta.ResettableRESTMapper, error) {
	if c.mapper != nil {
		return c.mapper, nil
	}
	cfg, err := c.RestConfig()
	if err != nil {
		return nil, err
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	c.mapper = newRESTMapper(dc)
	return c.mapper, nil
}

func newRESTMapper(dc discovery.DiscoveryInterface) meta.ResettableRESTMapper {
	cached := memory.NewMemCacheClient(dc)
	return restmapper.NewShortcutExpander(restmapper.NewDeferredDiscoveryRESTMapper(cached), cached, nil).(meta.ResettableRESTMapper)
}

// ApplyBytes is like `kubectl apply --server-side --force-conflicts -f -`: it applies every
// document of a YAML or JSON manifest, custom resources included. Namespaced objects without
// a namespace go to defaultNamespace.
//
// Unlike Apply, it uses server-side apply instead of create-or-replace, so fields owned by
// other managers are kept.
func (c *Client) ApplyBytes(ctx context.Context, defaultNamespace string, manifest []byte) error {
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	for {
		raw := map[string]interface{}{}
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode manifest: %w", err)
		}
		if len(raw) == 0 {
			continue // empty document, e.g. a leading or trailing ---
		}
		if err := c.applyObject(ctx, defaultNamespace, &unstructured.Unstructured{Object: raw}); err != nil {
			return err
		}
	}
}

func (c *Client) applyObject(ctx context.Context, defaultNamespace string, obj *unstructured.Unstructured) error {
	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" || obj.GetName() == "" {
		return fmt.Errorf("manifest object needs kind and metadata.name")
	}
	mapper, err := c.RESTMapper()
	if err != nil {
		return err
	}
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if meta.IsNoMatchError(err) {
		// the kind may come from a CRD applied moments ago: refresh discovery once
		mapper.Reset()
		mapping, err = mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", gvk.Kind, obj.GetName(), err)
	}

	namespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
	if namespaced && obj.GetNamespace() == "" {
		if defaultNamespace == "" {
			return fmt.Errorf("%s %s: %w", gvk.Kind, obj.GetName(), ErrorMissingNamespace)
		}
		obj.SetNamespace(defaultNamespace)
	}
	ri, err := c.resourceInterface(mapping.Resource, namespaced, obj.GetNamespace())
	if err != nil {
		return err
	}
	data, err := obj.MarshalJSON()
	if err != nil {
		return err
	}
	force := true
	_, err = ri.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: FieldManager, Force: &force})
	if err != nil {
		return fmt.Errorf("apply %s %s: %w", gvk.Kind, obj.GetName(), err)
	}
	return nil
}

// GetResource is like `kubectl get <resource> <name> -o yaml`. resource takes the same forms as
// kubectl: a short name (pvc), a plural (persistentvolumeclaims) or a qualified name
// (applications.argoproj.io). namespace is ignored for cluster-scoped resources.
func (c *Client) GetResource(ctx context.Context, resource, namespace, name string) (*unstructured.Unstructured, error) {
	ri, err := c.resolve(resource, namespace)
	if err != nil {
		return nil, err
	}
	return ri.Get(ctx, name, metav1.GetOptions{})
}

// MergePatch is like `kubectl patch <resource> <name> --type=merge -p <patch>`.
func (c *Client) MergePatch(ctx context.Context, resource, namespace, name string, patch []byte) (*unstructured.Unstructured, error) {
	ri, err := c.resolve(resource, namespace)
	if err != nil {
		return nil, err
	}
	return ri.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
}

// PatchMetadata is like `kubectl label/annotate <resource> <name> --overwrite`, including
// the `key-` removal form.
func (c *Client) PatchMetadata(ctx context.Context, resource, namespace, name string, p MetadataPatch) error {
	patch, err := metadataPatchJSON(p)
	if err != nil {
		return err
	}
	_, err = c.MergePatch(ctx, resource, namespace, name, patch)
	return err
}

func metadataPatchJSON(p MetadataPatch) ([]byte, error) {
	section := func(set map[string]string, remove []string) map[string]interface{} {
		if len(set) == 0 && len(remove) == 0 {
			return nil
		}
		m := map[string]interface{}{}
		for k, v := range set {
			m[k] = v
		}
		for _, k := range remove {
			m[k] = nil // null removes the key in a JSON merge patch
		}
		return m
	}
	metadata := map[string]interface{}{}
	if labels := section(p.Labels, p.RemoveLabels); labels != nil {
		metadata["labels"] = labels
	}
	if annotations := section(p.Annotations, p.RemoveAnnotations); annotations != nil {
		metadata["annotations"] = annotations
	}
	if len(metadata) == 0 {
		return nil, fmt.Errorf("empty metadata patch")
	}
	return json.Marshal(map[string]interface{}{"metadata": metadata})
}

// DeleteAll is like `kubectl delete <resource,resource,...> --all -n <namespace>`.
// Objects are deleted one by one, since not every resource supports deletecollection.
// Missing objects are ignored; other errors are collected and returned together.
func (c *Client) DeleteAll(ctx context.Context, namespace string, resources ...string) error {
	var errs []error
	for _, resource := range resources {
		ri, err := c.resolve(resource, namespace)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		list, err := ri.List(ctx, metav1.ListOptions{})
		if err != nil {
			errs = append(errs, fmt.Errorf("list %s: %w", resource, err))
			continue
		}
		background := metav1.DeletePropagationBackground
		for _, item := range list.Items {
			err := ri.Delete(ctx, item.GetName(), metav1.DeleteOptions{PropagationPolicy: &background})
			if err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("delete %s %s: %w", resource, item.GetName(), err))
			}
		}
	}
	return errors.Join(errs...)
}

// resolve maps a kubectl-style resource name to a dynamic client scoped to namespace.
func (c *Client) resolve(resource, namespace string) (dynamic.ResourceInterface, error) {
	mapper, err := c.RESTMapper()
	if err != nil {
		return nil, err
	}
	gvr, err := resourceFor(mapper, resource)
	if err != nil {
		return nil, err
	}
	gvk, err := mapper.KindFor(gvr)
	if err != nil {
		return nil, fmt.Errorf("resource %q: %w", resource, err)
	}
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("resource %q: %w", resource, err)
	}
	namespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
	if namespaced && namespace == "" {
		return nil, fmt.Errorf("resource %q: %w", resource, ErrorMissingNamespace)
	}
	return c.resourceInterface(mapping.Resource, namespaced, namespace)
}

func resourceFor(mapper meta.RESTMapper, resource string) (schema.GroupVersionResource, error) {
	fullySpecified, gr := schema.ParseResourceArg(strings.ToLower(resource))
	if fullySpecified != nil {
		if gvr, err := mapper.ResourceFor(*fullySpecified); err == nil {
			return gvr, nil
		}
	}
	gvr, err := mapper.ResourceFor(gr.WithVersion(""))
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("resource %q: %w", resource, err)
	}
	return gvr, nil
}

func (c *Client) resourceInterface(gvr schema.GroupVersionResource, namespaced bool, namespace string) (dynamic.ResourceInterface, error) {
	dyn, err := c.Dynamic()
	if err != nil {
		return nil, err
	}
	if namespaced {
		return dyn.Resource(gvr).Namespace(namespace), nil
	}
	return dyn.Resource(gvr), nil
}
