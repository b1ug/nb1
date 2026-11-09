# Pure timing-report strings/DTOs only. No NativeOps, account/profile/process,
# PowerShell bridge, COM, task, permission, service or USB operations.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. "$PSScriptRoot/windows-token-native.ps1" -LibraryOnly

$script:TimingSID = 'S-1-5-21-11-22-33-1001'
$script:TimingTime = '2026-11-09T06:11:12.123456789Z'
$specs = New-Object Collections.ArrayList

function New-Nb1FakeTimingSample([string]$Label) {
    return [ordered]@{
        label = $Label; identity_ms = 1; trust_check_ms = 12; trust_checks = 1
        bridge_ms = 4500; parse_ms = 0; bridge_invoked = $true; run_returned = $true
        private_ps_exit_proven = $true; response_received = $true; error_kind = 'absence_unverified'
        selected_status = [ordered]@{
            schema_version = 1; checked_at = $script:TimingTime; task_path = ('\B1UG-nb1-' + $script:TimingSID)
            presence = 'unknown'; ownership = 'not_checked'
            failure = [ordered]@{ kind = 'not_found_unverified'; stage = 'get_task'; hresult = '0x80070002' }
        }
    }
}

function New-Nb1FakeTimingDTO {
    return [ordered]@{
        schema_version = 1; mode = 'DIAGNOSTIC_ONLY'; status = 'DIAGNOSTIC_ONLY'; phase = 'complete'
        current_sid = $script:TimingSID; task_path = ('\B1UG-nb1-' + $script:TimingSID)
        goarch = '386'; go_version = 'go1.27.3'; source_revision = 'not_embedded'
        production_query_invoked = $false; production_acceptance = $false
        identity_ms = 1; constructor_ms = 15
        samples = @((New-Nb1FakeTimingSample 'first'), (New-Nb1FakeTimingSample 'second'))
    }
}

function Add-Nb1TimingJson([string]$Name, [string]$Json, [bool]$Accept, [string]$ExpectedStatus = 'DIAGNOSTIC_ONLY', [string]$ExpectedTime = $script:TimingTime, [string]$SID = $script:TimingSID) {
    [void]$specs.Add([ordered]@{ name = $Name; json = $Json; accept = $Accept; status = $ExpectedStatus; time = $ExpectedTime; sid = $SID })
}

function Add-Nb1TimingMutation([string]$Name, [scriptblock]$Change, [bool]$Accept = $false, [string]$ExpectedStatus = 'DIAGNOSTIC_ONLY') {
    $value = New-Nb1FakeTimingDTO
    & $Change $value
    Add-Nb1TimingJson $Name ($value | ConvertTo-Json -Depth 16 -Compress) $Accept $ExpectedStatus
}

$base = (New-Nb1FakeTimingDTO) | ConvertTo-Json -Depth 16 -Compress
Add-Nb1TimingJson 'complete-first-second-observations' $base $true
Add-Nb1TimingJson 'whitespace' ("`r`n " + $base + "`t") $true
Add-Nb1TimingJson 'escaped-required-key' ($base.Replace('"mode":', '"\u006dode":')) $true
Add-Nb1TimingJson 'escaped-sample-label-key' ($base.Replace('"label":', '"\u006cabel":')) $true
Add-Nb1TimingJson 'escaped-hresult-key' ($base.Replace('"hresult":', '"\u0068result":')) $true
Add-Nb1TimingMutation 'amd64-metadata' { param($v) $v.goarch = 'amd64' } $true
Add-Nb1TimingMutation 'arm64-metadata-not-native-proof' { param($v) $v.goarch = 'arm64' } $true
Add-Nb1TimingMutation 'embedded-vcs-source' { param($v) $v.source_revision = ('a' * 40) } $true
Add-Nb1TimingMutation 'longer-than-production-budget-is-diagnostic' { param($v) $v.samples[0].bridge_ms = 15000; $v.samples[1].bridge_ms = 350 } $true

