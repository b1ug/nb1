# Pure fake lifecycle regression. No NativeOps, accounts, processes, registry,
# profiles, permissions, Task Scheduler or system configuration are accessed.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

. "$PSScriptRoot/windows-token-native.ps1" -LibraryOnly

function Assert-Nb1Fake {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw ("fake assertion: " + $Message) }
}

function New-Nb1FakeOps {
    return @{
        Preflight = {
            param($State, $Report)
            [void]$State.Events.Add('Preflight')
            if ($State.Case -eq 'preflight-failure') { throw 'fake preflight unknown' }
        }
        AdministratorCase = {
            param($State, $Report)
            [void]$State.Events.Add('AdministratorCase')
            if ($State.Case -eq 'administrator-failure') { throw 'fake administrator rejection not proven' }
        }
        CreateUser = {
            param($State, $Report)
            [void]$State.Events.Add('CreateUser')
            Assert-Nb1Fake ($State.CreationAttempted -eq $true) 'creation intent must precede create'
            Assert-Nb1Fake ($State.CreationReturned -eq $false) 'create has not returned yet'
            $State.FakeUserExists = $true
            $State.Marker = 'this-fake-run'
            if ($State.Case -eq 'foreign-marker') { $State.Marker = 'foreign-fake-run' }
            if ($State.Case -eq 'partial-create') { throw 'fake user created before API error' }
        }
        ClaimUser = {
            param($State, $Report)
            [void]$State.Events.Add('ClaimUser')
            $State.ClaimCalls++
            if ($State.ChildrenExited) { $State.CleanupClaimCalls++ }
            if ($State.Case -eq 'claim-exception') { throw 'fake attribution query failed' }
            if ($State.CleanupClaimCalls -eq 2 -and $State.Case -eq 'claim-second-exception') {
                throw 'fake attribution query failed before removal'
            }
            if ($State.CleanupClaimCalls -eq 2 -and $State.Case -eq 'claim-changed-before-remove') {
                $State.Marker = 'foreign-fake-run'
            }
            if ($State.Case -eq 'claim-true-unproven') {
                # A true return without setting the required proof flag is not
                # enough to authorize resource/account removal.
                return $true
            }
            if (-not $State.FakeUserExists -or $State.Marker -ne 'this-fake-run') {
                return $false
            }
            $State.CreationVerified = $true
            return $true
        }
        EnsureGroups = {
            param($State, $Report)
            [void]$State.Events.Add('EnsureGroups')
            Assert-Nb1Fake ($State.CreationReturned -eq $true) 'groups require returned create'
            if ($State.Case -eq 'groups-failure') { throw 'fake group check failed' }
        }
        StageFixture = {
            param($State, $Report)
            [void]$State.Events.Add('StageFixture')
            if ($State.Case -eq 'stage-failure') { throw 'fake fixture staging failed' }
        }
        OrdinaryCase = {
            param($State, $Report)
            [void]$State.Events.Add('OrdinaryCase')
            if ($State.Case -eq 'ordinary-failure') { throw 'fake ordinary token did not qualify' }
        }
        ReapChildren = {
            param($State, $Report)
            [void]$State.Events.Add('ReapChildren')
            if ($State.Case -eq 'unknown-child') { throw 'fake child exit is unknown' }
            if ($State.Case -eq 'child-unproven') { return }
            $State.ChildrenExited = $true
        }
        CleanupResources = {
            param($State, $Report)
            [void]$State.Events.Add('CleanupResources')
            Assert-Nb1Fake ($State.ChildrenExited -eq $true) 'resources require actual child exit proof'
            Assert-Nb1Fake ($State.CreationVerified -eq $true) 'resources require attribution proof'
            Assert-Nb1Fake ($State.Marker -eq 'this-fake-run') 'unknown resources must remain'
            if ($State.Case -eq 'resource-changed') {
                $State.ProfileObservation = @{ sid = 'fresh-fake-sid'; mapping_matches = $true; root_owner = 'different-fake-owner'; root_id = 'same-fake-file-id' }
                throw 'fake reparse or changed profile resource'
            }
            if ($State.Case -eq 'cleanup-failure') { throw 'fake resource cleanup failed' }
            $State.ResourcesGone = $true
        }
        RemoveUser = {
            param($State, $Report)
            [void]$State.Events.Add('RemoveUser')
            Assert-Nb1Fake ($State.ChildrenExited -eq $true) 'remove requires child exit proof'
            Assert-Nb1Fake ($State.CreationVerified -eq $true) 'remove requires attribution proof'
            Assert-Nb1Fake ($State.Marker -eq 'this-fake-run') 'changed account must never reach remove'
            Assert-Nb1Fake ($State.CleanupClaimCalls -eq 2) 'remove requires second cleanup claim'
            Assert-Nb1Fake ($State.ResourcesGone -eq $true) 'remove requires successful resource cleanup'
            if ($State.Case -eq 'remove-failure') { throw 'fake account deletion failed' }
            $State.FakeUserExists = $false
            if ($State.Case -eq 'remove-partial-failure') { throw 'fake deletion error after account disappearance' }
        }
        FinalAbsence = {
            param($State, $Report)
            [void]$State.Events.Add('FinalAbsence')
            Assert-Nb1Fake (-not $State.FakeUserExists) 'final account absence was not proven'
            Assert-Nb1Fake ($State.ResourcesGone -eq $true) 'final resources still exist'
        }
    }
}

