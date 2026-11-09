#!/usr/bin/env python3
"""Offline Draft2020-12 schema, local-ref, inventory and actual-example checks.

Requires jsonschema>=4 and referencing, only as documentation check dependencies.
This does not validate raw lexical JSON or certify complete OpenAPI conformance.
"""
from pathlib import Path
import argparse
import copy
import hashlib
import importlib.metadata
import json
import re
import sys

from jsonschema import Draft202012Validator
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT202012

HERE=Path(__file__).resolve().parent
WORK=HERE.parents[1]
parser=argparse.ArgumentParser()
parser.add_argument('--report',type=Path)
args=parser.parse_args()
document=json.loads((WORK/'docs/openapi.json').read_text())
assert document['openapi']=='3.2.1'
schemas=document['components']['schemas']
refs=[]
def visit(value):
    if isinstance(value,dict):
        if '$ref' in value:
            ref=value['$ref'];assert ref.startswith('#/'),ref
            current=document
            for part in ref[2:].split('/'):
                current=current[part.replace('~1','/').replace('~0','~')]
            refs.append(ref)
        for v in value.values():visit(v)
    elif isinstance(value,list):
        for v in value:visit(v)
visit(document)
for schema in schemas.values():Draft202012Validator.check_schema(schema)
registry=Registry().with_resource('urn:nb1:openapi',Resource(contents=document,specification=DRAFT202012))
validators={name:Draft202012Validator({'$ref':'urn:nb1:openapi#/components/schemas/'+name},registry=registry,format_checker=Draft202012Validator.FORMAT_CHECKER) for name in schemas}
results=[]
def check(name,schema,value,expected=True,source=''):
    errors=list(validators[schema].iter_errors(value))
    accepted=not errors
    if accepted!=expected:
        raise AssertionError(f'{name}: {schema} accepted={accepted}, expected={expected}; '+str(errors[0])[:1800] if errors else name+' unexpectedly passed')
    results.append(dict(name=name,schema=schema,accepted=accepted,expected=expected,source=source))
samples=json.loads((HERE/'actual-samples.json').read_text())
for sample in samples:
    assert sample['source']['base_commit']=='4d625891cc421c037807e2e6a193174016c05c71'
    check(sample['name'],sample['schema'],sample['value'],source=sample['source']['kind'])
fixture=WORK/'internal/httpapi/testdata/pattern-v2'
mapping={
 'requests/preview-orbit-red-blue.json':'PatternAuthorPreviewRequestV2',
 'requests/preview-pulse-lime.json':'PatternAuthorPreviewRequestV2',
 'requests/save-orbit-red-blue.json':'PatternAuthorSaveRequestV2',
 'requests/save-pulse-lime.json':'PatternAuthorSaveRequestV2',
 'requests/import-known.snapshot.json':'PatternSnapshotV2Write',
 'responses/list-empty.json':'PatternListV2','responses/list-ready.json':'PatternListV2','responses/list-read-only.json':'PatternListV2',
 'responses/get-orbit-red-blue.json':'PatternRecordResponseV2','responses/get-pulse-lime.json':'PatternRecordResponseV2','responses/get-unknown-recipe.json':'PatternRecordResponseV2',
 'responses/preview-orbit-red-blue.json':'PatternPreviewResponseV2','responses/preview-pulse-lime.json':'PatternPreviewResponseV2',
 'responses/save-created.json':'PatternWriteResponseV2','responses/save-existing.json':'PatternWriteResponseV2','responses/import-created.json':'PatternWriteResponseV2',
 'responses/export-known.snapshot.json':'PatternSnapshotV2Read','responses/export-read-only.snapshot.json':'PatternSnapshotV2Read','responses/export-single-unknown.snapshot.json':'PatternSnapshotV2Read',
}
for file,schema in mapping.items():check(file,schema,json.loads((fixture/file).read_text()),source='existing P3 HTTP test golden')
for file in (fixture/'responses/errors').glob('*.json'):check(file.name,'Error',json.loads(file.read_text()),source='existing P3 error golden')
for file in ('save-missing-revision.json','save-forbidden-execution.json'):
    check(file,'PatternAuthorSaveRequestV2',json.loads((fixture/'requests'/file).read_text()),False,'existing runtime-negative golden')
