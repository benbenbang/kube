// Package kubectl runs kubectl commands in-process.
//
// It lives apart from package kube because it embeds the whole kubectl command tree
// (k8s.io/kubectl), which multiplies the binary size of every program importing it.
// Import it only when a typed or dynamic helper in package kube cannot do the job.
package kubectl

import (
	"bytes"
	"fmt"
	"strings"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	kcli "k8s.io/component-base/cli"
	kcmd "k8s.io/kubectl/pkg/cmd"
	"k8s.io/kubectl/pkg/cmd/plugin"
)

// Run is like running `kubectl <args...>`: it returns stdout, and an error carrying
// stderr when the command fails.
func Run(args ...string) (string, error) {
	var stdout, stderr bytes.Buffer

	command := kcmd.NewDefaultKubectlCommandWithArgs(kcmd.KubectlOptions{
		PluginHandler: kcmd.NewDefaultPluginHandler(plugin.ValidPluginFilenamePrefixes),
		// kubectl reads these as os.Args to resolve plugins, program name first
		Arguments:   append([]string{"kubectl"}, args...),
		ConfigFlags: genericclioptions.NewConfigFlags(true),
		IOStreams: genericclioptions.IOStreams{
			In:     bytes.NewReader(nil),
			Out:    &stdout,
			ErrOut: &stderr,
		},
	})
	command.SetArgs(args)
	command.SetOut(&stdout)
	command.SetErr(&stderr)

	if err := kcli.RunNoErrOutput(command); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return stdout.String(), fmt.Errorf("kubectl %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}
