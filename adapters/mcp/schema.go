package main

import (
	"github.com/b1ug/nb1/internal/sequence"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var specifications = makeToolSpecifications()

func toolSpecifications() []*mcp.Tool { return specifications }

func makeToolSpecifications() []*mcp.Tool {
	return []*mcp.Tool{
		newTool("list_devices", "List the devices configured in the local daemon and their cached state.", object(nil), true, true),
		newTool("get_device_state", "Read a device's cached RGB observation and sampled_at timestamp.", object(map[string]any{"serial": serialSchema()}, "serial"), true, true),
		newTool("resolve_color", "Resolve a hex color, name, RGB8/HSV object or @local-name through the daemon. Returns an exact lowercase Hex6 color.", object(map[string]any{"color": colorSchema()}, "color"), true, true),
		newTool("preview_light", "Preview exactly one Action, Sequence or saved pattern_id. Returns the normalized Sequence, duration, step count and device capability check. Optional serial selects a cached device profile.", authorSchema(true, false), true, true),
		newTool("run_light", "Submit exactly one Action, Sequence or saved pattern_id once. The adapter generates a short Job ID when id is omitted or empty. Returns a Job, which can still be running. Preserve the returned ID and endpoint and use get_job after an unknown outcome.", authorSchema(true, true), false, false),
		newTool("get_job", "Read the original Job by ID. Missing records may have expired or belong to an earlier daemon lifetime.", object(map[string]any{"id": idSchema()}, "id"), true, true),
		newTool("cancel_job", "Request cancellation of a Job by ID. A cancelling result requires get_job polling until cleanup reaches a terminal state.", object(map[string]any{"id": idSchema()}, "id"), false, true),
		newTool("list_colors", "List locally saved color names and exact lowercase Hex6 values.", object(nil), true, true),
		newTool("save_color", "Resolve and save a local named color. Names start with a lowercase letter and contain up to 32 lowercase letters, digits or hyphens. Existing names are replaced.", object(map[string]any{"name": map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]{0,31}$"}, "color": colorSchema()}, "name", "color"), false, false),
		newTool("list_patterns", "List locally saved Pattern ID and title summaries.", object(nil), true, true),
		newTool("get_pattern", "Read a local Pattern's ID, optional title and frozen Sequence.", object(map[string]any{"id": idSchema()}, "id"), true, true),
		newTool("save_pattern", "Compile and save one Action or Sequence as a local Pattern. Optional title accepts up to 256 Unicode characters. The saved Pattern fixes resolved RGB values. Existing IDs are replaced.", authorSchema(false, false), false, false),
	}
}

func newTool(name, description string, schema map[string]any, readOnly, idempotent bool) *mcp.Tool {
	closed, destructive := false, !readOnly
	return &mcp.Tool{Name: name, Description: description, InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: idempotent, OpenWorldHint: &closed, DestructiveHint: &destructive}}
}

func argumentFields(name string) (allowed, required []string) {
	for _, tool := range specifications {
		if tool.Name != name {
			continue
		}
		schema := tool.InputSchema.(map[string]any)
		for property := range schema["properties"].(map[string]any) {
			allowed = append(allowed, property)
		}
		required, _ = schema["required"].([]string)
		return allowed, required
	}
	return nil, nil
}

func object(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func serialSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$"}
}

func idSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,64}$"}
}

func integer(min, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max}
}

func colorSchema() map[string]any {
	return map[string]any{"oneOf": []any{
		map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "description": "Hex3, Hex6, CSS color name or @local-name."},
		object(map[string]any{"space": map[string]any{"const": "rgb8"}, "r": integer(0, 255), "g": integer(0, 255), "b": integer(0, 255)}, "space", "r", "g", "b"),
		object(map[string]any{"space": map[string]any{"const": "hsv"}, "h_deg": map[string]any{"type": "number", "minimum": 0, "maximum": 360}, "s_pct": map[string]any{"type": "number", "minimum": 0, "maximum": 100}, "v_pct": map[string]any{"type": "number", "minimum": 0, "maximum": 100}}, "space", "h_deg", "s_pct", "v_pct"),
	}, "description": "HSV decimals allow at most six fractional digits. The daemon resolves exact RGB without implicit gamma."}
}

