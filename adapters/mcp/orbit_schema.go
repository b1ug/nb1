package main

func orbitActionSchema(normalized bool) map[string]any {
	color := resolveColorSchema()["properties"].(map[string]any)["color"]
	if normalized {
		color = map[string]any{"type": "string", "pattern": "^#[0-9a-f]{6}$"}
	}
	properties := map[string]any{
		"kind":      map[string]any{"type": "string", "const": "orbit"},
		"colors":    map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "items": color, "default": []string{"red", "blue"}},
		"leds":      map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "uniqueItems": true, "items": map[string]any{"type": "integer", "enum": []int{1, 2}}, "default": []int{1, 2}},
		"period_ms": map[string]any{"type": "integer", "minimum": 200, "maximum": 4800000, "default": 3000},
		"cycles":    map[string]any{"type": "integer", "minimum": 1, "maximum": 256, "default": 1},
		"segments":  map[string]any{"type": "integer", "enum": []int{4, 8}, "default": 4},
		"on_finish": map[string]any{"type": "string", "enum": []string{"off", "hold"}, "default": "off"},
	}
	required := []string{"kind"}
	if normalized {
		required = []string{"kind", "colors", "leds", "period_ms", "cycles", "segments", "on_finish"}
		properties["colors"].(map[string]any)["default"] = []string{"#ff0000", "#0000ff"}
	}
	schema := objectSchema(properties, required...)
	schema["allOf"] = []any{map[string]any{
		"if":   map[string]any{"required": []string{"segments"}, "properties": map[string]any{"segments": map[string]any{"const": 8}}},
		"then": map[string]any{"properties": map[string]any{"period_ms": map[string]any{"minimum": 400}, "cycles": map[string]any{"maximum": 128}}},
		"else": map[string]any{"properties": map[string]any{"period_ms": map[string]any{"maximum": 2400000}}},
	}}
	schema["description"] = "Experimental Orbit: 4 simple or 8 detailed paired linear fade segments. Default one cycle, 3000ms. The daemon owns the curve and the 24h total budget; this adapter does not compile effects or prove optical quality."
	return schema
}

func versionedLightSchema(properties map[string]any, required ...string) map[string]any {
	legacy := properties["action"]
	properties["contract"] = map[string]any{"type": "string", "enum": []string{lightContract, orbitLightContract}}
	// The contract branches below contain the action definitions once. Repeating
	// the whole union here needlessly multiplies SDK schema-resolution work.
	properties["action"] = map[string]any{"description": "Action shape is selected by the explicit contract in oneOf."}
	schema := objectSchema(properties, required...)
	schema["oneOf"] = []any{
		map[string]any{"properties": map[string]any{"contract": map[string]any{"const": lightContract}, "action": legacy}},
		map[string]any{"properties": map[string]any{"contract": map[string]any{"const": orbitLightContract}, "action": orbitActionSchema(false)}},
	}
	return schema
}

func frameProgramSchema() map[string]any {
	target := objectSchema(map[string]any{"led": map[string]any{"type": "integer", "enum": []int{1, 2}}, "color": map[string]any{"type": "string", "pattern": "^#[0-9a-f]{6}$"}}, "led", "color")
	targets := map[string]any{"type": "array", "minItems": 1, "maxItems": 2, "items": target}
	frame := objectSchema(map[string]any{"targets": targets, "fade_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": 600000, "multipleOf": 10}, "interval_ms": map[string]any{"type": "integer", "minimum": 50, "maximum": 600000}}, "targets", "fade_ms", "interval_ms")
	return objectSchema(map[string]any{"contract": map[string]any{"const": frameCompileContract}, "initial": targets, "frames": map[string]any{"type": "array", "minItems": 1, "maxItems": 1024, "items": frame}, "repeat": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}, "on_finish": map[string]any{"type": "string", "enum": []string{"off", "hold"}}}, "contract", "initial", "frames", "repeat", "on_finish")
}

