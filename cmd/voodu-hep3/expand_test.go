package main

import (
	"encoding/json"
	"testing"
)

// expandFromJSON runs decode→build from a raw HCL-block JSON body.
func expandFromJSON(t *testing.T, scope, name, specJSON string) expandedPayload {
	t.Helper()

	spec, err := decodeSpecMap([]byte(specJSON))
	if err != nil {
		t.Fatalf("decodeSpecMap: %v", err)
	}

	p, err := buildExpand(scope, name, spec)
	if err != nil {
		t.Fatalf("buildExpand: %v", err)
	}

	return p
}

// dep returns the single deployment manifest from an expand payload.
func dep(t *testing.T, p expandedPayload) manifest {
	t.Helper()

	if len(p.Manifests) != 1 {
		t.Fatalf("got %d manifests, want exactly 1", len(p.Manifests))
	}

	m := p.Manifests[0]

	if m.Kind != "deployment" {
		t.Fatalf("manifest kind = %q, want deployment (reader is a plain deployment)", m.Kind)
	}

	return m
}

func envMap(t *testing.T, m manifest) map[string]any {
	t.Helper()

	env, ok := m.Spec["env"].(map[string]any)
	if !ok {
		t.Fatalf("spec.env is not a map: %v", m.Spec["env"])
	}

	return env
}

func TestExpand_Defaults(t *testing.T) {
	m := dep(t, expandFromJSON(t, "voip", "api", `{}`))

	if m.Scope != "voip" || m.Name != "api" {
		t.Errorf("scope/name = %q/%q, want voip/api", m.Scope, m.Name)
	}

	// Default image is the LOCAL tag (built by install), not a public one.
	if m.Spec["image"] != imageTag() {
		t.Errorf("image = %v, want local tag %s", m.Spec["image"], imageTag())
	}

	if m.Spec["replicas"] != 1 {
		t.Errorf("replicas = %v, want 1", m.Spec["replicas"])
	}

	// No ports / no volumes are added by the plugin — those are the
	// operator's to set (passthrough).
	if _, has := m.Spec["ports"]; has {
		t.Errorf("plugin must not add ports: %v", m.Spec["ports"])
	}

	if _, has := m.Spec["volumes"]; has {
		t.Errorf("plugin must not add volumes (operator wires them): %v", m.Spec["volumes"])
	}

	env := envMap(t, m)
	if env["HEP3_API_ADDR"] != defaultAPIAddr {
		t.Errorf("HEP3_API_ADDR = %v, want %s", env["HEP3_API_ADDR"], defaultAPIAddr)
	}

	if env["HEP_STORE"] != "ndjson" {
		t.Errorf("HEP_STORE = %v, want ndjson (default)", env["HEP_STORE"])
	}

	if env["HEP_DATA_DIR"] != "/data" {
		t.Errorf("HEP_DATA_DIR = %v, want /data", env["HEP_DATA_DIR"])
	}

	ef, ok := m.Spec["env_from"].([]any)
	if !ok || len(ef) != 1 || ef[0] != "voip/api" {
		t.Errorf("env_from = %v, want [voip/api]", m.Spec["env_from"])
	}
}

// Standard deployment fields pass through untouched; the plugin overlays
// its env without clobbering the operator's.
func TestExpand_Passthrough(t *testing.T) {
	spec := `{
		"store": "ndjson",
		"volumes": ["hep3-data:/data:ro"],
		"ports": ["0.0.0.0:9999:8080"],
		"resources": {"limits": {"cpu": "0.5", "memory": "128Mi"}},
		"env": {"FOO": "bar"}
	}`

	m := dep(t, expandFromJSON(t, "voip", "api", spec))

	vols, ok := m.Spec["volumes"].([]any)
	if !ok || len(vols) != 1 || vols[0] != "hep3-data:/data:ro" {
		t.Errorf("volumes passthrough lost: %v", m.Spec["volumes"])
	}

	ports, ok := m.Spec["ports"].([]any)
	if !ok || len(ports) != 1 || ports[0] != "0.0.0.0:9999:8080" {
		t.Errorf("ports passthrough lost: %v", m.Spec["ports"])
	}

	res, ok := m.Spec["resources"].(map[string]any)
	if !ok {
		t.Fatalf("resources passthrough lost: %v", m.Spec["resources"])
	}

	if limits, _ := res["limits"].(map[string]any); limits["memory"] != "128Mi" {
		t.Errorf("resources.limits.memory = %v, want 128Mi", res["limits"])
	}

	env := envMap(t, m)
	if env["FOO"] != "bar" {
		t.Errorf("operator env FOO lost: %v", env["FOO"])
	}

	if env["HEP_STORE"] != "ndjson" {
		t.Errorf("HEP_STORE = %v, want ndjson", env["HEP_STORE"])
	}

	// `store` is sugar, not a deployment field — it must be stripped.
	if _, has := m.Spec["store"]; has {
		t.Errorf("store must not leak into the deployment spec")
	}
}

