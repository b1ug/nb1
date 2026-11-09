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
    assert sample['source']['base_commit']=='f8bb267b5cf8b952ce6a8cdbd86e8b18971028ae'
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
assert inventory['baselines']['installed']['commit']=='96a1f92a8a2fcf836129797e4c3a82602a4e7ffc'
assert inventory['baselines']['installed']['readonly_checks']==33
assert all(row['status']['installed']=='implemented' for row in inventory['routes'])
public_inventory=json.loads((WORK/'docs/api-http-inventory.json').read_text())
assert public_inventory['summary']['installed_method_path_count']==44
assert public_inventory['summary']['daily_method_path_count']==32
assert all(row['status']['installed']=='implemented' for row in public_inventory['routes'])
shape=lambda key:re.sub(r'\{[^}]+\}','{}',key)
expected={r['method']+' '+r['path'] for r in inventory['routes']}
assert {shape(k) for k in set(operations)-set(aliases)}=={shape(k) for k in expected}
assert len(operations)==48 and len(expected)==44 and len(aliases)==4
assert not any('/run' in p or '/query' in p or '/state' in p or '/set' in p for p in document['paths'])
for key,operation in operations.items():
    assert operation['x-availability']['development']['source_commit']==('56c4aef153c560c68845e89ebdaa15bc1323eeb2' if key.split(' ',1)[1].startswith('/demo') else 'f8bb267b5cf8b952ce6a8cdbd86e8b18971028ae')
    if key.split(' ',1)[1].startswith('/v2/patterns'):
        assert operation['x-availability']['daily']['status']=='not_implemented'
        assert operation['x-no-execution'] is True
        wanted='implemented' if key in ('POST /v2/patterns/preview','POST /v2/patterns/preview-ref') else 'implemented_with_default_lazy_v2_store'
        assert operation['x-availability']['development']['status']==wanted,(key,wanted)
    if operation.get('operationId') in ('getPatternRequestV2','cancelPatternRequestV2'):
        assert operation['x-store-dependency']=='none including configuration/Path/Resolve'
for name,available in [('pattern_service_configured',True),('pattern_service_unsupported',False),('pattern_library_unconfigured',False),('pattern_library_configuration_error',False)]:
    check('availability '+name,'PatternExecutionAvailabilityV2',{'available':available,'reason':name})
    check('availability wrong bool '+name,'PatternExecutionAvailabilityV2',{'available':not available,'reason':name},False)
check('availability old P3 constant not new contract','PatternExecutionAvailabilityV2',{'available':False,'reason':'pattern_identity_admission_not_integrated'},False)
submit=next(copy.deepcopy(x['value']) for x in samples if x['schema']=='PatternSubmitRequestV2')
for key in ('policy','pattern_ref','request_id','expected_daemon_instance_id'):
    bad=copy.deepcopy(submit);del bad[key];check('P6 missing '+key,'PatternSubmitRequestV2',bad,False)
for key,value in [('program',{}),('execution_digest','sha256:'+'0'*64),('policy',''),('pattern_ref',None)]:
    bad=copy.deepcopy(submit);bad[key]=value;check('P6 forbidden or empty '+key,'PatternSubmitRequestV2',bad,False)
cancel=next(copy.deepcopy(x['value']) for x in samples if x['schema']=='PatternCancelRequestV2')
for value in ('',None,'job_bad'):
    bad=copy.deepcopy(cancel);bad['confirmed_job_id']=value;check('P6 invalid confirmed job '+str(value),'PatternCancelRequestV2',bad,False)