function Get-Nb1FakeEventCount {
    param($State, [string]$Name)
    return @($State.Events | Where-Object { $_ -eq $Name }).Count
}

function Invoke-Nb1FakeCase {
    param([string]$Name)
    $state = @{
        Case = $Name
        CreationAttempted = $false
        CreationVerified = $false
        CreationReturned = $false
        ChildrenExited = $false
        FakeUserExists = $false
        Marker = ''
        ClaimCalls = 0
        CleanupClaimCalls = 0
        ResourcesGone = $false
        Events = [System.Collections.ArrayList]::new()
    }
    $report = @{ status = 'FAIL'; checks = @(); cleanup = @() }
    $output = @(Invoke-Nb1TokenLifecycle -State $state -Report $report -Ops (New-Nb1FakeOps))
    Assert-Nb1Fake ($output.Count -eq 1) 'lifecycle must return one report without callback pipeline output'
    Assert-Nb1Fake ($output[0] -is [hashtable]) 'lifecycle result must be a report hashtable'
    $actual = $output[0]
    $removeCalls = Get-Nb1FakeEventCount $state 'RemoveUser'
    $cleanupCalls = Get-Nb1FakeEventCount $state 'CleanupResources'
    $claimCalls = Get-Nb1FakeEventCount $state 'ClaimUser'
    $expectedRemoval = $Name -in @('normal', 'partial-create', 'remove-failure',
        'remove-partial-failure', 'groups-failure', 'stage-failure', 'ordinary-failure')

    if ($Name -eq 'normal') {
        Assert-Nb1Fake ($actual.status -eq 'PASS') 'normal fake lifecycle must pass'
    } else {
        Assert-Nb1Fake ($actual.status -ne 'PASS') 'any forward or cleanup failure must stay red'
    }
    Assert-Nb1Fake ($removeCalls -eq [int]$expectedRemoval) 'unverified/failed cleanup must not call remove'
    Assert-Nb1Fake ((Get-Nb1FakeEventCount $state 'ReapChildren') -eq 1) 'finally must attempt child reaping once'
    if ($expectedRemoval) {
        Assert-Nb1Fake ($state.CleanupClaimCalls -eq 2) 'remove path needs two cleanup attribution checks'
        Assert-Nb1Fake ($cleanupCalls -eq 1) 'remove path needs one successful resource cleanup'
        Assert-Nb1Fake ($state.Events.IndexOf('ReapChildren') -lt $state.Events.IndexOf('CleanupResources')) 'reap must precede resource cleanup'
        Assert-Nb1Fake ($state.Events.LastIndexOf('ClaimUser') -lt $state.Events.IndexOf('RemoveUser')) 'second attribution must precede remove'
    }
    if ($Name -eq 'partial-create') {
        Assert-Nb1Fake ($state.CreationAttempted -and -not $state.CreationReturned) 'partial create flags must preserve uncertainty'
        Assert-Nb1Fake ($actual.status -eq 'FAIL_CLEANUP') 'partial creation remains FAIL_CLEANUP after proven cleanup'
        Assert-Nb1Fake (-not $state.FakeUserExists) 'proven partial-create fixture may be cleaned'
    }
    if ($Name -eq 'resource-changed') {
        Assert-Nb1Fake ($actual.profile_observation.root_owner -eq 'different-fake-owner' -and $actual.profile_observation.mapping_matches) 'failed owner/mapping evidence must survive without authorizing removal'
    }
    if ($Name -in @('unknown-child', 'child-unproven', 'foreign-marker', 'claim-exception', 'claim-true-unproven')) {
        Assert-Nb1Fake ($cleanupCalls -eq 0) 'unknown child/account must not reach resource cleanup'
    }
    if ($Name -in @('preflight-failure', 'administrator-failure')) {
        Assert-Nb1Fake (-not $state.CreationAttempted) 'failure before create must not invent an attempt'
        Assert-Nb1Fake ((Get-Nb1FakeEventCount $state 'CreateUser') -eq 0) 'preflight failure must not create a user'
        Assert-Nb1Fake ($claimCalls -eq 0) 'without create intent no account claim/removal is allowed'
    }
    if ($Name -in @('unknown-child', 'child-unproven',
        'resource-changed', 'cleanup-failure', 'claim-changed-before-remove',
        'claim-exception', 'claim-second-exception', 'claim-true-unproven', 'remove-failure')) {
        Assert-Nb1Fake ($state.FakeUserExists -eq $true) 'uncertain or failed cleanup must preserve remaining account'
        Assert-Nb1Fake ($actual.status -eq 'FAIL_CLEANUP') 'cleanup uncertainty must explicitly fail cleanup'
    }
    if ($Name -notin @('preflight-failure', 'administrator-failure', 'partial-create')) {
        Assert-Nb1Fake ($state.CreationReturned -eq $true) 'normal create must set returned flag'
    }
    return @{
        case = $Name
        status = 'PASS'
        lifecycle_status = $actual.status
        creation_attempted = $state.CreationAttempted
        creation_returned = $state.CreationReturned
        creation_verified = $state.CreationVerified
        cleanup_claim_calls = $state.CleanupClaimCalls
        children_exited = $state.ChildrenExited
        remove_calls = $removeCalls
        events = @($state.Events)
    }
}

