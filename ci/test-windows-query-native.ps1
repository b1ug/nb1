# Pure JSON/DTO regression only. Does not call NativeOps, start a process,
# enumerate accounts/tasks/devices, or change profiles, credentials or ACLs.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. "$PSScriptRoot/windows-token-native.ps1" -LibraryOnly

$script:QueryFixtureSID = 'S-1-5-21-11-22-33-1001'
$script:QueryFixtureTime = '2026-11-09T06:11:12.123456789Z'
$specs = New-Object Collections.ArrayList

function New-Nb1FakeQueryDTO {
    return [ordered]@{
        schema_version = 1; status = 'PASS'; phase = 'missing_contract_observed'
        current_sid = $script:QueryFixtureSID; task_path = ('\B1UG-nb1-' + $script:QueryFixtureSID)
        selected_status = [ordered]@{
            schema_version = 1; checked_at = $script:QueryFixtureTime
            task_path = ('\B1UG-nb1-' + $script:QueryFixtureSID)
            presence = 'unknown'; ownership = 'not_checked'
            failure = [ordered]@{ kind = 'not_found_unverified'; stage = 'get_task'; hresult = '0x80070002' }
        }
    }
}

function Add-Nb1QueryJsonCase([string]$Name, [string]$Json, [bool]$Accept, [string]$ExpectedTime = $script:QueryFixtureTime, [string]$ExpectedSID = $script:QueryFixtureSID) {
    [void]$specs.Add([ordered]@{ name = $Name; json = $Json; accept = $Accept; time = $ExpectedTime; sid = $ExpectedSID })
}

function Add-Nb1QueryMutation([string]$Name, [scriptblock]$Change) {
    $value = New-Nb1FakeQueryDTO
    & $Change $value
    Add-Nb1QueryJsonCase $Name ($value | ConvertTo-Json -Depth 12 -Compress) $false
}

$base = (New-Nb1FakeQueryDTO) | ConvertTo-Json -Depth 12 -Compress
Add-Nb1QueryJsonCase 'exact-missing-observation' $base $true
Add-Nb1QueryJsonCase 'valid-json-whitespace' (" `r`n" + $base + "`t ") $true
Add-Nb1QueryJsonCase 'valid-escaped-property-name' ($base.Replace('"schema_version":1', '"\u0073chema_version":1')) $true
Add-Nb1QueryJsonCase 'valid-escaped-status-name' ($base.Replace('"status":"PASS"', '"\u0073tatus":"PASS"')) $true
Add-Nb1QueryJsonCase 'valid-escaped-presence-name' ($base.Replace('"presence":"unknown"', '"\u0070resence":"unknown"')) $true
Add-Nb1QueryJsonCase 'valid-escaped-hresult-name' ($base.Replace('"hresult":"0x80070002"', '"\u0068result":"0x80070002"')) $true
Add-Nb1QueryJsonCase 'valid-escaped-identity-values' ($base.Replace('S-1-5-21-', 'S\u002D1-5-21-')) $true

foreach ($digits in 0..9) {
    $fraction = ''
    if ($digits -gt 0) { $fraction = '.' + '123456789'.Substring(0, $digits) }
    $date = '2026-11-09T06:11:12' + $fraction + 'Z'
    $value = New-Nb1FakeQueryDTO
    $value.selected_status.checked_at = $date
    Add-Nb1QueryJsonCase ('timestamp-fraction-' + $digits) ($value | ConvertTo-Json -Depth 12 -Compress) $true $date
}
$value = New-Nb1FakeQueryDTO
$value.selected_status.checked_at = '2024-02-29T23:59:59.000000001Z'
Add-Nb1QueryJsonCase 'timestamp-valid-leap-day' ($value | ConvertTo-Json -Depth 12 -Compress) $true $value.selected_status.checked_at

foreach ($key in @('schema_version','status','phase','current_sid','task_path','selected_status')) {
    $value = New-Nb1FakeQueryDTO
    $value.Remove($key)
    Add-Nb1QueryJsonCase ('missing-top-' + $key) ($value | ConvertTo-Json -Depth 12 -Compress) $false
}
foreach ($key in @('schema_version','checked_at','task_path','presence','ownership','failure')) {
    $value = New-Nb1FakeQueryDTO
    $value.selected_status.Remove($key)
    Add-Nb1QueryJsonCase ('missing-status-' + $key) ($value | ConvertTo-Json -Depth 12 -Compress) $false
}
foreach ($key in @('kind','stage','hresult')) {
    $value = New-Nb1FakeQueryDTO
    $value.selected_status.failure.Remove($key)
    Add-Nb1QueryJsonCase ('missing-failure-' + $key) ($value | ConvertTo-Json -Depth 12 -Compress) $false
}