// store=pg: HEP_STORE=pg, no HEP_DATA_DIR; DATABASE_URL via env_from.
func TestExpand_PGStore(t *testing.T) {
	m := dep(t, expandFromJSON(t, "voip", "api", `{"store":"pg"}`))

	env := envMap(t, m)
	if env["HEP_STORE"] != "pg" {
		t.Errorf("HEP_STORE = %v, want pg", env["HEP_STORE"])
	}

	if _, has := env["HEP_DATA_DIR"]; has {
		t.Errorf("pg store must not set HEP_DATA_DIR: %v", env["HEP_DATA_DIR"])
	}
}

// Image override passes through.
func TestExpand_ImageOverride(t *testing.T) {
	m := dep(t, expandFromJSON(t, "voip", "api", `{"image":"voodu-hep3-api:v9"}`))

	if m.Spec["image"] != "voodu-hep3-api:v9" {
		t.Errorf("image override lost: %v", m.Spec["image"])
	}
}

// Operator env wins over the plugin defaults.
func TestExpand_EnvPassthroughWins(t *testing.T) {
	m := dep(t, expandFromJSON(t, "voip", "api", `{"env":{"HEP3_API_ADDR":"0.0.0.0:7000"}}`))

	if env := envMap(t, m); env["HEP3_API_ADDR"] != "0.0.0.0:7000" {
		t.Errorf("operator env must win: HEP3_API_ADDR = %v, want 0.0.0.0:7000", env["HEP3_API_ADDR"])
	}
}

// An unknown store is rejected.
func TestExpand_InvalidStore(t *testing.T) {
	spec, err := decodeSpecMap([]byte(`{"store":"redis"}`))
	if err != nil {
		t.Fatalf("decodeSpecMap: %v", err)
	}

	if _, err := buildExpand("voip", "api", spec); err == nil {
		t.Fatal("store=redis should error")
	}
}

// expand must emit HEP3_ENDPOINT (the PAT-plane path) for consumers.
func TestExpand_EmitsEndpointAction(t *testing.T) {
	p := expandFromJSON(t, "voip", "api", `{}`)

	if len(p.Actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(p.Actions))
	}

	a := p.Actions[0]

	if a.Type != "config_set" || a.Scope != "voip" || a.Name != "api" || !a.SkipRestart {
		t.Errorf("action = %+v, want config_set voip/api skip_restart", a)
	}

	if a.KV["HEP3_ENDPOINT"] != "/api/pat/v1/hep3/voip/api" {
		t.Errorf("HEP3_ENDPOINT = %q, want /api/pat/v1/hep3/voip/api", a.KV["HEP3_ENDPOINT"])
	}
}

func TestDecodeExpandRequest(t *testing.T) {
	raw := `{"kind":"hep3","scope":"voip","name":"api","spec":{"store":"ndjson"},"config":{}}`

	req, err := decodeExpandRequest([]byte(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if req.Kind != "hep3" || req.Scope != "voip" || req.Name != "api" {
		t.Errorf("req = %+v", req)
	}

	var spec map[string]any
	if err := json.Unmarshal(req.Spec, &spec); err != nil {
		t.Fatalf("spec not raw-preserved: %v", err)
	}

	if spec["store"] != "ndjson" {
		t.Errorf("spec.store = %v, want ndjson", spec["store"])
	}
}