$failed = New-Nb1FakeTimingDTO
$failed.status = 'DIAGNOSTIC_FAILED'; $failed['error_kind'] = 'sample_contract_failed'
$failed.samples[0].error_kind = 'deadline'; $failed.samples[0].response_received = $false
$failed.samples[0].selected_status.failure = [ordered]@{ kind = 'cancelled'; stage = 'bridge' }
Add-Nb1TimingJson 'complete-failed-deadline-still-only-exit-proof' ($failed | ConvertTo-Json -Depth 16 -Compress) $true 'DIAGNOSTIC_FAILED'

foreach ($digits in 0..9) {
    $fraction = ''
    if ($digits -gt 0) { $fraction = '.' + '123456789'.Substring(0, $digits) }
    $time = '2026-11-09T06:11:12' + $fraction + 'Z'
    $value = New-Nb1FakeTimingDTO
    foreach ($sample in $value.samples) { $sample.selected_status.checked_at = $time }
    Add-Nb1TimingJson ('timestamp-fraction-' + $digits) ($value | ConvertTo-Json -Depth 16 -Compress) $true 'DIAGNOSTIC_ONLY' $time
}

foreach ($key in @('schema_version','mode','status','phase','current_sid','task_path','goarch','go_version','source_revision','production_query_invoked','production_acceptance','identity_ms','constructor_ms','samples')) {
    $value = New-Nb1FakeTimingDTO; $value.Remove($key)
    Add-Nb1TimingJson ('missing-top-' + $key) ($value | ConvertTo-Json -Depth 16 -Compress) $false
}
foreach ($key in @('label','identity_ms','trust_check_ms','trust_checks','bridge_ms','parse_ms','bridge_invoked','run_returned','private_ps_exit_proven','response_received','error_kind','selected_status')) {
    $value = New-Nb1FakeTimingDTO; $value.samples[0].Remove($key)
    Add-Nb1TimingJson ('missing-first-' + $key) ($value | ConvertTo-Json -Depth 16 -Compress) $false
}
foreach ($phase in @('configuration','identity','constructor','runner','sample_identity','sample_run','sample_parse','report')) {
    $value = New-Nb1FakeTimingDTO
    $value.status = 'DIAGNOSTIC_FAILED'; $value.phase = $phase; $value['error_kind'] = 'sample_contract_failed'
    Add-Nb1TimingJson ('partial-phase-' + $phase) ($value | ConvertTo-Json -Depth 16 -Compress) $false
}

