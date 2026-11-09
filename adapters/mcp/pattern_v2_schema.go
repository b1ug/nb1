package main

func patternV2RefSchema() map[string]any {
	return objectSchema(map[string]any{
		"record_contract": map[string]any{"const": patternV2RecordContract},
		"id":              map[string]any{"type": "string", "pattern": patternID.String(), "minLength": 1, "maxLength": 64},
		"revision":        patternV2DigestSchema(),
	}, "record_contract", "id", "revision")
}
func patternV2DigestSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": patternRevision.String(), "minLength": 71, "maxLength": 71}
}
func patternV2SerialSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[0-9a-fA-F]{8}$"}
}
func patternV2JobIDSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^(job_|framejob_)[0-9a-f]{32}$"}
}
func patternV2PreviewSchema() map[string]any {
	return objectSchema(map[string]any{"pattern_ref": patternV2RefSchema(), "serial": patternV2SerialSchema()}, "pattern_ref")
}
func patternV2TicketProperties() map[string]any {
	return map[string]any{
		"pattern_ref": patternV2RefSchema(), "serial": patternV2SerialSchema(),
		"request_id":                  map[string]any{"type": "string", "pattern": "^req_[1-9][0-9]{0,18}_[0-9a-f]{32}$"},
		"expected_daemon_instance_id": map[string]any{"type": "string", "pattern": "^daemon_[0-9a-f]{32}$"},
	}
}
func patternV2RunSchema() map[string]any {
	p := patternV2TicketProperties()
	p["policy"] = policySchema()
	return objectSchema(p, "pattern_ref", "serial", "policy", "request_id", "expected_daemon_instance_id")
}
func patternV2RequestSchema() map[string]any {
	p := patternV2TicketProperties()
	p["job_id"] = patternV2JobIDSchema()
	p["execution_digest"] = patternV2DigestSchema()
	return objectSchema(p, "pattern_ref", "serial", "request_id", "expected_daemon_instance_id")
}
func patternV2JobSchema(frame bool) map[string]any {
	p := map[string]any{
		"id": patternV2JobIDSchema(), "device": patternV2SerialSchema(), "status": map[string]any{"enum": []string{"queued", "running", "cancelling", "succeeded", "failed", "cancelled"}},
		"created_at": map[string]any{"type": "string", "format": "date-time"}, "started_at": map[string]any{"type": "string", "format": "date-time"}, "finished_at": map[string]any{"type": "string", "format": "date-time"}, "error": map[string]any{"type": "string", "minLength": 1},
	}
	required := []string{"id", "device", "status", "program", "created_at"}
	count := func(max int) map[string]any { return map[string]any{"type": "integer", "minimum": 0, "maximum": max} }
	if frame {
		p["id"] = map[string]any{"type": "string", "pattern": "^framejob_[0-9a-f]{32}$"}
		p["program"] = frameProgramSchema()
		for _, key := range []string{"completed_frames", "total_frames"} {
			p[key] = count(1024)
			required = append(required, key)
		}
		for _, key := range []string{"completed_target_writes", "planned_target_writes"} {
			p[key] = count(2050)
			required = append(required, key)
		}
	} else {
		p["id"] = map[string]any{"type": "string", "pattern": "^job_[0-9a-f]{32}$"}
		p["program"] = frozenPatternProgramSchema()
		for _, key := range []string{"completed_steps", "total_steps"} {
			p[key] = count(1024 * 10000)
			required = append(required, key)
		}
	}
	return objectSchema(p, required...)
}
func patternV2EnvelopeSchema() map[string]any {
	p := objectSchema(map[string]any{"pattern_ref": patternV2RefSchema(), "execution_digest": patternV2DigestSchema(), "job_kind": map[string]any{"enum": []string{"program", "frame"}}, "job": map[string]any{}}, "pattern_ref", "execution_digest", "job_kind", "job")
	p["oneOf"] = []any{map[string]any{"properties": map[string]any{"job_kind": map[string]any{"const": "program"}, "job": patternV2JobSchema(false)}}, map[string]any{"properties": map[string]any{"job_kind": map[string]any{"const": "frame"}, "job": patternV2JobSchema(true)}}}
	return p
}
func patternV2ProgramPreviewSchema() map[string]any {
	caps := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	validation := objectSchema(map[string]any{"program": map[string]any{"const": "valid"}, "device": map[string]any{"enum": []string{"not_requested", "unknown", "supported", "unsupported"}}, "reason": map[string]any{"type": "string"}, "missing_capabilities": caps}, "program", "device")
	device := objectSchema(map[string]any{"serial": patternV2SerialSchema(), "connection": map[string]any{"type": "string"}, "capabilities": caps}, "serial", "capabilities")
	return objectSchema(map[string]any{"schema_version": map[string]any{"const": 1}, "compile_contract": map[string]any{"const": compileContract}, "input_kind": map[string]any{"const": "program"}, "program": frozenPatternProgramSchema(), "nominal_duration_ms": map[string]any{"type": "integer", "minimum": 10, "maximum": 86400000}, "total_steps": map[string]any{"type": "integer", "minimum": 1, "maximum": 1024 * 10000}, "required_capabilities": caps, "validation": validation, "device": device}, "schema_version", "compile_contract", "input_kind", "program", "nominal_duration_ms", "total_steps", "required_capabilities", "validation")
}
func patternV2PreviewDataSchema() map[string]any {
	_, framePreview := orbitPreviewSchemas()
	execution := objectSchema(map[string]any{"kind": map[string]any{"enum": []string{"program", "frame_program"}}, "contract": map[string]any{"enum": []string{compileContract, frameCompileContract}}, "program": map[string]any{}}, "kind", "contract", "program")
	execution["oneOf"] = []any{map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "program"}, "contract": map[string]any{"const": compileContract}, "program": frozenPatternProgramSchema()}}, map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "frame_program"}, "contract": map[string]any{"const": frameCompileContract}, "program": frameProgramSchema()}}}
	return objectSchema(map[string]any{"schema_version": map[string]any{"const": 1}, "contract": map[string]any{"const": patternV2PreviewContract}, "pattern_ref": patternV2RefSchema(), "execution_digest": patternV2DigestSchema(), "execution": execution, "preview": map[string]any{"oneOf": []any{patternV2ProgramPreviewSchema(), framePreview}}, "pattern_execution": objectSchema(map[string]any{"available": map[string]any{"type": "boolean"}, "reason": map[string]any{"enum": []string{"pattern_service_configured", "pattern_service_unsupported", "pattern_library_unconfigured", "pattern_library_configuration_error"}}}, "available", "reason")}, "schema_version", "contract", "pattern_ref", "execution_digest", "execution", "preview", "pattern_execution")
}
func patternV2OutputSchema(toolName string) map[string]any {
	kind := "pattern_request"
	var data any = patternV2EnvelopeSchema()
	var accepted any = map[string]any{"const": false}
	switch toolName {
	case "preview_pattern":
		kind = "pattern_ref_preview"
		data = patternV2PreviewDataSchema()
	case "run_pattern":
		kind = "pattern_job"
		accepted = map[string]any{"type": "boolean"}
	case "cancel_pattern_request":
		kind = "pattern_cancel"
		accepted = map[string]any{"type": "boolean"}
		data = objectSchema(map[string]any{"pattern_job": patternV2EnvelopeSchema(), "cancel_attempted": map[string]any{"type": "boolean"}}, "cancel_attempted")
	}
	p := map[string]any{"kind": map[string]any{"enum": []string{kind, "error"}}, "accepted": accepted, "data": data, "error": objectSchema(map[string]any{"code": map[string]any{"type": "string", "minLength": 1}, "message": map[string]any{"type": "string", "minLength": 1}, "http_status": map[string]any{"type": "integer"}, "outcome_unknown": map[string]any{"type": "boolean"}}, "code", "message")}
	if toolName != "preview_pattern" {
		p["request_ticket"] = objectSchema(map[string]any{"request_id": patternV2TicketProperties()["request_id"], "expected_daemon_instance_id": patternV2TicketProperties()["expected_daemon_instance_id"]}, "request_id", "expected_daemon_instance_id")
		p["poll_after_ms"] = map[string]any{"const": 200}
	}
	out := objectSchema(p, "kind", "accepted")
	out["oneOf"] = []any{map[string]any{"properties": map[string]any{"kind": map[string]any{"const": kind}}, "required": []string{"data"}, "not": map[string]any{"required": []string{"error"}}}, map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "error"}, "accepted": map[string]any{"const": false}}, "required": []string{"error"}}}
	return out
}
