package main

import (
	"encoding/json"
	"testing"
)

// expandFromJSON runs parse→build from a raw HCL-block JSON body.
func expandFromJSON(t *testing.T, scope, name, specJSON string) expandedPayload {
	t.Helper()

	spec, err := parseSpec([]byte(specJSON))
	if err != nil {
		t.Fatalf("parseSpec: %v", err)
	}

	return buildExpand(scope, name, spec)
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

	// The reader is internal-only: NO published ports.
	if _, hasPorts := m.Spec["ports"]; hasPorts {
		t.Errorf("reader must not publish ports (internal only): %v", m.Spec["ports"])
	}

	env := envMap(t, m)
	if env["HEP3_API_ADDR"] != "0.0.0.0:8080" {
		t.Errorf("HEP3_API_ADDR = %v, want 0.0.0.0:8080", env["HEP3_API_ADDR"])
	}

	// Default store is ndjson: HEP_STORE + HEP_DATA_DIR + a read-only mount
	// of the collector's shared volume.
	if env["HEP_STORE"] != "ndjson" {
		t.Errorf("HEP_STORE = %v, want ndjson", env["HEP_STORE"])
	}

	if env["HEP_DATA_DIR"] != "/data" {
		t.Errorf("HEP_DATA_DIR = %v, want /data", env["HEP_DATA_DIR"])
	}

	vols, ok := m.Spec["volumes"].([]any)
	if !ok || len(vols) != 1 || vols[0] != "hep3-data:/data:ro" {
		t.Errorf("volumes = %v, want [hep3-data:/data:ro]", m.Spec["volumes"])
	}

	ef, ok := m.Spec["env_from"].([]any)
	if !ok || len(ef) != 1 || ef[0] != "voip/api" {
		t.Errorf("env_from = %v, want [voip/api]", m.Spec["env_from"])
	}
}

// store=pg: no shared volume, HEP_STORE=pg, DATABASE_URL via env_from.
func TestExpand_PGStore(t *testing.T) {
	m := dep(t, expandFromJSON(t, "voip", "api", `{"store":"pg"}`))

	env := envMap(t, m)
	if env["HEP_STORE"] != "pg" {
		t.Errorf("HEP_STORE = %v, want pg", env["HEP_STORE"])
	}

	if _, has := env["HEP_DATA_DIR"]; has {
		t.Errorf("pg store must not set HEP_DATA_DIR: %v", env["HEP_DATA_DIR"])
	}

	if _, has := m.Spec["volumes"]; has {
		t.Errorf("pg store must not mount a volume: %v", m.Spec["volumes"])
	}
}

// data_volume override flows into the mount.
func TestExpand_DataVolumeOverride(t *testing.T) {
	m := dep(t, expandFromJSON(t, "voip", "api", `{"data_volume":"capture-vol"}`))

	vols, ok := m.Spec["volumes"].([]any)
	if !ok || len(vols) != 1 || vols[0] != "capture-vol:/data:ro" {
		t.Errorf("volumes = %v, want [capture-vol:/data:ro]", m.Spec["volumes"])
	}
}

// An unknown store is rejected at parse time.
func TestExpand_InvalidStore(t *testing.T) {
	if _, err := parseSpec([]byte(`{"store":"redis"}`)); err == nil {
		t.Fatal("store=redis should error")
	}
}

func TestExpand_Overrides(t *testing.T) {
	spec := `{"image":"voodu-hep3-api:v9","api_port":9000,"resources":{"limits":{"cpu":"0.5","memory":"128Mi"}}}`

	m := dep(t, expandFromJSON(t, "voip", "api", spec))

	if m.Spec["image"] != "voodu-hep3-api:v9" {
		t.Errorf("image override lost: %v", m.Spec["image"])
	}

	if envMap(t, m)["HEP3_API_ADDR"] != "0.0.0.0:9000" {
		t.Errorf("api_port override = %v, want 0.0.0.0:9000", envMap(t, m)["HEP3_API_ADDR"])
	}

	res, ok := m.Spec["resources"].(map[string]any)
	if !ok {
		t.Fatalf("resources not passed through: %v", m.Spec["resources"])
	}

	limits, _ := res["limits"].(map[string]any)
	if limits["memory"] != "128Mi" {
		t.Errorf("resources.limits.memory = %v, want 128Mi", limits["memory"])
	}
}

func TestExpand_EnvPassthroughWins(t *testing.T) {
	m := dep(t, expandFromJSON(t, "voip", "api", `{"env":{"HEP3_API_ADDR":"0.0.0.0:7000","FOO":"bar"}}`))

	env := envMap(t, m)

	if env["FOO"] != "bar" {
		t.Errorf("passthrough FOO = %v, want bar", env["FOO"])
	}

	if env["HEP3_API_ADDR"] != "0.0.0.0:7000" {
		t.Errorf("operator env must win: HEP3_API_ADDR = %v, want 0.0.0.0:7000", env["HEP3_API_ADDR"])
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
	raw := `{"kind":"hep3","scope":"voip","name":"api","spec":{"api_port":8080},"config":{}}`

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

	if spec["api_port"].(float64) != 8080 {
		t.Errorf("spec.api_port = %v, want 8080", spec["api_port"])
	}
}