Add-Nb1QueryMutation 'unknown-top-field' { param($v) $v['xml'] = 'must-not-be-kept' }
Add-Nb1QueryMutation 'unknown-status-field' { param($v) $v.selected_status['engine_pid'] = 123 }
Add-Nb1QueryMutation 'unknown-failure-field' { param($v) $v.selected_status.failure['message'] = 'must-not-be-kept' }
Add-Nb1QueryMutation 'unexpected-top-error-kind' { param($v) $v['error_kind'] = 'absence_unverified' }
Add-Nb1QueryMutation 'unexpected-top-error-null' { param($v) $v['error_kind'] = $null }
Add-Nb1QueryMutation 'unexpected-task-null' { param($v) $v.selected_status['task'] = $null }
Add-Nb1QueryMutation 'unexpected-task-properties' { param($v) $v.selected_status['task'] = @{ enabled = $true; scheduler_state = 3; scheduler_state_name = 'ready'; last_task_result = 0 } }
Add-Nb1QueryMutation 'top-schema-bool' { param($v) $v.schema_version = $true }
Add-Nb1QueryMutation 'top-schema-string' { param($v) $v.schema_version = '1' }
Add-Nb1QueryMutation 'top-schema-other' { param($v) $v.schema_version = 2 }
Add-Nb1QueryMutation 'selected-schema-bool' { param($v) $v.selected_status.schema_version = $true }
Add-Nb1QueryMutation 'selected-schema-string' { param($v) $v.selected_status.schema_version = '1' }
Add-Nb1QueryMutation 'selected-schema-other' { param($v) $v.selected_status.schema_version = 2 }
Add-Nb1QueryJsonCase 'schema-decimal-json' ($base.Replace('"schema_version":1', '"schema_version":1.0')) $false
Add-Nb1QueryJsonCase 'schema-exponent-json' ($base.Replace('"schema_version":1', '"schema_version":1e0')) $false
Add-Nb1QueryJsonCase 'schema-overflow-json' ($base.Replace('"schema_version":1', '"schema_version":18446744073709551616')) $false
Add-Nb1QueryMutation 'status-lowercase' { param($v) $v.status = 'pass' }
Add-Nb1QueryMutation 'status-failed' { param($v) $v.status = 'FAIL' }
Add-Nb1QueryMutation 'phase-wrong' { param($v) $v.phase = 'complete' }
Add-Nb1QueryMutation 'phase-missing-instead-of-observed' { param($v) $v.phase = 'absent' }
Add-Nb1QueryMutation 'current-sid-other-user' { param($v) $v.current_sid = 'S-1-5-21-11-22-33-1002' }
Add-Nb1QueryMutation 'current-sid-noncanonical-case' { param($v) $v.current_sid = 's-1-5-21-11-22-33-1001' }
Add-Nb1QueryMutation 'current-sid-null' { param($v) $v.current_sid = $null }
Add-Nb1QueryMutation 'current-sid-array' { param($v) $v.current_sid = @($script:QueryFixtureSID) }
Add-Nb1QueryMutation 'task-path-other-user' { param($v) $v.task_path = '\B1UG-nb1-S-1-5-21-11-22-33-1002' }
Add-Nb1QueryMutation 'task-path-case-changed' { param($v) $v.task_path = '\b1ug-nb1-' + $script:QueryFixtureSID }
Add-Nb1QueryMutation 'task-path-arbitrary' { param($v) $v.task_path = '\different-task' }
Add-Nb1QueryMutation 'selected-path-other-user' { param($v) $v.selected_status.task_path = '\B1UG-nb1-S-1-5-21-11-22-33-1002' }
Add-Nb1QueryMutation 'selected-status-null' { param($v) $v.selected_status = $null }
Add-Nb1QueryMutation 'selected-status-array' { param($v) $v.selected_status = @($v.selected_status) }
Add-Nb1QueryMutation 'selected-status-string' { param($v) $v.selected_status = 'unknown' }
Add-Nb1QueryMutation 'presence-absent' { param($v) $v.selected_status.presence = 'absent' }
Add-Nb1QueryMutation 'presence-present' { param($v) $v.selected_status.presence = 'present' }
Add-Nb1QueryMutation 'presence-bool' { param($v) $v.selected_status.presence = $true }
Add-Nb1QueryMutation 'ownership-managed' { param($v) $v.selected_status.ownership = 'managed' }
Add-Nb1QueryMutation 'ownership-unchecked-case' { param($v) $v.selected_status.ownership = 'NOT_CHECKED' }
Add-Nb1QueryMutation 'failure-null' { param($v) $v.selected_status.failure = $null }
Add-Nb1QueryMutation 'failure-string' { param($v) $v.selected_status.failure = 'not_found_unverified' }
Add-Nb1QueryMutation 'failure-kind-native' { param($v) $v.selected_status.failure.kind = 'native' }
Add-Nb1QueryMutation 'failure-stage-connect' { param($v) $v.selected_status.failure.stage = 'connect' }
Add-Nb1QueryMutation 'failure-stage-properties' { param($v) $v.selected_status.failure.stage = 'properties' }
Add-Nb1QueryMutation 'failure-hresult-access-denied' { param($v) $v.selected_status.failure.hresult = '0x80070005' }
Add-Nb1QueryMutation 'failure-hresult-file-not-found-number' { param($v) $v.selected_status.failure.hresult = 2147942402 }
Add-Nb1QueryMutation 'failure-hresult-case-changed' { param($v) $v.selected_status.failure.hresult = '0X80070002' }
Add-Nb1QueryJsonCase 'expected-sid-mismatch' $base $false $script:QueryFixtureTime 'S-1-5-21-11-22-33-1002'
Add-Nb1QueryJsonCase 'expected-sid-empty' $base $false $script:QueryFixtureTime ''