check('preview rejects serial','PatternAuthorPreviewRequestV2',json.loads((fixture/'requests/preview-forbidden-serial.json').read_text()),False)
unknown=json.loads((fixture/'responses/export-read-only.snapshot.json').read_text())
check('unknown readable not writable','PatternSnapshotV2Write',unknown,False)
frame=next(copy.deepcopy(x['value']) for x in samples if x['schema']=='FrameProgramRequest')
for field,value in [('repeat',0),('on_finish','')]:
    bad=copy.deepcopy(frame);bad[field]=value
    check('HTTP Frame rejects explicit '+field,'FrameProgramRequest',bad,False)
# Deliberate schema/runtime gaps: standard mathematical integer and pure SHA
# string shape are insufficient. These accepted examples are NOT runtime-valid.
gap=json.loads((fixture/'requests/save-revision-mismatch.json').read_text())
check('runtime-only expected revision mismatch','PatternAuthorSaveRequestV2',gap,True,'runtime rejects409 despite schema acceptance')
gap=json.loads((fixture/'requests/preview-fractional-time.json').read_text())
check('runtime-only fractional integer lexeme','PatternAuthorPreviewRequestV2',gap,True,'runtime rejects original1.0 token despite mathematical integer')
operations={}
aliases=[]
for path,item in document['paths'].items():
    for method,operation in item.items():
        if method not in ('get','post','put','patch','delete','head','options'):continue
        assert operation.get('operationId') and operation.get('x-availability'),path
        key=method.upper()+' '+path
        assert operation['operationId'] not in [op['operationId'] for op in operations.values()]
        operations[key]=operation
        if operation.get('x-alias-of'):aliases.append(key)
        params=item.get('parameters',[])+operation.get('parameters',[])
        placeholders=set(re.findall(r'{([^}]+)}',path))
        assert placeholders=={p['name'] for p in params if p['in']=='path'},(key,placeholders,params)
inventory=json.loads((HERE/'route-inventory.json').read_text())
shape=lambda key:re.sub(r'\{[^}]+\}','{}',key)
expected={r['method']+' '+r['path'] for r in inventory['routes']}
assert {shape(k) for k in set(operations)-set(aliases)}=={shape(k) for k in expected}
assert len(operations)==40 and len(expected)==37 and len(aliases)==3
assert not any('/run' in p or '/query' in p or '/state' in p or '/set' in p for p in document['paths'])
for key,operation in operations.items():
    if key.split(' ',1)[1].startswith('/v2/patterns'):
        assert operation['x-availability']['daily']['status']=='not_implemented'
        assert operation['x-no-execution'] is True
        assert operation['x-availability']['development']['status']==('implemented' if key=='POST /v2/patterns/preview' else 'requires_explicit_store_injection')
report={'status':'PASS_OPENAPI_DOCUMENTATION_CHECK','openapi':document['openapi'],'jsonschema':importlib.metadata.version('jsonschema'),
        'paths':len(document['paths']),'operations':len(operations),'canonical_operations':len(expected),'id_alias_operations':aliases,
        'schemas':len(schemas),'local_refs_checked':len(refs),'examples_checked':len(results),'checks':results,
        'openapi_sha256':hashlib.sha256((WORK/'docs/openapi.json').read_bytes()).hexdigest(),
        'network_access':False,'hardware_access':False,'limitations':['Not complete OpenAPI metaschema certification','JSON Schema is not a source/hash/lexical verifier; explicit runtime-only gap samples remain accepted by design']}
if args.report:args.report.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k!='checks'},ensure_ascii=False))