Add-Nb1TimingMutation 'production-query-invoked' { param($v) $v.production_query_invoked = $true }
Add-Nb1TimingMutation 'production-acceptance-claimed' { param($v) $v.production_acceptance = $true }
Add-Nb1TimingMutation 'production-flag-number' { param($v) $v.production_acceptance = 0 }
Add-Nb1TimingMutation 'production-flag-string' { param($v) $v.production_query_invoked = 'false' }
Add-Nb1TimingMutation 'mode-product-pass' { param($v) $v.mode = 'PASS' }
Add-Nb1TimingMutation 'status-product-pass' { param($v) $v.status = 'PASS' }
Add-Nb1TimingMutation 'only-status-with-error' { param($v) $v['error_kind'] = 'sample_contract_failed' }
Add-Nb1TimingMutation 'failed-missing-error-kind' { param($v) $v.status = 'DIAGNOSTIC_FAILED' }
Add-Nb1TimingMutation 'failed-unmodeled-error-kind' { param($v) $v.status = 'DIAGNOSTIC_FAILED'; $v['error_kind'] = 'private-raw-error' }
Add-Nb1TimingMutation 'schema-bool' { param($v) $v.schema_version = $true }
Add-Nb1TimingMutation 'schema-other' { param($v) $v.schema_version = 2 }
Add-Nb1TimingMutation 'identity-ms-negative' { param($v) $v.identity_ms = -1 }
Add-Nb1TimingMutation 'constructor-ms-string' { param($v) $v.constructor_ms = '15' }
Add-Nb1TimingMutation 'current-sid-other' { param($v) $v.current_sid = 'S-1-5-21-11-22-33-1002' }
Add-Nb1TimingMutation 'task-path-other' { param($v) $v.task_path = '\B1UG-nb1-S-1-5-21-11-22-33-1002' }
Add-Nb1TimingMutation 'arch-unknown' { param($v) $v.goarch = 'mips' }
Add-Nb1TimingMutation 'source-unverified-text' { param($v) $v.source_revision = 'unknown-source' }
Add-Nb1TimingMutation 'source-short' { param($v) $v.source_revision = ('a' * 39) }
Add-Nb1TimingMutation 'unknown-top' { param($v) $v['environment'] = @{ secret = 'must-not-be-retained' } }
Add-Nb1TimingMutation 'samples-null' { param($v) $v.samples = $null }
Add-Nb1TimingMutation 'samples-empty' { param($v) $v.samples = @() }
Add-Nb1TimingMutation 'samples-half' { param($v) $v.samples = @($v.samples[0]) }
Add-Nb1TimingMutation 'samples-extra' { param($v) $v.samples += New-Nb1FakeTimingSample 'third' }
Add-Nb1TimingMutation 'samples-object' { param($v) $v.samples = $v.samples[0] }
Add-Nb1TimingMutation 'label-swap' { param($v) $v.samples[0].label = 'second'; $v.samples[1].label = 'first' }

foreach ($index in 0..1) {
    foreach ($key in @('bridge_invoked','run_returned','private_ps_exit_proven','response_received')) {
        $value = New-Nb1FakeTimingDTO; $value.samples[$index][$key] = $false
        Add-Nb1TimingJson ('sample-' + $index + '-' + $key + '-false') ($value | ConvertTo-Json -Depth 16 -Compress) $false
        $value = New-Nb1FakeTimingDTO; $value.samples[$index][$key] = 1
        Add-Nb1TimingJson ('sample-' + $index + '-' + $key + '-numeric') ($value | ConvertTo-Json -Depth 16 -Compress) $false
    }
    foreach ($key in @('identity_ms','trust_check_ms','bridge_ms','parse_ms')) {
        $value = New-Nb1FakeTimingDTO; $value.samples[$index][$key] = -1
        Add-Nb1TimingJson ('sample-' + $index + '-' + $key + '-negative') ($value | ConvertTo-Json -Depth 16 -Compress) $false
        $value = New-Nb1FakeTimingDTO; $value.samples[$index][$key] = '1'
        Add-Nb1TimingJson ('sample-' + $index + '-' + $key + '-string') ($value | ConvertTo-Json -Depth 16 -Compress) $false
    }
    foreach ($count in 0,2) {
        $value = New-Nb1FakeTimingDTO; $value.samples[$index].trust_checks = $count
        Add-Nb1TimingJson ('sample-' + $index + '-trust-count-' + $count) ($value | ConvertTo-Json -Depth 16 -Compress) $false
    }
    $value = New-Nb1FakeTimingDTO; $value.samples[$index]['ps_pid'] = 123
    Add-Nb1TimingJson ('sample-' + $index + '-unknown-field') ($value | ConvertTo-Json -Depth 16 -Compress) $false
    $value = New-Nb1FakeTimingDTO; $value.samples[$index].error_kind = 'deadline'
    Add-Nb1TimingJson ('sample-' + $index + '-only-mode-mismatched-outcome') ($value | ConvertTo-Json -Depth 16 -Compress) $false
    $value = New-Nb1FakeTimingDTO; $value.samples[$index].selected_status.task_path = '\different-task'
    Add-Nb1TimingJson ('sample-' + $index + '-wrong-selected-path') ($value | ConvertTo-Json -Depth 16 -Compress) $false
    $value = New-Nb1FakeTimingDTO; $value.samples[$index].selected_status.presence = 'absent'
    Add-Nb1TimingJson ('sample-' + $index + '-absence-claim') ($value | ConvertTo-Json -Depth 16 -Compress) $false
    $value = New-Nb1FakeTimingDTO; $value.samples[$index].selected_status.ownership = 'managed'
    Add-Nb1TimingJson ('sample-' + $index + '-ownership-claim') ($value | ConvertTo-Json -Depth 16 -Compress) $false
    $value = New-Nb1FakeTimingDTO; $value.samples[$index].selected_status['task'] = $null
    Add-Nb1TimingJson ('sample-' + $index + '-unexpected-task') ($value | ConvertTo-Json -Depth 16 -Compress) $false
    foreach ($time in @('2026-02-30T06:11:12Z','2026-11-09T06:11:12.1234567890Z','0001-01-01T00:00:00Z')) {
        $value = New-Nb1FakeTimingDTO; $value.samples[$index].selected_status.checked_at = $time
        Add-Nb1TimingJson ('sample-' + $index + '-invalid-time-' + $specs.Count) ($value | ConvertTo-Json -Depth 16 -Compress) $false
    }
}