foreach ($time in @('', '0001-01-01T00:00:00Z', '2023-02-29T06:11:12Z', '2026-02-30T06:11:12Z', '2026-13-09T06:11:12Z', '2026-11-00T06:11:12Z', '2026-11-09T24:11:12Z', '2026-11-09T06:60:12Z', '2026-11-09T06:11:60Z', '2026-11-09T06:11:12.1234567890Z', '2026-11-09T06:11:12.Z', '2026-11-09 06:11:12Z', '2026-11-09T06:11:12', '2026-11-09T06:11:12+00:00', '2026-11-09T06:11:12+01:00')) {
    $value = New-Nb1FakeQueryDTO
    $value.selected_status.checked_at = $time
    Add-Nb1QueryJsonCase ('invalid-timestamp-' + $specs.Count) ($value | ConvertTo-Json -Depth 12 -Compress) $false
}
Add-Nb1QueryMutation 'timestamp-null' { param($v) $v.selected_status.checked_at = $null }
Add-Nb1QueryMutation 'timestamp-number' { param($v) $v.selected_status.checked_at = 123 }
Add-Nb1QueryMutation 'timestamp-array' { param($v) $v.selected_status.checked_at = @($script:QueryFixtureTime) }

foreach ($stage in @('configuration','identity','constructor','query','missing_contract')) {
    $value = New-Nb1FakeQueryDTO
    $value.status = 'FAIL'; $value.phase = $stage; $value['error_kind'] = 'trust'
    Add-Nb1QueryJsonCase ('explicit-fail-phase-' + $stage) ($value | ConvertTo-Json -Depth 12 -Compress) $false
}
foreach ($kind in @('current_identity_rejected','trust','constructor_nil','constructor_identity_mismatch','transport','protocol','native','deadline','cancelled','unexpected_query_success','missing_contract_mismatch')) {
    $value = New-Nb1FakeQueryDTO
    $value.status = 'FAIL'; $value.phase = 'query'; $value['error_kind'] = $kind
    Add-Nb1QueryJsonCase ('failure-cannot-be-absence-' + $kind) ($value | ConvertTo-Json -Depth 12 -Compress) $false
}