func authorSchema(pattern, run bool) map[string]any {
	properties := map[string]any{"action": actionSchema(), "sequence": sequenceSchema()}
	choices := []any{map[string]any{"required": []string{"action"}}, map[string]any{"required": []string{"sequence"}}}
	var required []string
	if pattern {
		properties["pattern_id"] = idSchema()
		properties["serial"] = serialSchema()
		choices = append(choices, map[string]any{"required": []string{"pattern_id"}})
		if run {
			properties["id"] = map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{0,64}$", "description": "Omit or use an empty string to generate j_ plus ten Crockford Base32 characters."}
			required = []string{"serial"}
		}
	} else {
		properties["id"] = idSchema()
		properties["title"] = map[string]any{"type": "string", "maxLength": 256}
		required = []string{"id"}
	}
	result := object(properties, required...)
	result["oneOf"] = choices
	return result
}

func actionSchema() map[string]any {
	var actions []any
	for _, kind := range []string{"set", "off", "fade", "blink", "breathe", "pulse", "alternate", "orbit", "rainbow"} {
		properties := map[string]any{"kind": map[string]any{"const": kind}}
		required := []string{"kind"}
		if kind != "alternate" && kind != "orbit" {
			properties["led"] = integer(0, 2)
		}
		if kind != "off" && kind != "alternate" && kind != "orbit" && kind != "rainbow" {
			properties["color"] = colorSchema()
			required = append(required, "color")
		}
		switch kind {
		case "fade":
			properties["duration_ms"] = integer(sequence.FadeQuantumMS, sequence.MaxFadeMS)
			required = append(required, "duration_ms")
		case "blink":
			properties["on_ms"] = integer(sequence.UpdateAllowanceMS, sequence.MaxHoldMS+sequence.UpdateAllowanceMS)
			properties["off_ms"] = integer(sequence.UpdateAllowanceMS, sequence.MaxHoldMS+sequence.UpdateAllowanceMS)
		case "pulse":
			properties["attack_ms"] = integer(sequence.FadeQuantumMS, sequence.MaxFadeMS)
			properties["decay_ms"] = integer(sequence.FadeQuantumMS, sequence.MaxFadeMS)
			properties["rest_ms"] = integer(0, sequence.MaxHoldMS)
		case "alternate", "orbit":
			properties["leds"] = map[string]any{"enum": []any{[]int{1, 2}, []int{2, 1}}}
			properties["colors"] = map[string]any{"type": "array", "items": colorSchema(), "minItems": 2, "maxItems": 2}
			required = append(required, "colors")
		}
		if kind == "breathe" || kind == "alternate" || kind == "orbit" || kind == "rainbow" {
			properties["period_ms"] = map[string]any{"type": "integer", "minimum": 1, "description": "The compiler validates the full cycle against step, timing and duration limits."}
		}
		if kind != "set" && kind != "off" && kind != "fade" {
			properties["cycles"] = integer(1, sequence.MaxExecutedSteps)
			properties["on_finish"] = map[string]any{"enum": []string{"off", "hold"}}
		}
		actions = append(actions, object(properties, required...))
	}
	return map[string]any{"oneOf": actions}
}

func sequenceSchema() map[string]any {
	var variants []any
	for _, timing := range []string{sequence.TimingSerial, sequence.TimingInterval} {
		minLED, maxTargets := 0, 1
		if timing == sequence.TimingInterval {
			minLED, maxTargets = 1, 2
		}
		target := object(map[string]any{"led": integer(minLED, 2), "color": colorSchema()}, "led", "color")
		targets := map[string]any{"type": "array", "items": target, "minItems": 1, "maxItems": maxTargets}
		fade := integer(0, sequence.MaxFadeMS)
		fade["multipleOf"] = sequence.FadeQuantumMS
		step := map[string]any{"targets": targets, "fade_ms": fade}
		requiredStep := []string{"targets"}
		properties := map[string]any{"timing": map[string]any{"const": timing}, "repeat": integer(1, sequence.MaxExecutedSteps), "on_finish": map[string]any{"enum": []string{"off", "hold"}}}
		required := []string{"timing", "steps"}
		if timing == sequence.TimingSerial {
			step["hold_ms"] = integer(0, sequence.MaxHoldMS)
		} else {
			step["interval_ms"] = integer(sequence.MinIntervalMS, sequence.MaxIntervalMS)
			requiredStep = append(requiredStep, "interval_ms")
			properties["initial"] = targets
			required = append(required, "initial")
		}
		properties["steps"] = map[string]any{"type": "array", "items": object(step, requiredStep...), "minItems": 1, "maxItems": sequence.MaxExecutedSteps}
		variants = append(variants, object(properties, required...))
	}
	return map[string]any{"oneOf": variants, "description": "At most 1024 executed Steps including repeat and 24 hours nominal duration. Interval Steps retain initial LED order and use fade_ms <= interval_ms."}
}
