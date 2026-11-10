#!/usr/bin/env python3
"""Read-only local verification for the public English documentation slice."""

import hashlib
import json
import re
import subprocess
from collections import Counter
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[2]
BASE = '1e2e7650e2cff6686d95cb9908953e5fd370f2bc'
ARTIFACT = Path(__file__).resolve().parent


def git(*args):
    return subprocess.check_output(['git', '-C', str(ROOT), *args])


def original(path):
    return git('show', f'{BASE}:{path}')


def digest(data):
    return hashlib.sha256(data).hexdigest()


def pointer(document, fragment):
    assert fragment.startswith('/'), fragment
    value = document
    for part in fragment[1:].split('/'):
        key = part.replace('~1', '/').replace('~0', '~')
        value = value[int(key)] if isinstance(value, list) else value[key]
    return value


def difference(before, after, path=()):
    assert type(before) is type(after), path
    if isinstance(before, dict):
        assert before.keys() == after.keys(), path
        return [item for key in before for item in difference(before[key], after[key], path + (key,))]
    if isinstance(before, list):
        assert len(before) == len(after), path
        return [item for index, value in enumerate(before) for item in difference(value, after[index], path + (str(index),))]
    return [(path, before, after)] if before != after else []


def permitted(path, before, after):
    if not isinstance(before, str) or not isinstance(after, str):
        return False
    if any(part in ('example', 'examples', 'x-availability') for part in path):
        return False
    return path == ('info', 'title') or path[-1] in ('description', 'summary')


def embedded_example_bytes(source):
    """Extract exact raw JSON bytes at all example/examples subtrees."""
    text = source.decode('utf-8')
    decoder = json.JSONDecoder()
    spans = {}

    def space(offset):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        return offset

    def visit(offset, path=()):
        start = offset = space(offset)
        if text[offset] == '{':
            offset = space(offset + 1)
            while text[offset] != '}':
                key, offset = decoder.raw_decode(text, offset)
                offset = space(offset)
                assert text[offset] == ':'
                offset = visit(offset + 1, path + (key,))
                offset = space(offset)
                if text[offset] != ',':
                    break
                offset = space(offset + 1)
            assert text[offset] == '}'
            offset += 1
        elif text[offset] == '[':
            offset = space(offset + 1)
            index = 0
            while text[offset] != ']':
                offset = visit(offset, path + (str(index),))
                index += 1
                offset = space(offset)
                if text[offset] != ',':
                    break
                offset = space(offset + 1)
            assert text[offset] == ']'
            offset += 1
        else:
            _, offset = decoder.raw_decode(text, offset)
        if path and path[-1] in ('example', 'examples'):
            spans[path] = text[start:offset].encode('utf-8')
        return offset

    visit(0)
    return spans


before_bytes = original('docs/openapi.json')
after_bytes = (ROOT / 'docs/openapi.json').read_bytes()
before = json.loads(before_bytes)
after = json.loads(after_bytes)
changes = difference(before, after)
assert changes and all(permitted(*entry) for entry in changes), 'non-prose OpenAPI change'

embedded_before = embedded_example_bytes(before_bytes)
embedded_after = embedded_example_bytes(after_bytes)
assert embedded_before == embedded_after, 'embedded example bytes changed'

inventory = json.loads(original('docs/api-http-inventory.json'))
canonical = [(item['method'], item['path']) for item in inventory['routes']]
assert len(canonical) == len(set(canonical)) == 44
manual_path = ROOT / 'docs/api-reference.md'
manual = manual_path.read_text()
listed = re.findall(r'^### (GET|POST) (/\S+)$', manual, re.MULTILINE)
assert listed == canonical, 'incomplete/out-of-order canonical route coverage'
http_methods = {'get', 'put', 'post', 'delete', 'options', 'head', 'patch', 'trace'}
operations = {(method.upper(), path) for path, item in after['paths'].items() for method in item if method in http_methods}
assert len(after['paths']) == 43 and len(operations) == 48
def route_shape(operation):
    method, path = operation
    return method, re.sub(r'\{[^}]+\}', '{}', path)


# OpenAPI uses jobId where the canonical inventory uses id; positions match.
canonical_shapes = {route_shape(operation) for operation in canonical}
assert canonical_shapes <= {route_shape(operation) for operation in operations}
aliases = {operation for operation in operations if route_shape(operation) not in canonical_shapes}
assert aliases == {('GET', path) for path in ('/v1/patterns/import', '/v2/patterns/preview', '/v2/patterns/import', '/v2/patterns/preview-ref')}
assert before['paths'].keys() == after['paths'].keys()
for method, path in operations:
    assert before['paths'][path][method.lower()].get('x-availability') == after['paths'][path][method.lower()].get('x-availability')

models = list(after['components']['schemas'])
listed_models = re.findall(r'\| \[`([^`]+)`\]\(openapi\.json#/components/schemas/', manual)
assert listed_models == models and len(models) == 111

refs = []
chinese_prose = []


def inspect(value, path=()):
    if isinstance(value, dict):
        for key, item in value.items():
            if key == '$ref':
                assert item.startswith('#/'), item
                pointer(after, item[1:])
                refs.append(item)
            if isinstance(item, str) and (key in ('description', 'summary') or path == ('info',) and key == 'title'):
                if any('\u4e00' <= char <= '\u9fff' for char in item):
                    chinese_prose.append('/'.join(path + (key,)))
            inspect(item, path + (key,))
    elif isinstance(value, list):
        for index, item in enumerate(value):
            inspect(item, path + (str(index),))


inspect(after)
assert not chinese_prose, chinese_prose