Add-Nb1TimingJson 'duplicate-top-same' ($base.Replace('"mode":"DIAGNOSTIC_ONLY"', '"mode":"DIAGNOSTIC_ONLY","mode":"DIAGNOSTIC_ONLY"')) $false
Add-Nb1TimingJson 'duplicate-top-escaped' ($base.Replace('"mode":"DIAGNOSTIC_ONLY"', '"mode":"DIAGNOSTIC_ONLY","\u006dode":"DIAGNOSTIC_ONLY"')) $false
Add-Nb1TimingJson 'case-collision-top' ($base.Replace('"mode":"DIAGNOSTIC_ONLY"', '"Mode":"DIAGNOSTIC_ONLY","mode":"DIAGNOSTIC_ONLY"')) $false
Add-Nb1TimingJson 'duplicate-sample-escaped' ($base.Replace('"label":"first"', '"label":"first","\u006cabel":"first"')) $false
Add-Nb1TimingJson 'case-collision-sample' ($base.Replace('"label":"first"', '"Label":"first","label":"first"')) $false
Add-Nb1TimingJson 'duplicate-sample-proof' ($base.Replace('"run_returned":true', '"run_returned":false,"run_returned":true')) $false
Add-Nb1TimingJson 'duplicate-selected-escaped' ($base.Replace('"presence":"unknown"', '"presence":"unknown","\u0070resence":"unknown"')) $false
Add-Nb1TimingJson 'duplicate-hresult-escaped' ($base.Replace('"hresult":"0x80070002"', '"hresult":"0x80070002","\u0068result":"0x80070002"')) $false
Add-Nb1TimingJson 'schema-decimal' ($base.Replace('"schema_version":1', '"schema_version":1.0')) $false
Add-Nb1TimingJson 'schema-exponent' ($base.Replace('"schema_version":1', '"schema_version":1e0')) $false
Add-Nb1TimingJson 'expected-sid-mismatch' $base $false 'DIAGNOSTIC_ONLY' $script:TimingTime 'S-1-5-21-11-22-33-1002'
foreach ($bad in @('', 'null', '[]', '{}', ($base + $base), ($base + 'null'), (' ' * 8193 + $base))) {
    Add-Nb1TimingJson ('malformed-or-bounded-json-' + $specs.Count) $bad $false
}

