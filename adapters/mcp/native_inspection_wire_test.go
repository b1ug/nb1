package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// These tests exercise an independent, HTTP-only wire validator. Fixtures are
// synthetic observations, not hardware evidence. No controller or SDK is used.
func nativeWireTestFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/native-inspection/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func nativeWireTestTree(t *testing.T, data []byte) map[string]any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value map[string]any
	if err := d.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func nativeWireTestMutate(t *testing.T, data []byte, change func(map[string]any)) []byte {
	t.Helper()
	value := nativeWireTestTree(t, data)
	change(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func nativeWireTestObject(value map[string]any, key string) map[string]any {
	return value[key].(map[string]any)
}

func nativeWireTestRow(value map[string]any, index int) map[string]any {
	return nativeWireTestObject(value, "ram")["rows"].([]any)[index].(map[string]any)
}

func nativeWireTestByte(t *testing.T, report map[string]any, index int, value byte) {
	t.Helper()
	data, err := hex.DecodeString(report["raw_hex"].(string))
	if err != nil || index < 0 || index >= len(data) {
		t.Fatalf("fixture byte %d: %v", index, err)
	}
	data[index] = value
	report["raw_hex"] = hex.EncodeToString(data)
}

func nativeWireTestCompletePlayback(value map[string]any) {
	value["complete"], value["status"], value["error"] = true, "complete", nil
	playback := nativeWireTestObject(value, "playback")
	playback["error"], playback["issues"] = nil, []any{}
	playback["configured_range"] = map[string]any{"start_line": 0, "line_count": 2}
	playback["decoding_status"] = "reference-fields-interpreted"
}

func nativeWireTestUntrustedPlayback(value map[string]any, n int, attempted bool) {
	value["status"] = "failed"
	playback := nativeWireTestObject(value, "playback")
	playback["raw_fields"] = map[string]any{"mode": nil, "start": nil, "end": nil, "count": nil, "next_position": nil}
	report := nativeWireTestObject(playback, "report")
	report["read_attempted"], report["returned_length"] = attempted, n
	report["length_valid"], report["prefix_trusted"] = false, false
	report["raw_hex"], report["unknown_tail_hex"] = "", ""
}

func TestNativeInspectionWireAcceptsExplicitNullableAndDiagnosticShapes(t *testing.T) {
	ram := nativeWireTestFixture(t, "ram-complete")
	playback := nativeWireTestFixture(t, "playback-partial")
	tests := []struct {
		name, kind string
		data       []byte
		complete   bool
		change     func(map[string]any)
	}{
		{name: "complete-ram-null-other-kind-and-error", kind: "ram", data: ram, complete: true},
		{name: "partial-playback-max-uint64-native-code", kind: "playback", data: playback},
		{name: "absent-native-carrier-remains-explicit-null", kind: "playback", data: playback, change: func(v map[string]any) {
			for _, problem := range []map[string]any{nativeWireTestObject(v, "error"), nativeWireTestObject(nativeWireTestObject(v, "playback"), "error")} {
				problem["native_platform"], problem["native_code"] = nil, nil
			}
		}},
		{name: "SDK-reference-may-be-unknown-with-independent-read-reference", kind: "ram", data: ram, complete: true, change: func(v map[string]any) {
			nativeWireTestObject(v, "profile")["sdk_reference_firmware"] = nil
		}},
		{name: "full-playback", kind: "playback", data: playback, complete: true, change: nativeWireTestCompletePlayback},
		{name: "unknown-mode-remains-complete-raw", kind: "playback", data: playback, complete: true, change: func(v map[string]any) {
			nativeWireTestCompletePlayback(v)
			p := nativeWireTestObject(v, "playback")
			nativeWireTestObject(p, "raw_fields")["mode"] = 255
			nativeWireTestByte(t, nativeWireTestObject(p, "report"), 2, 255)
			p["configured_range"], p["decoding_status"] = nil, "unrepresentable"
			p["issues"] = []any{map[string]any{"code": "unknown_playback_mode", "path": "raw_fields.mode", "message": "Synthetic unknown mode"}}
		}},
		{name: "next-cursor-outside-configured-range-is-still-raw", kind: "playback", data: playback, complete: true, change: func(v map[string]any) {
			nativeWireTestCompletePlayback(v)
			p := nativeWireTestObject(v, "playback")
			nativeWireTestObject(p, "raw_fields")["next_position"] = 255
			nativeWireTestByte(t, nativeWireTestObject(p, "report"), 6, 255)
		}},
		{name: "exclusive-end32-is-valid-readback", kind: "playback", data: playback, complete: true, change: func(v map[string]any) {
			nativeWireTestCompletePlayback(v)
			p := nativeWireTestObject(v, "playback")
			nativeWireTestObject(p, "raw_fields")["end"] = 32
			nativeWireTestByte(t, nativeWireTestObject(p, "report"), 4, 32)
			nativeWireTestObject(p, "configured_range")["line_count"] = 32
		}},
		{name: "untrusted-negative-n-is-diagnostic", kind: "playback", data: playback, change: func(v map[string]any) {
			nativeWireTestUntrustedPlayback(v, -1, true)
		}},
		{name: "untrusted-over-buffer-n-is-diagnostic", kind: "playback", data: playback, change: func(v map[string]any) {
			nativeWireTestUntrustedPlayback(v, 10, true)
		}},
		{name: "write-failure-with-no-read-attempt", kind: "playback", data: playback, change: func(v map[string]any) {
			nativeWireTestUntrustedPlayback(v, 0, false)
		}},
		{name: "trusted-zero-byte-read-does-not-invent-ID-data", kind: "playback", data: playback, change: func(v map[string]any) {
			v["status"] = "failed"
			p := nativeWireTestObject(v, "playback")
			p["raw_fields"] = map[string]any{"mode": nil, "start": nil, "end": nil, "count": nil, "next_position": nil}
			r := nativeWireTestObject(p, "report")
			r["returned_length"], r["raw_hex"], r["unknown_tail_hex"] = 0, "", ""
		}},
		{name: "short-prefix-unavailable-fields-are-null", kind: "playback", data: playback, change: func(v map[string]any) {
			p := nativeWireTestObject(v, "playback")
			p["raw_fields"] = map[string]any{"mode": nil, "start": nil, "end": nil, "count": nil, "next_position": nil}
			r := nativeWireTestObject(p, "report")
			r["returned_length"], r["raw_hex"], r["unknown_tail_hex"] = 2, "0153", ""
		}},
		{name: "unknown-layout-all-bytes-are-unknown-tail", kind: "playback", data: playback, change: func(v map[string]any) {
			p := nativeWireTestObject(v, "playback")
			p["raw_fields"] = map[string]any{"mode": nil, "start": nil, "end": nil, "count": nil, "next_position": nil}
			r := nativeWireTestObject(p, "report")
			r["layout"], r["requested_report_id"], r["buffer_length"] = "unknown", 2, 61
			r["unknown_tail_hex"] = r["raw_hex"]
		}},
		{name: "raw-header-is-not-authenticated-command-echo", kind: "ram", data: ram, complete: true, change: func(v map[string]any) {
			r := nativeWireTestObject(nativeWireTestRow(v, 0), "report")
			nativeWireTestByte(t, r, 0, 0x66)
			nativeWireTestByte(t, r, 1, 0x77)
		}},
		{name: "complete-raw-zero-duration-is-not-author-sequence", kind: "ram", data: ram, complete: true, change: func(v map[string]any) {
			row := nativeWireTestRow(v, 0)
			row["duration_ticks"] = 0
			r := nativeWireTestObject(row, "report")
			nativeWireTestByte(t, r, 5, 0)
			nativeWireTestByte(t, r, 6, 0)
			ram := nativeWireTestObject(v, "ram")
			ram["sequence"], ram["sequence_digest"], ram["decoding_status"] = nil, nil, "unrepresentable"
			ram["issues"] = []any{map[string]any{"code": "zero_duration_unqualified", "path": "rows[0]", "message": "Synthetic zero-time row retained"}}
		}},
		{name: "complete-raw-unknown-led-is-not-author-sequence", kind: "ram", data: ram, complete: true, change: func(v map[string]any) {
			row := nativeWireTestRow(v, 0)
			row["led_code"] = 255
			nativeWireTestByte(t, nativeWireTestObject(row, "report"), 7, 255)
			ram := nativeWireTestObject(v, "ram")
			ram["sequence"], ram["sequence_digest"], ram["decoding_status"] = nil, nil, "unrepresentable"
			ram["issues"] = []any{map[string]any{"code": "unsupported_led_encoding", "path": "rows[0]", "message": "Synthetic special LED byte retained"}}
		}},
		{name: "unresolved-read-profile-withholds-only-interpretation", kind: "ram", data: ram, complete: true, change: func(v map[string]any) {
			ref := nativeWireTestObject(nativeWireTestObject(v, "profile"), "read_reference")
			ref["rows_decodable"], ref["applicability"] = false, "model-only"
			r := nativeWireTestObject(v, "ram")
			r["sequence"], r["sequence_digest"], r["decoding_status"] = nil, nil, "profile-unresolved"
		}},
		{name: "cancel-between-rows-has-only-top-level-error", kind: "ram", data: ram, change: func(v map[string]any) {
			v["complete"], v["status"] = false, "partial"
			v["error"] = nativeWireTestTree(t, playback)["error"]
			r := nativeWireTestObject(v, "ram")
			r["rows"] = r["rows"].([]any)[:1]
			r["unread_positions"] = []any{1}
			r["sequence"], r["sequence_digest"], r["decoding_status"] = nil, nil, "incomplete"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.data
			if tc.change != nil {
				data = nativeWireTestMutate(t, data, tc.change)
			}
			start, count := 0, 0
			if tc.kind == "ram" {
				count = 2
			}
			got, err := decodeNativeInspectionResponse(data, "20000001", tc.kind, start, count)
			if err != nil || got.Kind != tc.kind || got.Complete != tc.complete {
				t.Fatalf("valid observation rejected or changed kind/completeness: kind=%q complete=%t err=%v", got.Kind, got.Complete, err)
			}
		})
	}
}

func TestNativeInspectionWireRejectsLossyJSONAndMalformedShapes(t *testing.T) {
	ram := nativeWireTestFixture(t, "ram-complete")
	playback := nativeWireTestFixture(t, "playback-partial")
	badRAM := map[string]func(map[string]any){
		"unknown-root-field":       func(v map[string]any) { v["extra"] = false },
		"case-alias":               func(v map[string]any) { v["Contract"] = v["contract"] },
		"missing-required":         func(v map[string]any) { delete(v, "contract") },
		"missing-explicit-null":    func(v map[string]any) { delete(v, "playback") },
		"null-nonnullable":         func(v map[string]any) { v["complete"] = nil },
		"wrong-scalar-type":        func(v map[string]any) { v["complete"] = "true" },
		"wrong-kind":               func(v map[string]any) { v["kind"] = "playback" },
		"wrong-serial":             func(v map[string]any) { nativeWireTestObject(v, "device")["serial"] = "20000002" },
		"wrong-firmware-source":    func(v map[string]any) { nativeWireTestObject(v, "device")["firmware_source"] = "guessed" },
		"wrong-source":             func(v map[string]any) { v["source"] = "cached" },
		"wrong-consistency":        func(v map[string]any) { v["consistency"] = "atomic" },
		"empty-instance":           func(v map[string]any) { v["daemon_instance_id"] = "" },
		"session-with-space":       func(v map[string]any) { v["device_session_id"] = "bad session" },
		"invalid-time":             func(v map[string]any) { v["started_at"] = "yesterday" },
		"zero-time":                func(v map[string]any) { v["started_at"] = "0001-01-01T00:00:00Z" },
		"negative-duration":        func(v map[string]any) { v["duration_ns"] = -1 },
		"float-integer":            func(v map[string]any) { v["duration_ns"] = json.Number("1.0") },
		"exponent-integer":         func(v map[string]any) { v["duration_ns"] = json.Number("1e6") },
		"int64-overflow":           func(v map[string]any) { v["duration_ns"] = json.Number("9223372036854775808") },
		"unknown-status":           func(v map[string]any) { v["status"] = "playing" },
		"status-complete-mismatch": func(v map[string]any) { v["status"] = "partial" },
		"complete-with-error":      func(v map[string]any) { v["error"] = nativeWireTestTree(t, playback)["error"] },
		"null-rows":                func(v map[string]any) { nativeWireTestObject(v, "ram")["rows"] = nil },
		"null-issues":              func(v map[string]any) { nativeWireTestObject(v, "ram")["issues"] = nil },
		"deployable":               func(v map[string]any) { nativeWireTestObject(v, "ram")["deployable"] = true },
		"request-range-moved": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestObject(v, "ram"), "requested_range")["start_line"] = 1
		},
		"row-duplicate-position":             func(v map[string]any) { nativeWireTestRow(v, 1)["position"] = 0 },
		"row-outside-range":                  func(v map[string]any) { nativeWireTestRow(v, 0)["position"] = 32 },
		"invented-unread-position":           func(v map[string]any) { nativeWireTestObject(v, "ram")["unread_positions"] = []any{1} },
		"short-fixed-RGB-array":              func(v map[string]any) { nativeWireTestRow(v, 0)["rgb8"] = []any{255, 0} },
		"long-fixed-RGB-array":               func(v map[string]any) { nativeWireTestRow(v, 0)["rgb8"] = []any{255, 0, 0, 0} },
		"RGB-byte-overflow":                  func(v map[string]any) { nativeWireTestRow(v, 0)["rgb8"] = []any{256, 0, 0} },
		"RGB-byte-negative":                  func(v map[string]any) { nativeWireTestRow(v, 0)["rgb8"] = []any{-1, 0, 0} },
		"ticks-uint16-overflow":              func(v map[string]any) { nativeWireTestRow(v, 0)["duration_ticks"] = 65536 },
		"ticks-negative":                     func(v map[string]any) { nativeWireTestRow(v, 0)["duration_ticks"] = -1 },
		"LED-byte-overflow":                  func(v map[string]any) { nativeWireTestRow(v, 0)["led_code"] = 256 },
		"raw-RGB-disagrees":                  func(v map[string]any) { nativeWireTestRow(v, 0)["rgb8"] = []any{1, 2, 3} },
		"raw-ticks-disagree":                 func(v map[string]any) { nativeWireTestRow(v, 0)["duration_ticks"] = 49 },
		"raw-LED-disagrees":                  func(v map[string]any) { nativeWireTestRow(v, 0)["led_code"] = 1 },
		"available-ticks-replaced-with-null": func(v map[string]any) { nativeWireTestRow(v, 0)["duration_ticks"] = nil },
		"missing-nullable-field":             func(v map[string]any) { delete(nativeWireTestRow(v, 0), "error") },
		"unknown-report-layout": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestRow(v, 0), "report")["layout"] = "future/v99"
		},
		"standard-layout-wrong-ID": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestRow(v, 0), "report")["requested_report_id"] = 2
		},
		"standard-layout-wrong-buffer": func(v map[string]any) { nativeWireTestObject(nativeWireTestRow(v, 0), "report")["buffer_length"] = 8 },
		"trusted-without-read": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestRow(v, 0), "report")["read_attempted"] = false
		},
		"trusted-invalid-length-flag": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestRow(v, 0), "report")["length_valid"] = false
		},
		"trusted-n-negative": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestRow(v, 0), "report")["returned_length"] = -1
		},
		"trusted-n-over-buffer": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestRow(v, 0), "report")["returned_length"] = 10
		},
		"raw-uppercase": func(v map[string]any) {
			r := nativeWireTestObject(nativeWireTestRow(v, 0), "report")
			r["raw_hex"] = strings.ToUpper(r["raw_hex"].(string))
		},
		"raw-nonhex": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestRow(v, 0), "report")["raw_hex"] = strings.Repeat("g", 18)
		},
		"raw-odd-length":      func(v map[string]any) { nativeWireTestObject(nativeWireTestRow(v, 0), "report")["raw_hex"] = "0" },
		"raw-length-mismatch": func(v map[string]any) { nativeWireTestObject(nativeWireTestRow(v, 0), "report")["returned_length"] = 8 },
		"raw-tail-mismatch": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestRow(v, 0), "report")["unknown_tail_hex"] = "ff"
		},
		"untrusted-with-raw-bytes": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestRow(v, 0), "report")["prefix_trusted"] = false
		},
		"unsupported-profile-qualification": func(v map[string]any) {
			nativeWireTestObject(v, "profile")["operation_qualification"] = "hardware-verified"
		},
		"capacity-null": func(v map[string]any) { nativeWireTestObject(v, "profile")["ram_lines"] = nil },
		"capacity33":    func(v map[string]any) { nativeWireTestObject(v, "profile")["ram_lines"] = 33 },
		"read-reference-firmware-mismatch": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestObject(v, "profile"), "read_reference")["firmware"] = 205
		},
		"read-reference-sources-null": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestObject(v, "profile"), "read_reference")["sources"] = nil
		},
		"decoded-rows-with-model-only-reference": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestObject(v, "profile"), "read_reference")["applicability"] = "model-only"
		},
		"sequence-color-disagrees": func(v map[string]any) {
			r := nativeWireTestObject(v, "ram")
			nativeWireTestObject(r, "sequence")["lines"].([]any)[0].(map[string]any)["color"] = "#00ff00"
			// This digest is correct for the changed sequence. Raw RAM bytes still
			// describe red, so hash consistency alone must not approve the claim.
			r["sequence_digest"] = "sha256:658830bb8b45208b3ffe04ac007d0173cdb8134db53a5ebdffe8664ea3bee739"
		},
		"sequence-duration-disagrees": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestObject(v, "ram"), "sequence")["lines"].([]any)[0].(map[string]any)["duration_ms"] = 510
		},
		"sequence-digest-disagrees": func(v map[string]any) {
			nativeWireTestObject(v, "ram")["sequence_digest"] = "sha256:" + strings.Repeat("0", 64)
		},
		"sequence-null-but-exact-status": func(v map[string]any) { nativeWireTestObject(v, "ram")["sequence"] = nil },
	}
	for name, change := range badRAM {
		t.Run("ram/"+name, func(t *testing.T) {
			if _, err := decodeNativeInspectionResponse(nativeWireTestMutate(t, ram, change), "20000001", "ram", 0, 2); err == nil {
				t.Fatal("malformed RAM response accepted")
			}
		})
	}
	badPlayback := map[string]func(map[string]any){
		"missing-nullable-error": func(v map[string]any) { delete(nativeWireTestObject(v, "playback"), "error") },
		"nullable-mode-missing-despite-available-prefix": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestObject(v, "playback"), "raw_fields")["mode"] = nil
		},
		"mode-outside-byte": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestObject(v, "playback"), "raw_fields")["mode"] = 256
		},
		"raw-count-disagrees": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestObject(v, "playback"), "raw_fields")["count"] = 1
		},
		"invented-playing-bool":      func(v map[string]any) { nativeWireTestObject(v, "playback")["playing"] = true },
		"partial-without-top-error":  func(v map[string]any) { v["error"] = nil },
		"partial-without-read-error": func(v map[string]any) { nativeWireTestObject(v, "playback")["error"] = nil },
		"native-code-unpaired":       func(v map[string]any) { nativeWireTestObject(v, "error")["native_platform"] = nil },
		"native-code-number-not-string": func(v map[string]any) {
			nativeWireTestObject(v, "error")["native_code"] = json.Number("18446744073709551615")
		},
		"native-code-overflow":         func(v map[string]any) { nativeWireTestObject(v, "error")["native_code"] = "18446744073709551616" },
		"native-code-leading-zero":     func(v map[string]any) { nativeWireTestObject(v, "error")["native_code"] = "01" },
		"native-code-signed":           func(v map[string]any) { nativeWireTestObject(v, "error")["native_code"] = "+1" },
		"native-code-exponent":         func(v map[string]any) { nativeWireTestObject(v, "error")["native_code"] = "1e3" },
		"native-code-without-platform": func(v map[string]any) { delete(nativeWireTestObject(v, "error"), "native_platform") },
		"error-flags-wrong-type":       func(v map[string]any) { nativeWireTestObject(v, "error")["message_truncated"] = "false" },
		"error-too-large":              func(v map[string]any) { nativeWireTestObject(v, "error")["message"] = strings.Repeat("x", 1025) },
		"empty-error-code":             func(v map[string]any) { nativeWireTestObject(v, "error")["code"] = "" },
		"unknown-end-encoding": func(v map[string]any) {
			nativeWireTestObject(nativeWireTestObject(v, "profile"), "read_reference")["playback_end_encoding"] = "guessed"
		},
		"profile-digest-malformed": func(v map[string]any) { nativeWireTestObject(v, "playback")["profile_digest"] = "sha256:bad" },
		"partial-cannot-acquire-configured-range": func(v map[string]any) {
			nativeWireTestObject(v, "playback")["configured_range"] = map[string]any{"start_line": 0, "line_count": 2}
		},
		"range-disagrees-with-registers": func(v map[string]any) {
			nativeWireTestCompletePlayback(v)
			nativeWireTestObject(nativeWireTestObject(v, "playback"), "configured_range")["line_count"] = 3
		},
		"unknown-mode-cannot-acquire-range": func(v map[string]any) {
			nativeWireTestCompletePlayback(v)
			p := nativeWireTestObject(v, "playback")
			nativeWireTestObject(p, "raw_fields")["mode"] = 255
			nativeWireTestByte(t, nativeWireTestObject(p, "report"), 2, 255)
		},
		"unknown-profile-cannot-acquire-range": func(v map[string]any) {
			nativeWireTestCompletePlayback(v)
			nativeWireTestObject(nativeWireTestObject(v, "profile"), "read_reference")["playback_end_encoding"] = "unknown"
		},
		"exclusive-end-zero-is-not-full-range-sentinel": func(v map[string]any) {
			nativeWireTestCompletePlayback(v)
			p := nativeWireTestObject(v, "playback")
			nativeWireTestObject(p, "raw_fields")["end"] = 0
			nativeWireTestByte(t, nativeWireTestObject(p, "report"), 4, 0)
			nativeWireTestObject(p, "configured_range")["line_count"] = 32
		},
	}
	for name, change := range badPlayback {
		t.Run("playback/"+name, func(t *testing.T) {
			if _, err := decodeNativeInspectionResponse(nativeWireTestMutate(t, playback, change), "20000001", "playback", 0, 0); err == nil {
				t.Fatal("malformed playback response accepted")
			}
		})
	}
	rawCases := map[string][]byte{
		"empty":              nil,
		"null":               []byte("null"),
		"array":              append(append([]byte("["), ram...), ']'),
		"duplicate":          append([]byte(`{"contract":"duplicate",`), ram[1:]...),
		"escaped-duplicate":  append([]byte(`{"contr\u0061ct":"duplicate",`), ram[1:]...),
		"trailing":           append(append([]byte(nil), ram...), []byte("{}")...),
		"invalid-utf8":       bytes.Replace(ram, []byte("Synthetic"), []byte{0xff}, 1),
		"unpaired-surrogate": bytes.Replace(ram, []byte("Synthetic"), []byte(`\ud800`), 1),
		"over64KiB":          append(append([]byte(nil), ram...), bytes.Repeat([]byte(" "), 64<<10)...),
	}
	for name, data := range rawCases {
		t.Run("raw/"+name, func(t *testing.T) {
			if _, err := decodeNativeInspectionResponse(data, "20000001", "ram", 0, 2); err == nil {
				t.Fatal("lossy or non-object JSON response accepted")
			}
		})
	}
}