preview=next(copy.deepcopy(x['value']) for x in samples if x['schema']=='PatternRefPreviewV2')
bad=copy.deepcopy(preview);bad['execution']['kind']='frame_program';check('P6 crossed preview union','PatternRefPreviewV2',bad,False)
# A valid SHA spelling does not bind an HTTP response to a source/receipt;
# full source/digest consistency belongs to P5/core/client runtime verification.
bad=copy.deepcopy(preview);bad['execution_digest']='sha256:'+'0'*64
check('P6 runtime-only digest mismatch','PatternRefPreviewV2',bad,True,'schema shape only; runtime/client must recompute and reject')
resources=json.loads((HERE/'resource-observations.json').read_text())
assert resources['host_source']=='56c4aef153c560c68845e89ebdaa15bc1323eeb2'
assert len(resources['observations'])==3
for row in resources['observations']:
    assert row['method']=='GET' and row['path']=='/demo/'+row['asset']
    assert row['status']==404 and row['content_type']=='application/json; charset=utf-8'
    check('historical host56c resource404 '+row['path'],'Error',row['error'],source='historical basic-bundle Recorder; not current96a observation')
    op=operations['GET '+row['path']]
    assert op['x-availability']['development']['status']=='conditional_on_embedded_bundle'
current=json.loads((HERE/'resource-observations.current.json').read_text())
candidate='96a1f92a8a2fcf836129797e4c3a82602a4e7ffc'
assert current['runtime_source']==candidate
manifest_path=WORK/'internal/webui/dist/manifest.json'
manifest=json.loads(manifest_path.read_text())
assert current['web_source']==manifest['source']['commit']=='f90ef8d6143acd55c059b94ecf961ec65a1bb646'
assert current['manifest_sha256']==hashlib.sha256(manifest_path.read_bytes()).hexdigest()
assert len(current['observations'])==3 and len(manifest['files'])==10
assert sum(f['bytes'] for f in manifest['files'])+manifest_path.stat().st_size==208132
for row in current['observations']:
    asset=WORK/'internal/webui/dist'/row['asset']
    assert row['status']==200 and row['method']=='GET' and row['path']=='/demo/'+row['asset']
    assert hashlib.sha256(asset.read_bytes()).hexdigest()==row['body_sha256'] and asset.stat().st_size==row['bytes']
    assert row['headers']['Referrer-Policy']==['no-referrer'] and len(row['headers']['Content-Security-Policy'])==1
    assert operations['GET '+row['path']]['x-availability']['integrated_candidate']['source_commit']==candidate
    results.append(dict(name='current96a extended resource200 '+row['path'],schema='static embedded bytes',accepted=True,expected=True,source='actual no-Service Recorder matches pinned f90 dist; historical56c preserved'))
lexemes=json.loads((HERE/'numeric-lexeme-observations.json').read_text())
assert lexemes['runtime_source']==candidate and len(lexemes['observations'])==8
accepted={'legacy-program-negative-zero','legacy-effect-negative-zero','raw-frame-zero-fade'}
for row in lexemes['observations']:
    assert row['status']==(200 if row['name'] in accepted else 400)
    assert isinstance(row['raw_request'],str) and json.loads(row['raw_request'])
    if row['status']==400:
        assert row['error_code'] and row['error_message_nonempty'] is True and 'response' not in row
    results.append(dict(name='numeric codec contrast '+row['name'],schema='actual raw HTTP codec, not mathematical JSON Schema',accepted=row['status']==200,expected=row['name'] in accepted,source=lexemes['scope']))
assert 'numeric_lexemes' in document['x-runtime-validation']
for name in ('StepRequest','ProgramRequest','SteadyEffect','PulseEffect','FrameTargetRequest','FrameRequest','FrameProgramRequest'):
    assert '-0 becomes0' in schemas[name]['x-integer-request-codec']
report={'status':'PASS_OPENAPI_DOCUMENTATION_CHECK','openapi':document['openapi'],'jsonschema':importlib.metadata.version('jsonschema'),
        'paths':len(document['paths']),'operations':len(operations),'canonical_operations':len(expected),'id_alias_operations':aliases,
        'schemas':len(schemas),'local_refs_checked':len(refs),'examples_checked':len(results),'checks':results,
        'openapi_sha256':hashlib.sha256((WORK/'docs/openapi.json').read_bytes()).hexdigest(),
        'network_access':False,'hardware_access':False,'limitations':['Not complete OpenAPI metaschema certification','JSON Schema is not a source/hash/lexical verifier; explicit runtime-only gap samples remain accepted by design']}
if args.report:args.report.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k!='checks'},ensure_ascii=False))