$results = @()
foreach ($spec in $specs) {
    try {
        $rejected = $false; $returned = @()
        try { $returned = @(Assert-Nb1TimingReport $spec.json $spec.sid) } catch { $rejected = $true }
        if ($spec.accept) {
            if ($rejected -or $returned.Count -ne 1) { throw 'valid timing DTO did not return exactly one item' }
            $value = $returned[0]
            if ($value.mode -cne 'DIAGNOSTIC_ONLY' -or $value.status -cne $spec.status -or $value.phase -cne 'complete' -or $value.current_sid -cne $script:TimingSID -or $value.production_query_invoked -or $value.production_acceptance -or $value.samples.Count -ne 2) { throw 'timing report changed mode, identity or production acceptance' }
            foreach ($sample in $value.samples) {
                if (-not $sample.bridge_invoked -or -not $sample.run_returned -or -not $sample.private_ps_exit_proven -or $sample.selected_status.checked_at -isnot [string] -or $sample.selected_status.checked_at -cne $spec.time) { throw 'timing report changed terminal proof or timestamp precision' }
            }
        } elseif (-not $rejected) { throw 'unsafe or partial timing DTO was accepted' }
        $results += [ordered]@{ case = $spec.name; status = 'PASS'; expected_acceptance = $spec.accept }
    } catch {
        $results += [ordered]@{ case = $spec.name; status = 'FAIL'; expected_acceptance = $spec.accept; error_kind = 'timing-dto-contract' }
    }
}
foreach ($case in @('complete-observation','complete-failed-diagnostic','invalid-report','partial-report','observation-exit-mismatch','failed-exit-mismatch','original-query-still-unproven')) {
    try {
        $state = @{ DiagnosticsAttempted = $false; DiagnosticsCompleted = $true; QueryAttempted = $false; QueryCompleted = $false }
        Start-Nb1DiagnosticsAttempt $state
        if (-not $state.DiagnosticsAttempted -or $state.DiagnosticsCompleted) { throw 'diagnostic intent did not clear old completion' }
        $json = $base; $exitCode = 0; $wantComplete = $true; $wantThrow = $false
        if ($case -eq 'complete-failed-diagnostic' -or $case -eq 'failed-exit-mismatch') { $json = $failed | ConvertTo-Json -Depth 16 -Compress; $exitCode = 1; $wantThrow = $true }
        if ($case -eq 'invalid-report') { $json = '{}'; $wantComplete = $false; $wantThrow = $true }
        if ($case -eq 'partial-report') { $json = $base.Replace('"phase":"complete"', '"phase":"sample_run"'); $wantComplete = $false; $wantThrow = $true }
        if ($case -eq 'observation-exit-mismatch') { $exitCode = 1; $wantComplete = $false; $wantThrow = $true }
        if ($case -eq 'failed-exit-mismatch') { $exitCode = 0; $wantComplete = $false }
        if ($case -eq 'original-query-still-unproven') { $state.QueryAttempted = $true }
        $record = @{}; $threw = $false
        try { Complete-Nb1DiagnosticsRecord $state $record $exitCode $json $script:TimingSID } catch { $threw = $true }
        if ($state.DiagnosticsCompleted -ne $wantComplete -or $threw -ne $wantThrow -or $state.QueryCompleted) { throw 'completion or exit-code policy changed' }
        $cleanupRejected = $false
        try { Assert-Nb1QueryExitProof $state } catch { $cleanupRejected = $true }
        if ($cleanupRejected -ne (-not $wantComplete -or $case -eq 'original-query-still-unproven')) { throw 'independent diagnostic cleanup proof was not enforced' }
        $results += [ordered]@{ case = ('completion-' + $case); status = 'PASS'; expected_acceptance = $wantComplete }
    } catch { $results += [ordered]@{ case = ('completion-' + $case); status = 'FAIL'; error_kind = 'timing-completion-contract' } }
}
$failures = @($results | Where-Object { $_.status -cne 'PASS' }).Count
$report = [ordered]@{ schema_version = 1; status = 'PASS'; case_count = $results.Count; passed = $results.Count - $failures; failed = $failures; cases = $results; scope = 'Pure timing JSON/DTOs only; no NativeOps, account, profile, process, task, permission, service or USB operations.' }
if ($failures -ne 0) { $report.status = 'FAIL' }
$report | ConvertTo-Json -Depth 8
if ($failures -ne 0) { exit 1 }
exit 0