$cases = @('normal', 'partial-create', 'foreign-marker', 'unknown-child', 'child-unproven',
    'resource-changed', 'cleanup-failure', 'remove-failure', 'remove-partial-failure',
    'claim-changed-before-remove', 'claim-exception', 'claim-second-exception', 'claim-true-unproven',
    'groups-failure', 'stage-failure', 'ordinary-failure', 'preflight-failure', 'administrator-failure')
$results = @()
foreach ($name in $cases) {
    try {
        $results += Invoke-Nb1FakeCase $name
    } catch {
        $results += @{ case = $name; status = 'FAIL'; error = $_.Exception.Message }
    }
}
function New-Nb1FakeNativeReport([string]$Expected) {
    $sid = 'S-1-5-21-11-22-33-1001'
    $value = [ordered]@{
        schema_version = 1; status = 'PASS'; expected = $Expected; expected_sid = $sid
        collection_fields = 6; collection_complete = $true; collector_closed = $true
        facts = [ordered]@{ sid = $sid; session_id = 2; token_is_elevated = 0; elevation_type = 1; integrity_sid = 'S-1-16-8192'; interactive_enabled = $true; service_or_batch = $false; admin_group = 'none'; domain_rid_500 = $false }
        current = [ordered]@{ valid = $true; sid = $sid; task_path = ('\B1UG-nb1-' + $sid); session_id = 2; integrity_rid = 8192; elevation_type = 1; admin_membership = 'none' }
        handle_observation = [ordered]@{ warmup_calls = 8; iterations = 32; completed = 32; budget_ms = 5000; settle_budget_ms = 250; allowed_growth = 2; baseline = 100; final = 100; peak = 101; elapsed_ms = 25; persistent_growth = $false }
    }
    if ($Expected -ceq 'administrator-rejected') {
        $value.facts.token_is_elevated = 1
        $value.facts.elevation_type = 2
        $value.facts.admin_group = 'enabled'
        $value.current = [ordered]@{ valid = $false; sid = ''; task_path = ''; session_id = 0; integrity_rid = 0; elevation_type = 0; admin_membership = '' }
    }
    # Exercise the actual JSON decoding boundary used by the native collector.
    return ($value | ConvertTo-Json -Depth 8 -Compress | ConvertFrom-Json)
}