example_files = sorted((ROOT / 'docs/examples/api').glob('*.json'))
assert len(example_files) == 14
examples = []
for file in example_files:
    relative = file.relative_to(ROOT).as_posix()
    data = file.read_bytes()
    assert data == original(relative), relative
    json.loads(data)
    assert f'](examples/api/{file.name})' in manual, file.name
    examples.append({'path': relative, 'sha256': digest(data), 'bytes': len(data)})

# All JSON fences retain existing authored/example values, including the title.
old_manual = original('docs/api-reference.zh-CN.md').decode('utf-8')
old_json_fences = [json.loads(block) for block in re.findall(r'```json\n(.*?)\n```', old_manual, re.DOTALL)]
json_fences = re.findall(r'```json\n(.*?)\n```', manual, re.DOTALL)
for block in json_fences:
    assert json.loads(block) in old_json_fences, 'changed authored/example JSON fence'
author_data = json.loads((ROOT / 'docs/examples/api/pattern-v2.preview.json').read_text())
assert author_data in [json.loads(block) for block in json_fences]

frozen_paths = [
    'docs/api-reference.zh-CN.md', 'docs/api-http-inventory.json',
    'docs/api-low-examples.json', 'docs/examples/api/README.zh-CN.md',
    'README.zh-CN.md', 'go.mod', 'go.sum',
] + sorted(file.relative_to(ROOT).as_posix() for file in (ROOT / 'docs').glob('mcp-tools*.json'))
frozen = []
for relative in frozen_paths:
    data = (ROOT / relative).read_bytes()
    assert data == original(relative), relative
    frozen.append({'path': relative, 'sha256': digest(data), 'bytes': len(data)})
tools = json.loads((ROOT / 'docs/mcp-tools.schema.json').read_text())['tools']
assert len(tools) == 16
assert tools == json.loads(original('docs/mcp-tools.schema.json'))['tools']
assert len(json.loads((ROOT / 'docs/mcp-tools.candidate-20.schema.json').read_text())['tools']) == 20

linked_documents = ['README.md', 'llms.txt', 'docs/api-reference.md', 'docs/examples/api/README.md']
links = []
for relative in linked_documents:
    file = ROOT / relative
    text = file.read_text()
    fenced = re.sub(r'```.*?```', '', text, flags=re.DOTALL)
    assert not re.search(r'[\u4e00-\u9fff]', fenced), f'Chinese prose in {relative}'
    for target in re.findall(r'\[[^\]]+\]\(([^\s)]+)\)', text):
        parsed = urlsplit(target)
        if parsed.scheme:
            continue
        resolved = (file.parent / unquote(parsed.path)).resolve()
        assert resolved == ROOT or ROOT in resolved.parents, target
        assert resolved.exists(), (relative, target)
        if parsed.fragment and resolved.suffix == '.json':
            pointer(json.loads(resolved.read_text()), unquote(parsed.fragment))
        links.append({'document': relative, 'target': target})

allowed = set(linked_documents) | {'docs/openapi.json'}
changed_paths = sorted(set(git('diff', '--name-only', BASE).decode().splitlines()) |
                       set(git('ls-files', '--others', '--exclude-standard').decode().splitlines()))
assert all(path in allowed or path.startswith('artifacts/public-english-docs-20261110/') for path in changed_paths), changed_paths

runtime = json.loads((ARTIFACT / 'runtime-chinese-inventory.json').read_text())
runtime_counts = Counter(item['path'] for item in runtime)
assert len(runtime) == 197 and len(runtime_counts) == 11
for item in runtime:
    assert not item['path'].endswith('_test.go') and not item['path'].startswith('docs/')
    assert (ROOT / item['path']).exists()

report = {
    'status': 'PASS', 'date': '2026-11-10', 'base_commit': BASE,
    'scope': 'local documentation/structure/bytes/links only; no daemon, USB, browser, install, configuration, PR, release, or private camera access',
    'openapi': {
        'only_permitted_prose_changed': True, 'changed_prose_fields': len(changes),
        'changed_prose_paths': ['/' + '/'.join(path) for path, _, _ in changes],
        'paths': len(after['paths']), 'operations_including_aliases': len(operations),
        'canonical_operations': len(canonical), 'aliases': sorted([list(item) for item in aliases]),
        'models': len(models), 'model_names': models, 'validated_internal_refs': len(refs),
        'x_availability_unchanged': True, 'non_prose_structure_unchanged': True,
        'before_sha256': digest(before_bytes), 'after_sha256': digest(after_bytes),
        'embedded_examples': [{'path': '/' + '/'.join(path), 'sha256': digest(data), 'bytes': len(data)} for path, data in embedded_after.items()],
    },
    'manual': {'canonical_operations': [f'{method} {path}' for method, path in listed], 'models_linked': len(listed_models), 'json_examples_linked': len(examples), 'unchanged_json_data_fences': len(json_fences)},
    'example_bytes_unchanged': examples, 'frozen_files_unchanged': frozen,
    'frozen_mcp16_complete_objects_unchanged': True, 'candidate_mcp20_unchanged': True,
    'local_links': {'count': len(links), 'all_targets_exist': True, 'checked': links},
    'runtime_chinese_backlog': {'literal_count': len(runtime), 'file_count': len(runtime_counts), 'by_path': dict(runtime_counts), 'by_category': dict(Counter(item['category'] for item in runtime)), 'runtime_changes': False},
    'changed_paths': sorted(changed_paths),
}
(ARTIFACT / 'validation.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'status': report['status'], 'changed_prose_fields': len(changes), 'canonical_operations': len(canonical), 'models': len(models), 'example_files': len(examples), 'embedded_examples': len(embedded_after), 'links': len(links), 'runtime_backlog_literals': len(runtime)}, indent=2))
