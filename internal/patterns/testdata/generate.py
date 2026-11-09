#!/usr/bin/env python3
"""Independent Python fixtures for the frozen Pattern record hash domain.

No Go codec output, Go execution, HTTP, device, filesystem library or service API
is used. This generates selected valid examples, not a production import parser.
"""
from copy import deepcopy
import hashlib
import json
from pathlib import Path
import re

HERE = Path(__file__).resolve().parent
DOMAIN = b"nb1-pattern-record/v1\n"
CONTRACT = "nb1-program-effect/v1"


def require(value, message):
    if not value:
        raise ValueError(message)


def scalar_string(value):
    require(isinstance(value, str), "string required")
    require(all(not 0xD800 <= ord(c) <= 0xDFFF for c in value),
            "surrogate code points are not Unicode scalars")
    value.encode("utf-8", "strict")
    return value


def quote(value):
    # Intentionally do not use Python json.dumps for canonical string escaping:
    # it uses short escapes for several C0 characters. The domain requires \u00xx.
    pieces = ['"']
    for char in scalar_string(value):
        code = ord(char)
        if code < 32:
            pieces.append("\\u%04x" % code)
        elif char == '"':
            pieces.append('\\"')
        elif char == "\\":
            pieces.append("\\\\")
        else:
            pieces.append(char)
    pieces.append('"')
    return "".join(pieces)


def canonical(value):
    if isinstance(value, str):
        return quote(value)
    if type(value) is int:
        return str(value)
    if isinstance(value, list):
        return "[" + ",".join(canonical(v) for v in value) + "]"
    if isinstance(value, dict):
        # Dict insertion order is the frozen field order, never sort_keys.
        return "{" + ",".join(quote(k) + ":" + canonical(v)
                              for k, v in value.items()) + "}"
    raise ValueError("canonical record contains an unsupported value")


def closed(value, fields, required):
    require(type(value) is dict, "object required")
    require(set(value).issubset(fields) and set(required).issubset(value),
            "closed object fields mismatch")


def integer(value, low, high, quantum=1):
    require(type(value) is int and low <= value <= high and value % quantum == 0,
            "integer range/quantum mismatch")
    return value


def color(value):
    require(isinstance(value, str) and re.fullmatch(r"#[0-9A-Fa-f]{6}", value),
            "raw Hex6 required")
    return value.lower()


def step(value):
    closed(value, {"led", "color", "fade_ms", "hold_ms"}, {"color"})
    return {
        "led": integer(value.get("led", 0), 0, 2),
        "color": color(value["color"]),
        "fade_ms": integer(value.get("fade_ms", 0), 0, 600000, 10),
        "hold_ms": integer(value.get("hold_ms", 0), 0, 3600000),
    }


def program(value):
    closed(value, {"steps", "repeat", "on_finish"}, {"steps"})
    require(type(value["steps"]) is list and 1 <= len(value["steps"]) <= 1024,
            "bounded nonempty steps required")
    steps = [step(s) for s in value["steps"]]
    repeat = value.get("repeat", 0)
    require(type(repeat) is int, "integer repeat required")
    repeat = 1 if repeat == 0 else repeat
    integer(repeat, 1, 10000)
    finish = value.get("on_finish", "")
    finish = "off" if finish == "" else finish
    require(finish in ("off", "hold"), "invalid on_finish")
    require(sum(s["fade_ms"] + s["hold_ms"] + 10 for s in steps) * repeat
            <= 86400000, "program exceeds nominal 24h budget")
    return {"steps": steps, "repeat": repeat, "on_finish": finish}


def effect(value):
    require(type(value) is dict and value.get("kind") in ("steady", "pulse"),
            "supported effect kind required")
    if value["kind"] == "steady":
        closed(value, {"kind", "color", "led", "fade_ms"}, {"kind", "color"})
        source = {
            "kind": "steady",
            "color": color(value["color"]),
            "led": integer(value.get("led", 0), 0, 2),
            "fade_ms": integer(value.get("fade_ms", 0), 0, 600000, 10),
        }
        compiled = program({"steps": [{"led": source["led"],
                                      "color": source["color"],
                                      "fade_ms": source["fade_ms"]}],
                            "repeat": 1, "on_finish": "hold"})
    else:
        closed(value, {"kind", "color", "led", "cycles", "period_ms", "on_finish"},
               {"kind", "color"})
        source = {
            "kind": "pulse",
            "color": color(value["color"]),
            "led": integer(value.get("led", 0), 0, 2),
            "cycles": integer(value.get("cycles", 1), 1, 10000),
            "period_ms": integer(value.get("period_ms", 1000), 60, 1200000, 10),
            "on_finish": value.get("on_finish", "off"),
        }
        require(source["on_finish"] in ("off", "hold"), "invalid pulse finish")
        up = source["period_ms"] // 20 * 10
        down = source["period_ms"] - up
        compiled = program({
            "steps": [
                {"led": source["led"], "color": "#000000"},
                {"led": source["led"], "color": source["color"],
                 "fade_ms": up - 20},
                {"led": source["led"], "color": "#000000",
                 "fade_ms": down - 10},
            ],
            "repeat": source["cycles"], "on_finish": source["on_finish"],
        })
    return source, compiled


