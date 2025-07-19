package kube

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/homedir"
	kcli "k8s.io/component-base/cli"
	kcmd "k8s.io/kubectl/pkg/cmd"
	"k8s.io/kubectl/pkg/cmd/plugin"
)

func GetKubeConfig() *Config {
	configPath := os.Getenv("KUBE_CLIENT_KUBECONFIG_PATH")
	if configPath == "" {
		home := homedir.HomeDir()
		configPath = filepath.Join(home, ".kube", "config")
	}
	flags := genericclioptions.NewConfigFlags(false)
	flags.KubeConfig = &configPath

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

func Kubectl(cmdRaw []string) string {
	buffer := new(bytes.Buffer)

	command := kcmd.NewDefaultKubectlCommandWithArgs(kcmd.KubectlOptions{
		PluginHandler: kcmd.NewDefaultPluginHandler(plugin.ValidPluginFilenamePrefixes),
		Arguments:     os.Args,
		ConfigFlags:   genericclioptions.NewConfigFlags(true),
		IOStreams: genericclioptions.IOStreams{
			In:     os.Stdin,
			Out:    buffer,
			ErrOut: os.Stderr,
		},
	})

	err := new(bytes.Buffer)

	command.SetArgs(cmdRaw)
	command.SetErr(err)
	kcli.RunNoErrOutput(command)

	return buffer.String()
}