func orbitPreviewSchemas() (map[string]any, map[string]any) {
	positive := map[string]any{"type": "integer", "minimum": 1}
	nonnegative := map[string]any{"type": "integer", "minimum": 0}
	frames := map[string]any{"type": "integer", "minimum": 1, "maximum": 1024}
	writes := map[string]any{"type": "integer", "minimum": 1, "maximum": 2050}
	caps := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	program := frameProgramSchema()
	result := objectSchema(map[string]any{
		"schema_version": map[string]any{"const": 1}, "contract": map[string]any{"const": orbitLightContract}, "program_compile_contract": map[string]any{"const": frameCompileContract},
		"recipe_id": map[string]any{"enum": []string{"orbit-paired-linear-4/experimental-v1", "orbit-paired-linear-8/experimental-v1"}},
		"action":    orbitActionSchema(true), "program": program, "nominal_duration_ms": positive, "total_frames": frames, "planned_target_writes": writes, "required_capabilities": caps,
	}, "schema_version", "contract", "program_compile_contract", "recipe_id", "action", "program", "nominal_duration_ms", "total_frames", "planned_target_writes", "required_capabilities")
	preview := objectSchema(map[string]any{
		"contract": map[string]any{"const": frameCompileContract}, "program": program, "total_frames": frames, "stored_target_writes": writes, "planned_target_writes": writes,
		"cycle_duration_ms": positive, "nominal_initial_ms": positive, "nominal_tail_ms": nonnegative, "nominal_duration_ms": positive,
		"timing_basis": map[string]any{"const": "host-nominal-excludes-native-io"}, "required_capabilities": caps,
		"execution": objectSchema(map[string]any{"available": map[string]any{"type": "boolean"}, "reason": map[string]any{"type": "string", "minLength": 1}}, "available", "reason"),
		"device":    objectSchema(map[string]any{"serial": tokenSchema(), "connection": map[string]any{"type": "string", "minLength": 1}}, "serial", "connection"),
	}, "contract", "program", "total_frames", "stored_target_writes", "planned_target_writes", "cycle_duration_ms", "nominal_initial_ms", "nominal_tail_ms", "nominal_duration_ms", "timing_basis", "required_capabilities", "execution")
	return result, preview
}

func extendLightPreviewOutput(out, legacyResult, legacyPreview map[string]any) map[string]any {
	orbitResult, orbitPreview := orbitPreviewSchemas()
	// Preserve the existing action-schema location for readers of the catalog.
	// The complete paired alternatives below still close/validate every field.
	actions := legacyResult["properties"].(map[string]any)["action"].(map[string]any)
	allActions := append([]any{}, actions["oneOf"].([]any)...)
	allActions = append(allActions, orbitActionSchema(true))
	// Keep one authoritative action union and let each complete result branch
	// constrain only its kind. Otherwise every fresh MCP connection recompiles
	// several copies of all eight legacy action schemas.
	resultBranch := func(original map[string]any, kind map[string]any) map[string]any {
		branch := make(map[string]any, len(original))
		for key, value := range original {
			branch[key] = value
		}
		props := make(map[string]any)
		for key, value := range original["properties"].(map[string]any) {
			props[key] = value
		}
		props["action"] = map[string]any{"properties": map[string]any{"kind": kind}, "required": []string{"kind"}}
		branch["properties"] = props
		return branch
	}
	legacyBranch := resultBranch(legacyResult, map[string]any{"enum": []string{"set", "off", "fade", "blink", "breathe", "pulse", "alternate", "rainbow"}})
	orbitBranch := resultBranch(orbitResult, map[string]any{"const": "orbit"})
	resultUnion := map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"oneOf": allActions}}, "oneOf": []any{legacyBranch, orbitBranch}}
	previewUnion := map[string]any{"oneOf": []any{legacyPreview, orbitPreview}}
	data := objectSchema(map[string]any{"result": resultUnion, "preview": previewUnion}, "result", "preview")
	data["oneOf"] = []any{
		map[string]any{"properties": map[string]any{"result": map[string]any{"properties": map[string]any{"contract": map[string]any{"const": lightContract}}}, "preview": map[string]any{"required": []string{"compile_contract"}, "properties": map[string]any{"compile_contract": map[string]any{"const": compileContract}}}}},
		map[string]any{"properties": map[string]any{"result": map[string]any{"properties": map[string]any{"contract": map[string]any{"const": orbitLightContract}}}, "preview": map[string]any{"required": []string{"contract"}, "properties": map[string]any{"contract": map[string]any{"const": frameCompileContract}}}}},
	}
	out["properties"].(map[string]any)["data"] = data
	return out
}