def record(item):
    closed(item, {"id", "title", "program", "effect"}, {"id", "title"})
    require(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,63}", item["id"]),
            "invalid ASCII id")
    title = scalar_string(item["title"])
    require(1 <= len(title) <= 256, "invalid title scalar count")
    require(("program" in item) != ("effect" in item), "oneof source required")
    if "program" in item:
        compiled = program(item["program"])
        source = {"program": deepcopy(compiled)}
    else:
        normalized, compiled = effect(item["effect"])
        source = {"effect": normalized}
    value = {"id": item["id"], "title": title, "source": source,
             "compiled_program": compiled, "compile_contract": CONTRACT}
    encoded = canonical(value).encode("utf-8")
    preimage = DOMAIN + encoded
    revision = "sha256:" + hashlib.sha256(preimage).hexdigest()
    return dict(value, revision=revision), encoded, preimage


def example(name, item, authoring_json=None):
    package = {"schema_version": 1, "patterns": [deepcopy(item)]}
    if authoring_json is not None:
        require(json.loads(authoring_json) == package, "alternate JSON mismatch")
    normalized, encoded, preimage = record(item)
    return {
        "name": name,
        "authoring_package": package,
        "authoring_json": (authoring_json if authoring_json is not None else
                           json.dumps(package, ensure_ascii=False,
                                      separators=(",", ":"))),
        "record": normalized,
        "snapshot_package": {"schema_version": 1,
                             "kind": "nb1-pattern-snapshot",
                             "records": [deepcopy(normalized)]},
        "canonical_json": encoded.decode("utf-8"),
        "canonical_hex": encoded.hex(),
        "preimage_hex": preimage.hex(),
        "revision": normalized["revision"],
    }


def generate():
    cases = []
    pulse = {"id": "build-success", "title": "构建完成短提示",
             "effect": {"kind": "pulse", "color": "#00A000",
                        "cycles": 2, "period_ms": 800}}
    cases.append(example("pulse-800x2-chinese-defaults", pulse))
    pulse_explicit = deepcopy(pulse)
    pulse_explicit["effect"] = {"on_finish": "off", "period_ms": 800,
                                "cycles": 2, "led": 0,
                                "color": "#00a000", "kind": "pulse"}
    cases.append(example("pulse-800x2-explicit-reordered", pulse_explicit))
    same_program = {"id": pulse["id"], "title": pulse["title"],
                    "program": deepcopy(cases[0]["record"]["compiled_program"])}
    cases.append(example("pulse-same-execution-program-source", same_program))
    steady = {"id": "steady-ok", "title": "常亮",
              "effect": {"kind": "steady", "color": "#ABCDEF"}}
    cases.append(example("steady-defaults", steady))
    explicit = deepcopy(steady)
    explicit["effect"] = {"kind": "steady", "color": "#abcdef",
                           "led": 0, "fade_ms": 0}
    cases.append(example("steady-explicit-defaults", explicit))
    base_program = {"id": "program-default", "title": "默认程序",
                    "program": {"steps": [{"color": "#ABCDEF"}]}}
    cases.append(example("program-defaults", base_program))
    explicit_program = deepcopy(base_program)
    explicit_program["program"] = {"on_finish": "off", "repeat": 1, "steps": [
        {"hold_ms": 0, "fade_ms": 0, "color": "#abcdef", "led": 0}]}
    cases.append(example("program-explicit-defaults", explicit_program))
    strange = {"id": "all-escapes",
               "title": "".join(chr(n) for n in range(32)) + '"\\<>&/\u2028\u2029😀',
               "effect": {"kind": "steady", "color": "#102030",
                          "led": 2, "fade_ms": 10}}
    cases.append(example("all-C0-quote-backslash-html-lines-emoji", strange))
    unicode = {"id": "unicode", "title": "café",
               "effect": {"kind": "steady", "color": "#abcdef"}}
    cases.append(example("unicode-precomposed", unicode))
    decomposed = deepcopy(unicode)
    decomposed["title"] = "cafe\u0301"
    cases.append(example("unicode-decomposed", decomposed))
    alternate = json.dumps({"patterns": [unicode], "schema_version": 1},
                           ensure_ascii=True, sort_keys=True, indent=2)
    cases.append(example("unicode-alternate-JSON-escapes-order-whitespace",
                         unicode, alternate))
    idcase = {"id": "Build-Success", "title": "ID区分大小写",
              "effect": {"kind": "steady", "color": "#112233"}}
    cases.append(example("id-uppercase", idcase))
    idlower = deepcopy(idcase)
    idlower["id"] = "build-success"
    cases.append(example("id-lowercase", idlower))
    title_spaces = deepcopy(steady)
    title_spaces["title"] = "  常亮  "
    cases.append(example("title-preserves-spaces", title_spaces))
    pulse_default = {"id": "pulse-default", "title": "默认呼吸",
                     "effect": {"kind": "pulse", "color": "#AABBCC"}}
    cases.append(example("pulse-default-period-cycles", pulse_default))
    pulse_full = deepcopy(pulse_default)
    pulse_full["effect"] = {"kind": "pulse", "color": "#aabbcc", "led": 0,
                            "cycles": 1, "period_ms": 1000, "on_finish": "off"}
    cases.append(example("pulse-explicit-default-period-cycles", pulse_full))
    return cases


