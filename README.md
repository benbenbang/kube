# kube

A simple Kubernetes client, based on [client-go](https://github.com/kubernetes/client-go).

## Quick Start

```go
package main

import (
    "context"
    "log"
    "os"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/cli-runtime/pkg/genericclioptions"

    "github.com/benbenbang/kube/v2"

)

func main() {
	os.Setenv("KUBE_CLIENT_KUBECONFIG_PATH", "testdata/config")

	cli, err := kube.FromKubeConfig()
	if err != nil {
		log.Fatal(err)
	}

	k8s, err := cli.Dial()
	if err != nil {
		log.Fatal(err)
	}

	// get a configmap named "configmapname"
	cm, err := k8s.CoreV1().ConfigMaps("default").Get(context.TODO(), "configmapname", metav1.GetOptions{})
	log.Println(cm.Data)

	// or
	cm, err = cli.GetConfigMap(context.TODO(), "default", "configmapname")
	log.Println(cm.Data)

	// apply file, is like "kubectl apply -f testdata/content-apply.yaml"
	err = cli.Apply("testdata/content-apply.yaml")
	if err != nil {
		log.Fatal(err)
	}

	// delete file, is like "kubectl delete -f testdata/content-apply.yaml"
	err = cli.Delete("testdata/content-apply.yaml")
	if err != nil {
		log.Fatal(err)
	}

	// Exec in a pod, is like "kubectl exec <pod name> -n <namespace> -c <container name> -- <command>"
	stdout, stderr, err := cli.Exec("podname", "containername", "namespace", "command")
	if err != nil {
		log.Println(stderr)
		log.Fatal(err)
	}
	log.Println(stdout)
}
```

## Kubeconfig

`FromKubeConfig` follows kubectl: `$KUBECONFIG` (colon-separated list), then `~/.kube/config`.
`KUBE_CLIENT_KUBECONFIG_PATH` overrides both with a single file.

## kubectl-style helpers

Resources take the same names as kubectl: short names (`pvc`, `pv`, `sa`), plurals, or
qualified names (`applications.argoproj.io`), custom resources included.

```go
ctx := context.Background()

// kubectl apply --server-side --force-conflicts -f - (multi-document, CRDs included)
err = cli.ApplyBytes(ctx, "default", manifest)

// kubectl get application areo-dags -n argocd -o yaml
app, err := cli.GetResource(ctx, "applications.argoproj.io", "argocd", "areo-dags")

// kubectl label/annotate pvc data --overwrite, and the `key-` removal form
err = cli.PatchMetadata(ctx, "pvc", "default", "data", kube.MetadataPatch{
    Labels:            map[string]string{"app.kubernetes.io/managed-by": "Helm"},
    RemoveAnnotations: []string{"kubectl.kubernetes.io/last-applied-configuration"},
})

// kubectl patch pv data-pv --type=merge -p '{"spec":{"claimRef":null}}'
_, err = cli.MergePatch(ctx, "pv", "", "data-pv", []byte(`{"spec":{"claimRef":null}}`))

// kubectl delete deployment,service --all -n default
err = cli.DeleteAll(ctx, "default", "deployment", "service")

// kubectl wait --for=condition=ready pod -l app=db (bounded by ctx, keeps polling while no pod matches)
err = cli.WaitForPodsReady(ctx, "default", "app=db")

// kubectl exec db-0 -- pg_isready -U postgres (argv, no shell)
stdout, stderr, err := cli.ExecCommand(ctx, "db-0", "", "default", []string{"pg_isready", "-U", "postgres"})
```

## Running kubectl in-process

`kube.Kubectl` moved to its own package in v2.1.0, so importing `kube` no longer embeds the
whole kubectl command tree. Import it only when no helper above fits:

```go
import "github.com/benbenbang/kube/v2/kubectl"

out, err := kubectl.Run("get", "pods", "-A")
```

## Test

Unit tests use fake clients and need no cluster:

```bash
go test ./...
```

Tests marked as needing a cluster are skipped unless a kubeconfig is given:

```bash
go test -v . -args -kubeconfig <kubeconfig file>

# Client.Exec
go test -v -coverprofile=coverage.out . -args -kubeconfig <kubeconfig file> -container <container name> -pod <pod name> -namespace <namespace>
```
