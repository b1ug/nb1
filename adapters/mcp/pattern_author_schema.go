package main

func patternAuthorInputSchema(name string) map[string]any {
	id := map[string]any{"type": "string", "pattern": patternID.String(), "minLength": 1, "maxLength": 64}
	if name == "list_patterns_v2" {
		return objectSchema(map[string]any{})
	}
	if name == "get_pattern_v2" {
		return objectSchema(map[string]any{"id": id, "expected_revision": patternV2DigestSchema()}, "id")
	}
	source := map[string]any{"oneOf": []any{
		objectSchema(map[string]any{"kind": map[string]any{"const": "light_action"}, "contract": map[string]any{"const": lightContract}, "action": lightActionSchema()}, "kind", "contract", "action"),
		objectSchema(map[string]any{"kind": map[string]any{"const": "light_action"}, "contract": map[string]any{"const": orbitLightContract}, "action": orbitActionSchema(false)}, "kind", "contract", "action"),
	}}
	properties := map[string]any{"id": id, "title": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "source": source}
	required := []string{"id", "title", "source"}
	if name == "save_pattern_v2" {
		properties["expected_revision"] = patternV2DigestSchema()
		required = append(required, "expected_revision")
	}
	return objectSchema(properties, required...)
}
func patternAuthorErrorSchema() map[string]any {
	return objectSchema(map[string]any{"code": map[string]any{"type": "string", "minLength": 1}, "message": map[string]any{"type": "string", "maxLength": 4120}, "http_status": map[string]any{"type": "integer", "minimum": 100, "maximum": 599}}, "code", "message")
}
func patternAuthorSupportSchema() map[string]any {
	return objectSchema(map[string]any{"verified": map[string]any{"type": "boolean"}, "reason": map[string]any{"enum": []string{"verified", "unsupported_source", "unsupported_recipe"}}}, "verified", "reason")
}
func patternAuthorAvailabilitySchema() map[string]any {
	return objectSchema(map[string]any{"available": map[string]any{"type": "boolean"}, "reason": map[string]any{"enum": []string{"pattern_service_configured", "pattern_service_unsupported", "pattern_library_unconfigured", "pattern_library_configuration_error"}}}, "available", "reason")
}
func patternAuthorRecordProperties() map[string]any {
	return map[string]any{
		"schema_version": map[string]any{"const": 2}, "contract": map[string]any{"const": patternAuthorLibraryContract}, "pattern_ref": patternV2RefSchema(),
		"record_json":  map[string]any{"type": "string", "minLength": 2, "maxLength": maxPatternAuthorRecord, "description": "Lossless P1 canonical complete Record JSON, <=1MiB UTF-8 bytes. Keep this string; generic float64/JavaScript-number maps may lose int64 source values. Adapter checks revision/execution hashes; source compilation remains daemon-owned."},
		"verification": patternAuthorSupportSchema(), "execution_digest": patternV2DigestSchema(),
	}
}
func patternAuthorRecordDataSchema() map[string]any {
	return objectSchema(patternAuthorRecordProperties(), "schema_version", "contract", "pattern_ref", "record_json", "verification", "execution_digest")
}
func patternAuthorFactsSchema() map[string]any {
	positive := func() map[string]any { return map[string]any{"type": "integer", "minimum": 1, "maximum": 86400000} }
	base := func() map[string]any {
		return map[string]any{"kind": map[string]any{"enum": []string{"program", "frame_program"}}, "contract": map[string]any{"enum": []string{compileContract, frameCompileContract}}, "nominal_duration_ms": positive(), "timing_basis": map[string]any{"const": "host-nominal-excludes-native-io"}, "required_capabilities": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}}}
	}
	program := base()
	program["kind"] = map[string]any{"const": "program"}
	program["contract"] = map[string]any{"const": compileContract}
	program["total_steps"] = positive()
	frame := base()
	frame["kind"] = map[string]any{"const": "frame_program"}
	frame["contract"] = map[string]any{"const": frameCompileContract}
	for _, key := range []string{"total_frames", "stored_target_writes", "planned_target_writes", "cycle_duration_ms"} {
		frame[key] = positive()
	}
	for _, key := range []string{"nominal_initial_ms", "nominal_tail_ms"} {
		frame[key] = map[string]any{"type": "integer", "minimum": 0, "maximum": 86400000}
	}
	return map[string]any{"oneOf": []any{objectSchema(program, "kind", "contract", "nominal_duration_ms", "timing_basis", "required_capabilities", "total_steps"), objectSchema(frame, "kind", "contract", "nominal_duration_ms", "timing_basis", "required_capabilities", "total_frames", "stored_target_writes", "planned_target_writes", "cycle_duration_ms", "nominal_initial_ms", "nominal_tail_ms")}}
}
func patternAuthorPreviewDataSchema() map[string]any {
	p := patternAuthorRecordProperties()
	p["preview"] = patternAuthorFactsSchema()
	p["pattern_execution"] = patternAuthorAvailabilitySchema()
	return objectSchema(p, "schema_version", "contract", "pattern_ref", "record_json", "verification", "execution_digest", "preview", "pattern_execution")
}
func patternAuthorListSchema() map[string]any {
	token := map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_./-]+$"}
	summary := objectSchema(map[string]any{"pattern_ref": patternV2RefSchema(), "title": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "source_contract": token, "execution_kind": map[string]any{"enum": []string{"program", "frame_program"}}, "execution_contract": map[string]any{"enum": []string{compileContract, frameCompileContract}}, "verification": patternAuthorSupportSchema(), "execution_digest": patternV2DigestSchema()}, "pattern_ref", "title", "source_contract", "execution_kind", "execution_contract", "verification", "execution_digest")
	library := objectSchema(map[string]any{"path": map[string]any{"type": "string", "minLength": 1}, "state": map[string]any{"enum": []string{"ready", "read_only"}}, "create_only": map[string]any{"const": true}, "max_records": map[string]any{"const": 128}, "max_package_bytes": map[string]any{"const": 1 << 20}, "record_contract": map[string]any{"const": patternV2RecordContract}, "snapshot_kind": map[string]any{"const": patternAuthorSnapshotKind}}, "path", "state", "create_only", "max_records", "max_package_bytes", "record_contract", "snapshot_kind")
	return objectSchema(map[string]any{"schema_version": map[string]any{"const": 2}, "contract": map[string]any{"const": patternAuthorLibraryContract}, "library": library, "pattern_execution": patternAuthorAvailabilitySchema(), "patterns": map[string]any{"type": "array", "maxItems": 128, "items": summary}}, "schema_version", "contract", "library", "pattern_execution", "patterns")
}
func patternAuthorReadSchema(name string) map[string]any {
	kind := "pattern_record_v2"
	data := patternAuthorRecordDataSchema()
	switch name {
	case "list_patterns_v2":
		kind = "pattern_library_v2"
		data = patternAuthorListSchema()
	case "preview_pattern_draft_v2":
		kind = "pattern_draft_preview_v2"
		data = patternAuthorPreviewDataSchema()
	}
	success := objectSchema(map[string]any{"kind": map[string]any{"const": kind}, "accepted": map[string]any{"const": false}, "data": data}, "kind", "accepted", "data")
	failure := objectSchema(map[string]any{"kind": map[string]any{"const": "error"}, "accepted": map[string]any{"const": false}, "error": patternAuthorErrorSchema()}, "kind", "accepted", "error")
	return map[string]any{"oneOf": []any{success, failure}}
}
func patternAuthorSaveSchema() map[string]any {
	props := map[string]any{"kind": map[string]any{"const": "pattern_publication_v2"}, "accepted": map[string]any{"const": false}, "publication_attempted": map[string]any{"type": "boolean"}, "publication_confirmed": map[string]any{"type": "boolean"}, "publication_unknown": map[string]any{"type": "boolean"}, "pattern_ref": patternV2RefSchema(), "original_author_json": map[string]any{"type": "string", "minLength": 1, "maxLength": maxPatternAuthorArguments, "description": "Exact original UTF-8 author argument bytes; preserve with Ref for explicit GET recovery. Not a reserialized float64 map."}, "error": patternAuthorErrorSchema()}
	result := map[string]any{"schema_version": map[string]any{"const": 2}, "contract": map[string]any{"const": patternAuthorLibraryContract}, "record_json": patternAuthorRecordProperties()["record_json"], "verification": patternAuthorSupportSchema(), "execution_digest": patternV2DigestSchema(), "created": map[string]any{"type": "array", "maxItems": 1, "items": map[string]any{"type": "string"}}, "existing": map[string]any{"type": "array", "maxItems": 1, "items": map[string]any{"type": "string"}}, "total": map[string]any{"type": "integer", "minimum": 1, "maximum": 128}}
	props["result"] = objectSchema(result, "schema_version", "contract", "record_json", "verification", "execution_digest", "created", "existing", "total")
	schema := objectSchema(props, "kind", "accepted", "publication_attempted", "publication_confirmed", "publication_unknown")
	branch := func(attempted, confirmed, unknown bool) map[string]any {
		required := []string{"error"}
		forbidden := "result"
		if confirmed {
			required, forbidden = []string{"pattern_ref", "original_author_json", "result"}, "error"
		} else if attempted {
			required = append(required, "pattern_ref", "original_author_json")
		}
		return map[string]any{"properties": map[string]any{"publication_attempted": map[string]any{"const": attempted}, "publication_confirmed": map[string]any{"const": confirmed}, "publication_unknown": map[string]any{"const": unknown}}, "required": required, "not": map[string]any{"required": []string{forbidden}}}
	}
	schema["oneOf"] = []any{branch(false, false, false), branch(true, false, false), branch(true, false, true), branch(true, true, false)}
	return schema
}
