// controller.go is the shared infra for dispatch commands (the hep3:api
// lifecycle): reading the invocation envelope the controller streams on
// stdin, and the tiny HTTP client that calls back into the controller's
// orchestration plane (pod listing + lifecycle). Mirrors voodu-postgres
// — replaced by the shared SDK when pkg/plugin/sdk lands.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// invocationContext is the JSON envelope the controller writes to stdin
// before invoking a dispatch command. The operator's args ride on
// os.Args; this carries where to call back.
type invocationContext struct {
	Plugin        string `json:"plugin"`
	Command       string `json:"command"`
	ControllerURL string `json:"controller_url,omitempty"`
	PluginDir     string `json:"plugin_dir,omitempty"`
	NodeName      string `json:"node_name,omitempty"`
}

// readInvocationContext decodes the stdin envelope, falling back to env
// vars so a direct CLI invocation (smoke testing) still works.
func readInvocationContext() (*invocationContext, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}

	ctx := &invocationContext{}

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, ctx); err != nil {
			return nil, fmt.Errorf("decode stdin: %w", err)
		}
	}

	if ctx.ControllerURL == "" {
		ctx.ControllerURL = os.Getenv("VOODU_CONTROLLER_URL")
	}

	if ctx.PluginDir == "" {
		ctx.PluginDir = os.Getenv("VOODU_PLUGIN_DIR")
	}

	return ctx, nil
}

// controllerClient calls the controller's orchestration plane.
type controllerClient struct {
	baseURL string
	http    *http.Client
}

func newControllerClient(baseURL string) *controllerClient {
	return &controllerClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// podInfo is the subset of the controller's /pods entry the lifecycle
// commands need.
type podInfo struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Scope        string `json:"scope"`
	ResourceName string `json:"resource_name"`
	Running      bool   `json:"running"`
}

// listPods returns the controller's pods filtered by (kind, scope, name).
func (c *controllerClient) listPods(kind, scope, name string) ([]podInfo, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("no controller_url available (run via dispatch or set VOODU_CONTROLLER_URL)")
	}

	u := fmt.Sprintf("%s/pods?kind=%s&scope=%s&name=%s",
		c.baseURL, url.QueryEscape(kind), url.QueryEscape(scope), url.QueryEscape(name))

	// #nosec G704 -- baseURL is the trusted controller admin URL injected by
	// the controller via the invocation context; the path is a fixed route.
	resp, err := c.http.Get(u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)

		return nil, fmt.Errorf("list pods: HTTP %d: %s", resp.StatusCode, body)
	}

	var env struct {
		Data struct {
			Pods []podInfo `json:"pods"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode pods response: %w", err)
	}

	return env.Data.Pods, nil
}

// restart triggers a rolling restart of (kind, scope, name) — recreating
// the pod, which picks up a freshly-built local image (new image id).
func (c *controllerClient) restart(kind, scope, name string) error {
	return c.post(fmt.Sprintf("/restart?kind=%s&scope=%s&name=%s",
		url.QueryEscape(kind), url.QueryEscape(scope), url.QueryEscape(name)))
}

// stopPod / startPod toggle a single pod's container by name.
func (c *controllerClient) stopPod(name string) error {
	return c.post(fmt.Sprintf("/pods/%s/stop", url.PathEscape(name)))
}

func (c *controllerClient) startPod(name string) error {
	return c.post(fmt.Sprintf("/pods/%s/start", url.PathEscape(name)))
}

func (c *controllerClient) post(path string) error {
	if c.baseURL == "" {
		return fmt.Errorf("no controller_url available")
	}

	// #nosec G704 -- baseURL is the trusted controller admin URL injected by
	// the controller via the invocation context; path is a fixed route.
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, nil)
	if err != nil {
		return err
	}

	// #nosec G704 -- request target is the trusted controller admin URL.
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("POST %s: HTTP %d: %s", path, resp.StatusCode, body)
	}

	return nil
}

// splitScopeName parses "scope/name" (or bare "name" → empty scope).
func splitScopeName(ref string) (scope, name string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ""
	}

	if i := strings.Index(ref, "/"); i >= 0 {
		return ref[:i], ref[i+1:]
	}

	return "", ref
}

// hasHelpFlag reports whether -h / --help appears in args.
func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}

	return false
}
