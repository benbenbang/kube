package kube

import (
	"bytes"
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// Exec is like kubectl exec, running command through `sh -c`.
func (c *Client) Exec(pod, container, namespace, command string) (string, string, error) {
	return c.ExecCommand(context.TODO(), pod, container, namespace, []string{"sh", "-c", command})
}

// ExecCommand is like `kubectl exec <pod> -c <container> -- <argv...>`, without a shell.
// An empty container picks the kubectl.kubernetes.io/default-container annotation, then the
// first container, as kubectl does. A non-zero exit status is returned as an error.
func (c *Client) ExecCommand(ctx context.Context, pod, container, namespace string, argv []string) (string, string, error) {
	p, err := c.GetPod(ctx, namespace, pod)
	if err != nil {
		return "", "", err
	}
	if container == "" {
		container = defaultContainer(p)
	}
	req := c.cli.CoreV1().RESTClient().Post().
		Resource("pods").Name(p.Name).
		Namespace(p.Namespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		TypeMeta:  metav1.TypeMeta{},
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
		Container: container,
		Command:   argv,
	}, scheme.ParameterCodec)

	var (
		stderr bytes.Buffer
		stdout bytes.Buffer
	)
	restcfg, err := c.RestConfig()
	if err != nil {
		return "", "", err
	}
	exec, err := remotecommand.NewSPDYExecutor(restcfg, "POST", req.URL())
	if err != nil {
		return "", "", err
	}
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func defaultContainer(pod *corev1.Pod) string {
	if name := pod.Annotations["kubectl.kubernetes.io/default-container"]; name != "" {
		return name
	}
	if len(pod.Spec.Containers) > 0 {
		return pod.Spec.Containers[0].Name
	}
	return ""
}