$protocolCases = @(
    @{ name = 'ordinary-report'; expected = 'ordinary'; reject = $false; change = { param($v) } },
    @{ name = 'administrator-report'; expected = 'administrator-rejected'; reject = $false; change = { param($v) } },
    @{ name = 'report-not-pass'; change = { param($v) $v.status = 'SKIP' } },
    @{ name = 'wrong-sid'; change = { param($v) $v.facts.sid = 'S-1-5-21-11-22-33-1002' } },
    @{ name = 'incomplete-collection'; change = { param($v) $v.collection_fields = 1 } },
    @{ name = 'collector-not-closed'; change = { param($v) $v.collector_closed = $false } },
    @{ name = 'string-boolean'; change = { param($v) $v.collector_closed = 'true' } },
    @{ name = 'string-integer'; change = { param($v) $v.collection_fields = '6' } },
    @{ name = 'handle-loop-incomplete'; change = { param($v) $v.handle_observation.completed = 31 } },
    @{ name = 'handle-growth'; change = { param($v) $v.handle_observation.final = 103; $v.handle_observation.peak = 103 } },
    @{ name = 'handle-persistent-growth'; change = { param($v) $v.handle_observation.persistent_growth = $true } },
    @{ name = 'handle-peak-impossible'; change = { param($v) $v.handle_observation.peak = 99 } },
    @{ name = 'ordinary-session-zero'; change = { param($v) $v.facts.session_id = 0 } },
    @{ name = 'ordinary-not-interactive'; change = { param($v) $v.facts.interactive_enabled = $false } },
    @{ name = 'ordinary-service-token'; change = { param($v) $v.facts.service_or_batch = $true } },
    @{ name = 'ordinary-elevated'; change = { param($v) $v.facts.token_is_elevated = 1 } },
    @{ name = 'ordinary-high-integrity'; change = { param($v) $v.facts.integrity_sid = 'S-1-16-12288' } },
    @{ name = 'current-facts-mismatch'; change = { param($v) $v.current.session_id = 3 } },
    @{ name = 'wrong-task-path'; change = { param($v) $v.current.task_path = '\foreign' } },
    @{ name = 'unproven-admin-rejection'; expected = 'administrator-rejected'; change = { param($v) $v.facts.token_is_elevated = 0 } },
    @{ name = 'nonzero-rejected-context'; expected = 'administrator-rejected'; change = { param($v) $v.current.sid = $v.facts.sid } }
)
foreach ($case in $protocolCases) {
    $expected = 'ordinary'
    if ($case.ContainsKey('expected')) { $expected = $case.expected }
    $reject = $true
    if ($case.ContainsKey('reject')) { $reject = $case.reject }
    try {
        $value = New-Nb1FakeNativeReport $expected
        & $case.change $value
        $rejected = $false
        try { Assert-Nb1NativeReport $value $expected 'S-1-5-21-11-22-33-1001' } catch { $rejected = $true }
        Assert-Nb1Fake ($rejected -eq $reject) 'native result gate accepted invalid facts or rejected valid facts'
        $results += @{ case = $case.name; status = 'PASS'; rejected = $rejected; scope = 'JSON protocol only' }
    } catch { $results += @{ case = $case.name; status = 'FAIL'; error = $_.Exception.Message } }
}