func TestNativeInspectionReadArgumentsAreClosedAndBounded(t *testing.T) {
	valid := []struct{ kind, raw string }{
		{"ram", `{"serial":"20000001","start_line":0,"line_count":1}`},
		{"ram", `{"serial":"ABCDEF01","start_line":31,"line_count":1}`},
		{"ram", `{"serial":"20000001","start_line":0,"line_count":32}`},
		{"playback", `{"serial":"20000001"}`},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.kind+"/"+tc.raw, func(t *testing.T) {
			if _, err := readNativeInspectionArgs(json.RawMessage(tc.raw), tc.kind); err != nil {
				t.Fatal(err)
			}
		})
	}
	invalid := []struct{ name, kind, raw string }{
		{"empty", "ram", ""},
		{"null", "ram", "null"},
		{"array", "ram", "[]"},
		{"missing-all", "ram", "{}"},
		{"missing-serial", "ram", `{"start_line":0,"line_count":1}`},
		{"missing-start", "ram", `{"serial":"20000001","line_count":1}`},
		{"missing-count", "ram", `{"serial":"20000001","start_line":0}`},
		{"null-serial", "ram", `{"serial":null,"start_line":0,"line_count":1}`},
		{"null-start", "ram", `{"serial":"20000001","start_line":null,"line_count":1}`},
		{"unknown-field", "ram", `{"serial":"20000001","start_line":0,"line_count":1,"profile":{}}`},
		{"policy-forbidden", "ram", `{"serial":"20000001","start_line":0,"line_count":1,"policy":"replace"}`},
		{"case-alias", "ram", `{"Serial":"20000001","start_line":0,"line_count":1}`},
		{"duplicate-serial", "ram", `{"serial":"20000001","serial":"20000002","start_line":0,"line_count":1}`},
		{"escaped-duplicate", "ram", `{"serial":"20000001","start_line":0,"sta\u0072t_line":1,"line_count":1}`},
		{"fraction", "ram", `{"serial":"20000001","start_line":0.5,"line_count":1}`},
		{"integer-as-float", "ram", `{"serial":"20000001","start_line":0,"line_count":1.0}`},
		{"exponent", "ram", `{"serial":"20000001","start_line":0,"line_count":1e0}`},
		{"integer-as-string", "ram", `{"serial":"20000001","start_line":"0","line_count":1}`},
		{"overflow", "ram", `{"serial":"20000001","start_line":18446744073709551616,"line_count":1}`},
		{"negative-start", "ram", `{"serial":"20000001","start_line":-1,"line_count":1}`},
		{"start32", "ram", `{"serial":"20000001","start_line":32,"line_count":1}`},
		{"count0", "ram", `{"serial":"20000001","start_line":0,"line_count":0}`},
		{"count33", "ram", `{"serial":"20000001","start_line":0,"line_count":33}`},
		{"range-over-capacity", "ram", `{"serial":"20000001","start_line":31,"line_count":2}`},
		{"short-serial", "playback", `{"serial":"2000001"}`},
		{"long-serial", "playback", `{"serial":"200000001"}`},
		{"nonhex-serial", "playback", `{"serial":"2000000g"}`},
		{"playback-range", "playback", `{"serial":"20000001","start_line":0,"line_count":1}`},
		{"playback-empty", "playback", `{}`},
		{"trailing-object", "playback", `{"serial":"20000001"}{}`},
		{"unknown-kind", "flash", `{"serial":"20000001"}`},
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			if _, err := readNativeInspectionArgs(json.RawMessage(tc.raw), tc.kind); err == nil {
				t.Fatal("invalid request arguments accepted")
			}
		})
	}
}
