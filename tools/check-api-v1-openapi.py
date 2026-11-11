#!/usr/bin/env python3
"""Check the v1 OpenAPI document and run its requests through the real handler.

Documentation-only dependencies: jsonschema>=4.18 and openapi-spec-validator>=0.7.
The Go fixture uses a simulated device and temporary library. It opens no USB or
listening port. Existing runtime tests remain the authority for device execution.
"""
from pathlib import Path
import argparse
import importlib.metadata
import json
import os
import subprocess

from jsonschema import Draft202012Validator, FormatChecker
from openapi_spec_validator import OpenAPIV31SpecValidator
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT202012

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--report', type=Path)
args = parser.parse_args()
document = json.loads((ROOT / 'docs/api-v1.openapi.json').read_text())
assert document['openapi'] == '3.1.0'
OpenAPIV31SpecValidator(document).validate()
references = []

def visit(value):
    if isinstance(value, dict):
        if '$ref' in value:
            pointer = value['$ref']
            assert pointer.startswith('#/'), pointer
            target = document
            for part in pointer[2:].split('/'):
                target = target[part.replace('~1', '/').replace('~0', '~')]
            references.append(pointer)
        for child in value.values():
            visit(child)
    elif isinstance(value, list):
        for child in value:
            visit(child)

visit(document)
registry = Registry().with_resource('urn:nb1:api-v1', Resource(contents=document, specification=DRAFT202012))

def validate(schema, value):
    Draft202012Validator(schema, registry=registry, format_checker=FormatChecker()).validate(value)

schema_examples = 0
for name, schema in document['components']['schemas'].items():
    Draft202012Validator.check_schema(schema)
    for example in schema.get('examples', []):
        validate({'$ref': 'urn:nb1:api-v1#/components/schemas/' + name}, example)
        schema_examples += 1

# Resolve local document references from the common root while leaving the
# declaration itself untouched for source review.
def absolute_refs(value):
    if isinstance(value, dict):
        return {key: ('urn:nb1:api-v1' + child if key == '$ref' else absolute_refs(child)) for key, child in value.items()}
    if isinstance(value, list):
        return [absolute_refs(child) for child in value]
    return value

requests = []
operations = {}
for path, item in document['paths'].items():
    for method, operation in item.items():
        if method not in ('get', 'post', 'put', 'patch', 'delete', 'head', 'options'):
            continue
        name = operation['operationId']
        assert name not in operations
        operations[name] = operation
        for response in operation['responses'].values():
            for media in response.get('content', {}).values():
                for example in media.get('examples', {}).values():
                    validate(absolute_refs(media['schema']), example['value'])
        concrete = path
        for parameter in operation.get('parameters', []):
            concrete = concrete.replace('{' + parameter['name'] + '}', parameter['example'])
        body = operation.get('requestBody')
        examples = {'none': {'value': None}} if body is None else body['content']['application/json']['examples']
        for example_name, example in examples.items():
            value = example['value']
            if body is not None:
                validate(absolute_refs(body['content']['application/json']['schema']), value)
            requests.append({'name': name + '/' + example_name, 'method': method.upper(), 'path': concrete,
                             'body': '' if value is None else json.dumps(value, ensure_ascii=False, separators=(',', ':')),
                             'operation': name, 'valid': True})

assert len(operations) == 15 and len(document['paths']) == 13
rank = lambda request: 0 if request['method'] == 'PUT' else 1 if request['operation'] == 'preview' else 2 if request['operation'] == 'submitJob' else 3
requests.sort(key=rank)
requests.append({'name': 'sequence-negative-zero', 'method': 'POST', 'path': '/v1/preview',
                 'body': '{"sequence":{"timing":"serial","steps":[{"targets":[{"led":-0,"color":"#123456"}],"fade_ms":-0,"hold_ms":-0}]}}',
                 'operation': 'preview', 'valid': True})
negative = [
    ('integer-token-fraction', '{"action":{"kind":"fade","color":"red","duration_ms":10.0}}'),
    ('duplicate-field', '{"action":{"kind":"off"},"action":{"kind":"off"}}'),
    ('cross-field-interval', '{"sequence":{"timing":"interval","initial":[{"led":1,"color":"#000000"}],"steps":[{"targets":[{"led":1,"color":"#ffffff"}],"fade_ms":100,"interval_ms":50}]}}'),
    ('two-sources', '{"action":{"kind":"off"},"pattern_id":"gentle-pulse"}'),
]
for name, body in negative:
    requests.append({'name': name, 'method': 'POST', 'path': '/v1/preview', 'body': body, 'operation': 'preview', 'valid': False})
wire = [{key: value for key, value in request.items() if key in ('name', 'method', 'path', 'body')} for request in requests]
result = subprocess.run(['go', 'run', './tools/api-v1-openapi-runtime'], cwd=ROOT,
                        input=json.dumps(wire), text=True, capture_output=True, timeout=120,
                        env=dict(os.environ, GOWORK='off'))
if result.returncode:
    raise RuntimeError(result.stderr)
observations = json.loads(result.stdout)
assert len(observations) == len(requests)
for request, observed in zip(requests, observations):
    assert request['name'] == observed['name']
    status = observed['status']
    if request['valid']:
        assert 200 <= status < 300, (request['name'], status, observed['body'])
    else:
        assert status == 400, (request['name'], status, observed['body'])
    response = operations[request['operation']]['responses'][str(status)]
    validate(absolute_refs(response['content']['application/json']['schema']), observed['body'])

report = {
    'status': 'PASS', 'source': document['x-source'], 'openapi': document['openapi'],
    'paths': len(document['paths']), 'operations': len(operations), 'resolved_refs': len(references),
    'schemas': len(document['components']['schemas']), 'schema_examples': schema_examples,
    'runtime_examples': sum(request['valid'] for request in requests),
    'runtime_rejections': sum(not request['valid'] for request in requests),
    'validation_versions': {name: importlib.metadata.version(name) for name in ['jsonschema', 'openapi-spec-validator']},
    'scope': 'Real v1 HTTP handler, Action/Sequence/color decoders, Engine and temporary library with software-only backend. No USB, no listening port.',
    'observations': observations,
}
if args.report:
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({key: value for key, value in report.items() if key not in ['observations', 'source']}, ensure_ascii=False))
