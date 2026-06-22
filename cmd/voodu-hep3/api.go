package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const apiHelp = `vd hep3:api <start|stop|restart> <scope/name>

Manage the reader API pod (the PAT-proxy backend that serves call lists,
ladder diagrams and stats over the shared Postgres).

  start    build the local image (if needed) and start the pod
  stop     stop the pod
  restart  rebuild the local image and restart the pod (picks up a new
           binary version)

The pod itself is declared with a hep3 block and created by vd apply —
these commands manage its runtime + the local image build.
`

// cmdAPI dispatches the hep3:api lifecycle subcommands.
func cmdAPI() error {
	args := os.Args[2:]

	if len(args) == 0 || hasHelpFlag(args) {
		fmt.Print(apiHelp)

		return nil
	}

	switch args[0] {
	case "start":
		return apiStart(args[1:])
	case "stop":
		return apiStop(args[1:])
	case "restart":
		return apiRestart(args[1:])
	default:
		return fmt.Errorf("unknown hep3:api subcommand %q (want start|stop|restart)", args[0])
	}
}

func apiRef(args []string) (scope, name string, err error) {
	if len(args) < 1 {
		return "", "", fmt.Errorf("usage: vd hep3:api <start|stop|restart> <scope/name>")
	}

	scope, name = splitScopeName(args[0])
	if name == "" {
		return "", "", fmt.Errorf("invalid ref %q (expected scope/name)", args[0])
	}

	return scope, name, nil
}

// apiRestart rebuilds the local image then rolling-restarts the pod, so a
// new binary version is picked up.
func apiRestart(args []string) error {
	scope, name, err := apiRef(args)
	if err != nil {
		return err
	}

	ctx, err := readInvocationContext()
	if err != nil {
		return err
	}

	if err := buildLocalImage(ctx.PluginDir); err != nil {
		return err
	}

	if err := newControllerClient(ctx.ControllerURL).restart("deployment", scope, name); err != nil {
		return err
	}

	emitOK(map[string]any{"message": fmt.Sprintf("hep3 api %s/%s: image rebuilt + restarted", scope, name)})

	return nil
}

// apiStart ensures the local image exists and starts the pod(s).
func apiStart(args []string) error {
	scope, name, err := apiRef(args)
	if err != nil {
		return err
	}

	ctx, err := readInvocationContext()
	if err != nil {
		return err
	}

	if err := buildLocalImage(ctx.PluginDir); err != nil {
		return err
	}

	client := newControllerClient(ctx.ControllerURL)

	pods, err := client.listPods("deployment", scope, name)
	if err != nil {
		return err
	}

	if len(pods) == 0 {
		return fmt.Errorf("no pod for %s/%s — apply the manifest first (vd apply -f hep3-api.voodu)", scope, name)
	}

	for _, p := range pods {
		if err := client.startPod(p.Name); err != nil {
			return err
		}
	}

	emitOK(map[string]any{"message": fmt.Sprintf("hep3 api %s/%s: started %d pod(s)", scope, name, len(pods))})

	return nil
}

// apiStop stops the pod(s).
func apiStop(args []string) error {
	scope, name, err := apiRef(args)
	if err != nil {
		return err
	}

	ctx, err := readInvocationContext()
	if err != nil {
		return err
	}

	client := newControllerClient(ctx.ControllerURL)

	pods, err := client.listPods("deployment", scope, name)
	if err != nil {
		return err
	}

	if len(pods) == 0 {
		return fmt.Errorf("no pod for %s/%s", scope, name)
	}

	for _, p := range pods {
		if err := client.stopPod(p.Name); err != nil {
			return err
		}
	}

	emitOK(map[string]any{"message": fmt.Sprintf("hep3 api %s/%s: stopped %d pod(s)", scope, name, len(pods))})

	return nil
}

// buildLocalImage builds the reader image from the plugin's runtime
// Dockerfile + the installed binary → the local tag expand references.
// No registry: the controller runs this local image without a pull.
func buildLocalImage(pluginDir string) error {
	if pluginDir == "" {
		return fmt.Errorf("plugin dir unknown — cannot locate Dockerfile.runtime")
	}

	dockerfile := filepath.Join(pluginDir, "Dockerfile.runtime")

	cmd := exec.Command("docker", "build", "-f", dockerfile, "-t", imageTag(), pluginDir) //nolint:gosec // args from install layout + plugin version
	// Build logs go to stderr; stdout is reserved for the JSON envelope.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build local image %s: %w", imageTag(), err)
	}

	return nil
}
