package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Defaults for the reader API deployment.
const (
	defaultAPIPort = 8080
	// imageRepo is the LOCAL image tag the install hook builds (binary +
	// runtime Dockerfile). Never a public registry — the `expand` output
	// references this local tag, and the controller runs it without a
	// pull because the install hook built it on the node.
	imageRepo = "voodu-hep3-api"
)

// imageTag is the local image reference: voodu-hep3-api:<version>. The
// install hook builds exactly this tag from the same plugin version, so
// expand and install agree.
func imageTag() string {
	return imageRepo + ":" + version
}

// hep3Spec is the parsed HCL block for `hep3 "scope" "name" { ... }` —
// the READER (API). The collector is a plain `deployment` the operator
// applies separately (public clowk-hep3 image); only the reader needs a
// kind, because only it has a locally-built image + a PAT-proxy route.
type hep3Spec struct {
	Image     string
	APIPort   int
	Resources map[string]any
	Env       map[string]string
}

// cmdExpand reads the expand request and emits the reader API deployment
// (local image) plus an HEP3_ENDPOINT config_set for consumers.
func cmdExpand() error {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	req, err := decodeExpandRequest(raw)
	if err != nil {
		return err
	}

	if req.Name == "" {
		return fmt.Errorf("expand request missing required field 'name'")
	}

	spec, err := parseSpec(req.Spec)
	if err != nil {
		return err
	}

	emitOK(buildExpand(req.Scope, req.Name, spec))

	return nil
}

// buildExpand assembles the reader deployment + actions. Split out for
// unit-testability without stdin.
func buildExpand(scope, name string, spec hep3Spec) expandedPayload {
	env := map[string]any{
		"HEP3_API_ADDR": fmt.Sprintf("0.0.0.0:%d", spec.APIPort),
	}

	// Operator env passthrough wins.
	for k, v := range spec.Env {
		env[k] = v
	}

	depSpec := map[string]any{
		"image":    spec.Image,
		"replicas": 1,
		// No published ports: the API stays internal on voodu0. The
		// controller's PAT proxy (HP0) reaches it by container IP on the
		// port carried in HEP3_API_ADDR.
		"env": env,
		// DATABASE_URL (and any secrets) come from the resource's own
		// config bucket — `vd config <scope>/<name> set DATABASE_URL=...`
		// — inherited via env_from. No secret in the HCL.
		"env_from":     []any{bucketRef(scope, name)},
		"health_check": fmt.Sprintf("wget -q -O- http://127.0.0.1:%d/health || exit 1", spec.APIPort),
	}

	if len(spec.Resources) > 0 {
		depSpec["resources"] = spec.Resources
	}

	dep := manifest{Kind: "deployment", Scope: scope, Name: name, Spec: depSpec}

	// Publish the PAT-plane read path into the resource's bucket so the
	// webui/consumers inherit it via env_from. skip_restart: metadata for
	// others, not a reason to bounce the reader.
	action := dispatchAction{
		Type:        "config_set",
		Scope:       scope,
		Name:        name,
		KV:          map[string]string{"HEP3_ENDPOINT": endpointPath(scope, name)},
		SkipRestart: true,
	}

	return expandedPayload{Manifests: []manifest{dep}, Actions: []dispatchAction{action}}
}

func bucketRef(scope, name string) string {
	if scope == "" {
		return name
	}

	return scope + "/" + name
}

// endpointPath is the controller PAT-plane path the webui uses to reach
// this reader through the authenticated reverse proxy (HP0).
func endpointPath(scope, name string) string {
	return fmt.Sprintf("/api/pat/v1/hep3/%s/%s", scope, name)
}

// parseSpec coerces the HCL block (numbers arrive as float64) into a
// typed hep3Spec, filling defaults.
func parseSpec(rawSpec []byte) (hep3Spec, error) {
	m, err := decodeSpecMap(rawSpec)
	if err != nil {
		return hep3Spec{}, err
	}

	s := hep3Spec{
		Image:   asString(m, "image", imageTag()),
		APIPort: asInt(m, "api_port", defaultAPIPort),
		Env:     asStringMap(m, "env"),
	}

	if res, ok := m["resources"].(map[string]any); ok {
		s.Resources = res
	}

	return s, nil
}

func asString(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}

	return def
}

func asInt(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}

	return def
}

func asStringMap(m map[string]any, key string) map[string]string {
	raw, ok := m[key].(map[string]any)
	if !ok {
		return nil
	}

	out := make(map[string]string, len(raw))

	for k, v := range raw {
		out[k] = stringify(v)
	}

	return out
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}

		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", x)
	}
}
