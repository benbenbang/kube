package kube

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// PollInterval is how often WaitForPodsReady checks the pods.
var PollInterval = 2 * time.Second

// WaitForPodsReady is like `kubectl wait --for=condition=ready pod -l <selector>`, bounded by
// ctx. Unlike kubectl it keeps polling while no pod matches yet, which is what an install wants
// right after creating a workload.
func (c *Client) WaitForPodsReady(ctx context.Context, namespace, selector string) error {
	cli, err := c.Dial()
	if err != nil {
		return err
	}
	state := "no pod matched yet"
	err = wait.PollUntilContextCancel(ctx, PollInterval, true, func(ctx context.Context) (bool, error) {
		pods, err := cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return false, err
		}
		if len(pods.Items) == 0 {
			state = "no pod matched yet"
			return false, nil
		}
		for i := range pods.Items {
			if !podReady(&pods.Items[i]) {
				state = fmt.Sprintf("pod %s not ready (%s)", pods.Items[i].Name, pods.Items[i].Status.Phase)
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("wait for pods %q in %s: %w: %s", selector, namespace, err, state)
	}
	return nil
}

func podReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
