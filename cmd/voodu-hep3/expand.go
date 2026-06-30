package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// imageRepo is the LOCAL image tag the install hook builds (binary +
// runtime Dockerfile). Never a public registry — the `expand` output
// references this local tag, and the controller runs it without a pull
// because the install hook built it on the node.
const imageRepo = "voodu-hep3-api"

// defaultAPIAddr is the reader's internal listen address (voodu0 only; the
// PAT proxy reaches it). Operators override via env HEP3_API_ADDR.
const defaultAPIAddr = "0.0.0.0:8080"

// imageTag is the local image reference: voodu-hep3-api:<version>. The
// install hook builds exactly this tag from the same plugin version, so
// expand and install agree.
func imageTag() string {
	return imageRepo + ":" + version
}

// cmdExpand reads the expand request and emits the reader deployment plus
// an HEP3_ENDPOINT config_set for consumers.
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

	spec, err := decodeSpecMap(req.Spec)
	if err != nil {
		return err
	}

	payload, err := buildExpand(req.Scope, req.Name, spec)
	if err != nil {
		return err
	}

	emitOK(payload)

	return nil
}

// buildExpand turns the operator's `hep3` block into the reader deployment.
//
// The block IS a deployment spec: every standard field (volumes, env,
// ports, resources, networks, …) passes through unchanged. The plugin only
// overlays its own concerns and never clobbers what the operator set:
//
//   - image      → the local tag (operator may override)
//   - replicas   → 1 (operator may override)
//   - env        → HEP_STORE (from `store`), HEP_DATA_DIR, HEP3_API_ADDR
//     merged UNDER the operator's env (operator wins)
//   - env_from   → the resource's own bucket (operator may override)
//   - health_check → wget /health (operator may override)
//
// `store` is the only plugin-specific field (sugar for HEP_STORE); it is
// stripped from the deployment spec. On the ndjson path the operator wires
// the shared volume with a standard `volumes = ["hep3-data:/data:ro"]`.
func buildExpand(scope, name string, spec map[string]any) (expandedPayload, error) {
	// Passthrough: start from a copy of the operator's spec.
	dep := make(map[string]any, len(spec)+4)
	for k, v := range spec {
		dep[k] = v
	}

	// `store` is plugin sugar, not a deployment field.
	store := strings.ToLower(strings.TrimSpace(asString(dep, "store", "ndjson")))
	delete(dep, "store")

	if store != "ndjson" && store != "pg" {
		return expandedPayload{}, fmt.Errorf("invalid store %q (want ndjson or pg)", store)
	}

	// Defaults — operator passthrough wins where a field is already set.
	if _, ok := dep["image"]; !ok {
		dep["image"] = imageTag()
	}

	if _, ok := dep["replicas"]; !ok {
		dep["replicas"] = 1
	}

	// Env: plugin defaults UNDER the operator's env (operator wins).
	env := map[string]any{
		"HEP3_API_ADDR": defaultAPIAddr,
		"HEP_STORE":     store,
	}

	if store == "ndjson" {
		env["HEP_DATA_DIR"] = "/data"
	}

	for k, v := range asStringMap(dep, "env") {
		env[k] = v
	}

	dep["env"] = env

	if _, ok := dep["env_from"]; !ok {
		// On the pg path, DATABASE_URL comes from the resource's own bucket
		// via env_from (no secret in HCL). Harmless on the ndjson path.
		dep["env_from"] = []any{bucketRef(scope, name)}
	}

	if _, ok := dep["health_check"]; !ok {
		addr, _ := env["HEP3_API_ADDR"].(string)
		dep["health_check"] = fmt.Sprintf("wget -q -O- http://127.0.0.1:%s/health || exit 1", portOf(addr, "8080"))
	}

	depManifest := manifest{Kind: "deployment", Scope: scope, Name: name, Spec: dep}

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

	return expandedPayload{Manifests: []manifest{depManifest}, Actions: []dispatchAction{action}}, nil
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

// portOf returns the port after the last ':' in addr, or def.
func portOf(addr, def string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}

	return def
}

func asString(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
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
