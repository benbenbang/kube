package kube

import (
	"fmt"
	"os"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

// GetKubeConfig returns the kubeconfig used by FromKubeConfig. KUBE_CLIENT_KUBECONFIG_PATH
// points it at one file; otherwise the kubectl loading rules apply: $KUBECONFIG
// (colon-separated list), then ~/.kube/config.
func GetKubeConfig() *Config {
	flags := genericclioptions.NewConfigFlags(false)
	if configPath := os.Getenv("KUBE_CLIENT_KUBECONFIG_PATH"); configPath != "" {
		flags.KubeConfig = &configPath
	}

	return NewConfig(flags)
}

func FromKubeConfig() (*Client, error) {
	cfg := GetKubeConfig()
	cli := New(cfg)
	_, err := cli.Dial()

	return cli, err
}

func FromKubeConfigWithKubeConn() (*Client, *kubernetes.Interface, error) {
	cfg := GetKubeConfig()
	cli := New(cfg)
	conn, err := cli.Dial()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to connect to cluster. error message: %v", err)
	}

	return cli, &conn, nil
}
