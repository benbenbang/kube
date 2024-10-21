package kube

import (
	"bytes"
	"log"
	"os"
	"path/filepath"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/util/homedir"
	kcli "k8s.io/component-base/cli"
	kcmd "k8s.io/kubectl/pkg/cmd"
	"k8s.io/kubectl/pkg/cmd/plugin"
)

func GetKubeConfig() *Config {
	home := homedir.HomeDir()
	configPath := filepath.Join(home, ".kube", "config")

	flags := genericclioptions.NewConfigFlags(false)
	flags.KubeConfig = &configPath

	return NewConfig(flags)
}

func FromKubeConfig() *Client {
	cfg := GetKubeConfig()
	cli := New(cfg)
	_, err := cli.Dial()
	if err != nil {
		log.Fatal("unable to connect to kubernetes")
		os.Exit(1)
	}

	return cli
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