Add-Nb1QueryJsonCase 'duplicate-top-same' ($base.Replace('"status":"PASS"', '"status":"PASS","status":"PASS"')) $false
Add-Nb1QueryJsonCase 'duplicate-top-shadow-fail' ($base.Replace('"status":"PASS"', '"status":"FAIL","status":"PASS"')) $false
Add-Nb1QueryJsonCase 'duplicate-top-escaped-name' ($base.Replace('"status":"PASS"', '"status":"PASS","\u0073tatus":"PASS"')) $false
Add-Nb1QueryJsonCase 'case-collision-top' ($base.Replace('"status":"PASS"', '"Status":"PASS","status":"PASS"')) $false
Add-Nb1QueryJsonCase 'case-collision-top-escaped-name' ($base.Replace('"status":"PASS"', '"\u0053tatus":"PASS","status":"PASS"')) $false
Add-Nb1QueryJsonCase 'case-collision-selected-name' ($base.Replace('"presence":"unknown"', '"Presence":"unknown","presence":"unknown"')) $false
Add-Nb1QueryJsonCase 'duplicate-selected-same' ($base.Replace('"presence":"unknown"', '"presence":"unknown","presence":"unknown"')) $false
Add-Nb1QueryJsonCase 'duplicate-selected-shadow-absent' ($base.Replace('"presence":"unknown"', '"presence":"absent","presence":"unknown"')) $false
Add-Nb1QueryJsonCase 'duplicate-selected-escaped-name' ($base.Replace('"presence":"unknown"', '"presence":"unknown","\u0070resence":"unknown"')) $false
Add-Nb1QueryJsonCase 'duplicate-failure-same' ($base.Replace('"hresult":"0x80070002"', '"hresult":"0x80070002","hresult":"0x80070002"')) $false
Add-Nb1QueryJsonCase 'duplicate-failure-escaped-name' ($base.Replace('"hresult":"0x80070002"', '"hresult":"0x80070002","\u0068result":"0x80070002"')) $false
Add-Nb1QueryJsonCase 'duplicate-failure-shadow-denied' ($base.Replace('"hresult":"0x80070002"', '"hresult":"0x80070005","hresult":"0x80070002"')) $false
Add-Nb1QueryJsonCase 'case-collision-failure' ($base.Replace('"hresult":"0x80070002"', '"HRESULT":"0x80070002","hresult":"0x80070002"')) $false
Add-Nb1QueryJsonCase 'duplicate-schema-nested' ($base.Replace('"schema_version":1', '"schema_version":1,"schema_version":1')) $false

foreach ($invalid in @('', ' ', 'null', '[]', 'true', '"text"', '{', ($base + $base), ($base + 'garbage'), (' ' * 2049 + $base), ('{"unknown":' + '[' * 80 + '0' + ']' * 80 + '}'))) {
    Add-Nb1QueryJsonCase ('malformed-or-bounded-json-' + $specs.Count) $invalid $false
}

$results = @()
foreach ($spec in $specs) {
    try {
        $rejected = $false
        $returned = @()
        try { $returned = @(Assert-Nb1QueryReport $spec.json $spec.sid) } catch { $rejected = $true }
        if ($spec.accept) {
            if ($rejected -or $returned.Count -ne 1) { throw 'valid DTO must return exactly one decoded item' }
            $value = $returned[0]
            if ($value.status -cne 'PASS' -or $value.phase -cne 'missing_contract_observed' -or $value.current_sid -cne $script:QueryFixtureSID -or $value.task_path -cne ('\B1UG-nb1-' + $script:QueryFixtureSID) -or $value.selected_status.presence -cne 'unknown' -or $value.selected_status.ownership -cne 'not_checked' -or $value.selected_status.checked_at -isnot [string] -or $value.selected_status.checked_at -cne $spec.time -or $value.selected_status.failure.stage -cne 'get_task' -or $value.selected_status.failure.hresult -cne '0x80070002') { throw 'decoded DTO changed identity, time or unknown-missing semantics' }
        } elseif (-not $rejected) { throw 'invalid or failed DTO was accepted as a missing observation' }
        $results += [ordered]@{ case = $spec.name; status = 'PASS'; expected_acceptance = $spec.accept }
    } catch {
        $results += [ordered]@{ case = $spec.name; status = 'FAIL'; expected_acceptance = $spec.accept; error_kind = 'query-dto-contract' }
    }
}
$failed = @($results | Where-Object { $_.status -cne 'PASS' }).Count
$report = [ordered]@{
    schema_version = 1; status = 'PASS'; case_count = $results.Count
    passed = $results.Count - $failed; failed = $failed; cases = $results
    scope = 'Pure string/DTO regressions only; no NativeOps, account, profile, process, task, permission, service or USB operations.'
}
if ($failed -ne 0) { $report.status = 'FAIL' }
$report | ConvertTo-Json -Depth 8
if ($failed -ne 0) { exit 1 }
exit 0