$pathCases = @(
    @{ path = 'C:\fixture\test.exe'; reject = $false },
    @{ path = 'test.exe'; reject = $true },
    @{ path = 'C:test.exe'; reject = $true },
    @{ path = '\fixture\test.exe'; reject = $true },
    @{ path = '\\server\share\test.exe'; reject = $true },
    @{ path = 'C:\fixture\test.exe:stream'; reject = $true }
)
foreach ($case in $pathCases) {
    $rejected = $false
    try { Assert-Nb1LocalAbsolutePath $case.path } catch { $rejected = $true }
    $results += @{ case = ('local-path-' + $case.path); status = $(if ($rejected -eq $case.reject) { 'PASS' } else { 'FAIL' }); rejected = $rejected; scope = 'Path syntax only' }
}

$identityCases = @(
    @{ name = 'fresh-identity'; account = 'nb1tok-0123456789ab'; marker = ('nb1-token-' + ('a' * 32)); reject = $false },
    @{ name = 'legacy-description-49'; account = 'nb1tok-0123456789ab'; marker = ('nb1-token-native-' + ('a' * 32)); reject = $true },
    @{ name = 'foreign-marker-format'; account = 'nb1tok-0123456789ab'; marker = ('foreign-' + ('a' * 32)); reject = $true },
    @{ name = 'overlong-account'; account = ('nb1tok-' + ('a' * 14)); marker = ('nb1-token-' + ('a' * 32)); reject = $true }
)
foreach ($case in $identityCases) {
    $rejected = $false
    try { Assert-Nb1FixtureIdentity $case.account $case.marker } catch { $rejected = $true }
    $results += @{ case = $case.name; status = $(if ($rejected -eq $case.reject) { 'PASS' } else { 'FAIL' }); rejected = $rejected; scope = 'New-LocalUser parameter limits only' }
}
try {
    $names = @{}; $markers = @{}
    for ($i = 0; $i -lt 100; $i++) {
        $identity = New-Nb1FixtureIdentity
        Assert-Nb1Fake ($identity.Name.Length -eq 19 -and $identity.Marker.Length -eq 42) 'generated fixture identity must fit cmdlet limits'
        Assert-Nb1Fake (-not $names.ContainsKey($identity.Name) -and -not $markers.ContainsKey($identity.Marker)) 'generated fixture identity must be fresh'
        $names[$identity.Name] = $true; $markers[$identity.Marker] = $true
    }
    $results += @{ case = 'generated-identity-limits'; status = 'PASS'; samples = 100; scope = 'RNG identity strings only' }
} catch { $results += @{ case = 'generated-identity-limits'; status = 'FAIL'; error = $_.Exception.Message } }

$failed = @($results | Where-Object { $_.status -ne 'PASS' })
$summary = [ordered]@{
    schema_version = 1
    status = $(if ($failed.Count -eq 0) { 'PASS' } else { 'FAIL' })
    scope = 'Injected fake operations only; no NativeOps, account, profile, process, permission, service or USB operations.'
    case_count = $results.Count
    lifecycle_case_count = $cases.Count
    protocol_case_count = $protocolCases.Count
    path_case_count = $pathCases.Count
    identity_case_count = $identityCases.Count + 1
    passed = $results.Count - $failed.Count
    failed = $failed.Count
    results = $results
}
Write-Output ($summary | ConvertTo-Json -Depth 12 -Compress)
if ($failed.Count -ne 0) { exit 1 }
exit 0