def main():
    cases = generate()
    index = {c["name"]: c for c in cases}
    relations = []

    def relation(name, left, right, equal, same_program=None):
        a, b = index[left], index[right]
        require((a["revision"] == b["revision"]) == equal,
                "revision relationship failed: " + name)
        if same_program is not None:
            require((a["record"]["compiled_program"] ==
                     b["record"]["compiled_program"]) == same_program,
                    "execution relationship failed: " + name)
        relations.append({"name": name, "left": left, "right": right,
                          "revision_equal": equal,
                          "compiled_program_equal": same_program,
                          "status": "PASS"})

    relation("pulse-explicit-defaults-and-key-order",
             cases[0]["name"], cases[1]["name"], True, True)
    relation("same-execution-different-source",
             cases[0]["name"], cases[2]["name"], False, True)
    relation("steady-explicit-defaults",
             cases[3]["name"], cases[4]["name"], True, True)
    relation("program-explicit-defaults",
             cases[5]["name"], cases[6]["name"], True, True)
    relation("unicode-normalization-not-implicit",
             cases[8]["name"], cases[9]["name"], False, True)
    relation("JSON-escape-order-whitespace-equivalence",
             cases[8]["name"], cases[10]["name"], True, True)
    relation("id-case-is-content",
             cases[11]["name"], cases[12]["name"], False, True)
    relation("title-no-trim",
             cases[3]["name"], cases[13]["name"], False, True)
    relation("pulse-defaults",
             cases[14]["name"], cases[15]["name"], True, True)
    pulse = cases[0]["record"]["compiled_program"]
    require([s["fade_ms"] for s in pulse["steps"]] == [0, 380, 390]
            and pulse["repeat"] == 2 and pulse["on_finish"] == "off",
            "800ms pulse snapshot mismatch")
    special = cases[7]
    for n in range(32):
        require("\\u%04x" % n in special["canonical_json"], "missing C0 escape")
    require("\u2028".encode().hex() in special["canonical_hex"]
            and "\u2029".encode().hex() in special["canonical_hex"]
            and "😀".encode().hex() in special["canonical_hex"],
            "non-ASCII must remain raw UTF-8")
    require("<>&/" in special["canonical_json"], "HTML/slash unexpectedly escaped")
    for c in cases:
        raw = bytes.fromhex(c["canonical_hex"])
        preimage = bytes.fromhex(c["preimage_hex"])
        require(raw == c["canonical_json"].encode("utf-8"), "JSON byte mismatch")
        require(preimage == DOMAIN + raw, "domain/newline mismatch")
        require(not raw.startswith(b"\xef\xbb\xbf") and not raw.endswith(b"\n"),
                "BOM or trailing newline")
        require("sha256:" + hashlib.sha256(preimage).hexdigest() == c["revision"],
                "hash mismatch")
        require(json.loads(raw) == {k: v for k, v in c["record"].items()
                                    if k != "revision"}, "record parse mismatch")
    output = {"schema_version": 1, "record_domain": DOMAIN[:-1].decode("ascii"),
              "compile_contract": CONTRACT, "case_count": len(cases),
              "cases": cases, "relations": relations}
    encoded = json.dumps(output, ensure_ascii=False, indent=2) + "\n"
    target = HERE / "golden.json"
    target.write_text(encoded, encoding="utf-8")
    print(json.dumps({"status": "PASS_SELECTED_GOLDEN_GENERATION",
                      "cases": len(cases), "relations": len(relations),
                      "golden_sha256": hashlib.sha256(target.read_bytes()).hexdigest(),
                      "pulse_revision": cases[0]["revision"]}))


if __name__ == "__main__":
    main()
