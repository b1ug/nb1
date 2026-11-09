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
            if ($State.Case.StartsWith('query-', [StringComparison]::Ordinal)) {
                if ($State.Case -eq 'query-stale-completion') { $State.QueryCompleted = $true }
                Start-Nb1QueryAttempt $State
                if ($State.Case -eq 'query-complete') { $State.QueryCompleted = $true }
                else { throw 'fake query missing/bad report or Go timeout does not prove PowerShell descendant exit' }
            }
            if ($State.Case.StartsWith('timing-', [StringComparison]::Ordinal)) {
                $State.TimingBinary = 'fake-timing.exe'
                if ($State.Case -eq 'timing-stale-completion') { $State.DiagnosticsCompleted = $true }
                Start-Nb1DiagnosticsAttempt $State
                if ($State.Case -in @('timing-complete','timing-reported-failure','timing-after-query-failed')) { $State.DiagnosticsCompleted = $true }
                if ($State.Case -eq 'timing-after-query-failed') { Start-Nb1QueryAttempt $State }
                if ($State.Case -ne 'timing-complete') { throw 'fake failed/partial timing observation' }
            }
        }
        ReapChildren = {
            param($State, $Report)
            [void]$State.Events.Add('ReapChildren')
            if ($State.Case -eq 'unknown-child') { throw 'fake child exit is unknown' }
            if ($State.Case -eq 'child-unproven') { return }
            Assert-Nb1QueryExitProof $State
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
        'remove-partial-failure', 'groups-failure', 'stage-failure', 'ordinary-failure', 'query-complete', 'timing-complete', 'timing-reported-failure')

    if ($Name -in @('normal','query-complete','timing-complete')) {
        $expectedStatus = 'PASS'
        if ($Name -eq 'timing-complete') { $expectedStatus = 'DIAGNOSTIC_ONLY' }
        Assert-Nb1Fake ($actual.status -eq $expectedStatus) 'normal fake lifecycle must have the exact mode verdict'
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
    if ($Name.StartsWith('query-', [StringComparison]::Ordinal) -and $Name -ne 'query-complete') {
        Assert-Nb1Fake ($actual.status -eq 'FAIL_CLEANUP' -and -not $state.ChildrenExited -and $state.FakeUserExists -and $cleanupCalls -eq 0 -and $removeCalls -eq 0) 'direct Go exit is not query descendant exit proof; preserve all resources'
        Assert-Nb1Fake (-not $actual.query_bridge_exit_proven) 'incomplete query cannot acquire cleanup proof'
    }
    if ($Name.StartsWith('timing-', [StringComparison]::Ordinal)) {
        Assert-Nb1Fake (-not $state.ContainsKey('QueryCompleted') -or -not $state.QueryCompleted) 'diagnostic success must never set ordinary QueryCompleted'
        if ($Name -notin @('timing-complete','timing-reported-failure')) { Assert-Nb1Fake ($actual.status -eq 'FAIL_CLEANUP' -and -not $state.ChildrenExited -and $state.FakeUserExists -and $cleanupCalls -eq 0 -and $removeCalls -eq 0) 'unknown/partial timing or earlier failed query must block resource cleanup' }
        if ($Name -eq 'timing-reported-failure') { Assert-Nb1Fake ($actual.status -eq 'FAIL' -and $actual.diagnostics_bridge_exit_proven -and -not $state.FakeUserExists) 'proven complete failed diagnostic may clean but cannot become acceptance' }
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
    'groups-failure', 'stage-failure', 'ordinary-failure', 'preflight-failure', 'administrator-failure',
    'query-complete', 'query-missing-report', 'query-bad-report', 'query-timeout-go-exited', 'query-stale-completion',
    'timing-complete', 'timing-reported-failure', 'timing-missing-report', 'timing-bad-report', 'timing-timeout-go-exited', 'timing-partial-pswait', 'timing-stale-completion', 'timing-after-query-failed')
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

$groupCases = @('builtin-alias', 'localized-alias', 'empty-membership', 'unmapped-alias', 'ambiguous-alias', 'different-alias', 'invalid-sid', 'enabled-administrators', 'additional-group')
foreach ($groupCase in $groupCases) {
    try {
        $names = @('Users')
        if ($groupCase -eq 'localized-alias') { $names = @('LocalUsersAlias') }
        if ($groupCase -eq 'empty-membership') { $names = @() }
        if ($groupCase -eq 'additional-group') { $names = @('Users','Administrators') }
        $lookup = {
            param($name)
            Assert-Nb1Fake (-not $name.Contains('\')) 'NetAPI local alias must not acquire an invented machine domain'
            if ($groupCase -eq 'unmapped-alias') { throw 'fake local SAM lookup failed' }
            $sid = 'S-1-5-32-545'
            if ($groupCase -eq 'enabled-administrators' -or $name -eq 'Administrators') { $sid = 'S-1-5-32-544' }
            if ($groupCase -eq 'invalid-sid') { $sid = 'unknown' }
            $returnedName = $name
            if ($groupCase -eq 'different-alias') { $returnedName = 'DifferentLocalAlias' }
            [pscustomobject]@{ Name = $returnedName; SID = [pscustomobject]@{ Value = $sid } }
            if ($groupCase -eq 'ambiguous-alias') { [pscustomobject]@{ Name = $name; SID = [pscustomobject]@{ Value = $sid } } }
        }
        $rejected = $false
        try {
            $sids = @(Resolve-Nb1LocalGroupSIDs -Names $names -Lookup $lookup)
            Assert-Nb1UsersOnly $sids
        } catch { $rejected = $true }
        Assert-Nb1Fake ($rejected -eq ($groupCase -notin @('builtin-alias','localized-alias'))) 'group resolution or Users-only gate is incorrect'
        $results += @{ case = ('local-group-' + $groupCase); status = 'PASS'; rejected = $rejected; scope = 'Injected local group lookup only' }
    } catch { $results += @{ case = ('local-group-' + $groupCase); status = 'FAIL'; error = $_.Exception.Message } }
}

$profileCases = @('fresh-owner', 'admin-owner-proven', 'unknown-owner', 'system-owner-unobserved', 'preexisting-profile', 'native-not-passed', 'unverified-creation', 'mapping-mismatch', 'fresh-readonly', 'unknown-write', 'unknown-generic-write', 'root-deny', 'inherit-only-owner', 'fresh-owner-preexisting', 'fresh-owner-native-failed', 'fresh-owner-partial-create', 'fresh-owner-unsafe-acl')
foreach ($profileCase in $profileCases) {
    try {
        $sid = 'S-1-5-21-11-22-33-1001'
        $state = @{ SID = $sid; HostSID = 'S-1-5-21-11-22-33-500'; CreationReturned = $true; CreationVerified = $true; ProfileAbsentBeforeLogon = $true; OrdinaryNativePassed = $true; ProfileObservation = @{ mapping_matches = $true } }
        $snapshot = @{ owner = 'S-1-5-32-544' }
        $rules = @(@{ sid = $sid; allow = $true; rights = 0x1f01ff; inherit_only = $false })
        if ($profileCase -eq 'fresh-owner') { $snapshot.owner = $sid }
        if ($profileCase.StartsWith('fresh-owner-', [StringComparison]::Ordinal)) { $snapshot.owner = $sid }
        if ($profileCase -eq 'fresh-owner-preexisting') { $state.ProfileAbsentBeforeLogon = $false }
        if ($profileCase -eq 'fresh-owner-native-failed') { $state.OrdinaryNativePassed = $false }
        if ($profileCase -eq 'fresh-owner-partial-create') { $state.CreationReturned = $false }
        if ($profileCase -eq 'fresh-owner-unsafe-acl') { $rules += @{ sid = 'S-1-5-32-545'; allow = $true; rights = 2; inherit_only = $false } }
        if ($profileCase -eq 'unknown-owner') { $snapshot.owner = 'S-1-5-21-11-22-33-1002' }
        if ($profileCase -eq 'system-owner-unobserved') { $snapshot.owner = 'S-1-5-18' }
        if ($profileCase -eq 'preexisting-profile') { $state.ProfileAbsentBeforeLogon = $false }
        if ($profileCase -eq 'native-not-passed') { $state.OrdinaryNativePassed = $false }
        if ($profileCase -eq 'unverified-creation') { $state.CreationVerified = $false }
        if ($profileCase -eq 'mapping-mismatch') { $state.ProfileObservation.mapping_matches = $false }
        if ($profileCase -eq 'fresh-readonly') { $rules[0].rights = 0x1200a9 }
        if ($profileCase -eq 'unknown-write') { $rules += @{ sid = 'S-1-5-32-545'; allow = $true; rights = 2; inherit_only = $false } }
        if ($profileCase -eq 'unknown-generic-write') { $rules += @{ sid = 'S-1-5-32-545'; allow = $true; rights = 0x40000000; inherit_only = $true } }
        if ($profileCase -eq 'root-deny') { $rules += @{ sid = $sid; allow = $false; rights = 1; inherit_only = $false } }
        if ($profileCase -eq 'inherit-only-owner') { $rules += @{ sid = 'S-1-3-0'; allow = $true; rights = 0x1f01ff; inherit_only = $true } }
        $access = Get-Nb1ProfileAccessFacts $rules $sid $state.HostSID
        $rejected = $false
        try { $basis = Assert-Nb1ProfileOwner $state $snapshot $access } catch { $rejected = $true }
        Assert-Nb1Fake ($rejected -eq ($profileCase -notin @('fresh-owner','admin-owner-proven','inherit-only-owner'))) 'profile owner compatibility must require exact provenance and safe DACL'
        $results += @{ case = ('profile-owner-' + $profileCase); status = 'PASS'; rejected = $rejected; scope = 'Injected profile metadata/ACE facts only' }
    } catch { $results += @{ case = ('profile-owner-' + $profileCase); status = 'FAIL'; error = $_.Exception.Message } }
}

$aceCases = @('allowed-common', 'denied-common', 'object-ace', 'callback-ace', 'opaque-ace', 'audit-ace', 'unknown-flags', 'unknown-rights', 'negative-rights', 'overlong-rights')
foreach ($aceCase in $aceCases) {
    try {
        $ace = @{ kind = 'CommonAce'; callback = $false; opaque = 0; type = 0; flags = 3; rights = 0x1f01ff; sid = 'S-1-5-21-11-22-33-1001' }
        if ($aceCase -eq 'denied-common') { $ace.type = 1 }
        if ($aceCase -eq 'object-ace') { $ace.kind = 'ObjectAce' }
        if ($aceCase -eq 'callback-ace') { $ace.callback = $true }
        if ($aceCase -eq 'opaque-ace') { $ace.opaque = 4 }
        if ($aceCase -eq 'audit-ace') { $ace.type = 2 }
        if ($aceCase -eq 'unknown-flags') { $ace.flags = 32 }
        if ($aceCase -eq 'unknown-rights') { $ace.rights = 0x02000000 }
        if ($aceCase -eq 'negative-rights') { $ace.rights = -1 }
        if ($aceCase -eq 'overlong-rights') { $ace.rights = 0x100000000L }
        $rejected = $false
        try { $normalized = Convert-Nb1ProfileAce $ace } catch { $rejected = $true }
        Assert-Nb1Fake ($rejected -eq ($aceCase -notin @('allowed-common','denied-common'))) 'unknown raw ACE semantics must never be projected away'
        $results += @{ case = ('raw-ace-' + $aceCase); status = 'PASS'; rejected = $rejected; scope = 'Injected raw ACE facts only' }
    } catch { $results += @{ case = ('raw-ace-' + $aceCase); status = 'FAIL'; error = $_.Exception.Message } }
}
$daclCases = @('present', 'absent', 'null', 'empty', 'overlong')
foreach ($daclCase in $daclCases) {
    $present = $true; $dacl = @(1)
    if ($daclCase -eq 'absent') { $present = $false }
    if ($daclCase -eq 'null') { $dacl = $null }
    if ($daclCase -eq 'empty') { $dacl = @() }
    if ($daclCase -eq 'overlong') { $dacl = @(1..65) }
    $rejected = $false
    try { Assert-Nb1RawDacl $present $dacl } catch { $rejected = $true }
    $results += @{ case = ('raw-dacl-' + $daclCase); status = $(if ($rejected -eq ($daclCase -ne 'present')) { 'PASS' } else { 'FAIL' }); scope = 'Injected DACL header only' }
}
$snapshotCases = @('unchanged', 'path', 'id', 'attributes', 'owner', 'sddl', 'raw_security_sha256')
foreach ($snapshotCase in $snapshotCases) {
    $expected = @{ path = 'C:\fixture'; id = 'file-id-1'; attributes = '16'; owner = 'fresh'; sddl = 'same-acl'; raw_security_sha256 = 'same-raw-hash' }
    $actual = $expected.Clone()
    if ($snapshotCase -ne 'unchanged') { $actual[$snapshotCase] = 'changed-value' }
    $rejected = $false
    try { Assert-Nb1Same $expected $actual -IdentityOnly } catch { $rejected = $true }
    $results += @{ case = ('snapshot-' + $snapshotCase); status = $(if ($rejected -eq ($snapshotCase -ne 'unchanged')) { 'PASS' } else { 'FAIL' }); scope = 'Injected filesystem metadata only' }
}

$claimEvidenceCases = @('repeat-preserves-group-proof', 'name-changed', 'sid-changed', 'marker-changed')
foreach ($claimCase in $claimEvidenceCases) {
    try {
        $report = [ordered]@{}
        $name = 'fresh'; $sid = 'S-1-5-21-11-22-33-1001'; $marker = 'same-marker-hash'
        Set-Nb1FixtureUser $report $name $sid $marker
        $report.fixture_user['local_group_sids'] = @('S-1-5-32-545')
        if ($claimCase -eq 'name-changed') { $name = 'foreign' }
        if ($claimCase -eq 'sid-changed') { $sid = 'S-1-5-21-11-22-33-1002' }
        if ($claimCase -eq 'marker-changed') { $marker = 'different-marker-hash' }
        $rejected = $false
        try { Set-Nb1FixtureUser $report $name $sid $marker; Set-Nb1FixtureUser $report $name $sid $marker } catch { $rejected = $true }
        Assert-Nb1Fake ($rejected -eq ($claimCase -ne 'repeat-preserves-group-proof')) 'repeated claims must preserve the exact original identity'
        $decoded = $report | ConvertTo-Json -Depth 5 | ConvertFrom-Json
        Assert-Nb1Fake (@($decoded.fixture_user.local_group_sids).Count -eq 1 -and $decoded.fixture_user.local_group_sids[0] -ceq 'S-1-5-32-545') 'repeated cleanup claims must preserve group proof through JSON encoding'
        $results += @{ case = ('claim-evidence-' + $claimCase); status = 'PASS'; rejected = $rejected; scope = 'Injected report metadata only' }
    } catch { $results += @{ case = ('claim-evidence-' + $claimCase); status = 'FAIL'; error = $_.Exception.Message } }
}

$caseSpecCases = @('token-admin','token-ordinary','query-ordinary','timing-ordinary','QUERY-ORDINARY','TOKEN-ADMIN','TIMING-ORDINARY','arbitrary-script')
foreach ($caseKind in $caseSpecCases) {
    try {
        $state = @{ SID = 'fresh-sid'; HostSID = 'host-sid'; WindowsDirectory = 'C:\Windows'; SystemDirectory = 'C:\Windows\System32'; TempDir = 'C:\private\tmp'; Password = 'fake-credential'; Binary = 'C:\source\token.exe'; SourceSnapshot = @{ sha256 = 'token-hash' }; StagedBinary = 'C:\private\bin\token.exe'; StagedSnapshot = @{ sha256 = 'token-hash' }; QueryBinary = 'C:\source\query.exe'; StagedQueryBinary = 'C:\private\bin\query.exe'; StagedQuerySnapshot = @{ sha256 = 'query-hash' } }
        if ($caseKind -ceq 'timing-ordinary') { $state.QueryBinary = $null; $state.TimingBinary = 'C:\source\timing.exe'; $state.StagedTimingBinary = 'C:\private\bin\timing.exe'; $state.StagedTimingSnapshot = @{ sha256 = 'timing-hash' } }
        $rejected = $false
        try { $spec = Get-Nb1NativeCaseSpec $state $caseKind } catch { $rejected = $true }
        $valid = $caseKind -cin @('token-admin','token-ordinary','query-ordinary','timing-ordinary')
        Assert-Nb1Fake ($rejected -eq (-not $valid)) 'case protocol must reject arbitrary/case-mutated kinds'
        if ($valid) {
            Assert-Nb1Fake ($spec.environment.Count -eq $(if ($caseKind -cin @('query-ordinary','timing-ordinary')) { 7 } else { 8 })) 'case must retain exact environment allowlist size'
            Assert-Nb1Fake (-not $spec.environment.Contains('GITHUB_TOKEN') -and -not $spec.environment.Contains('SUDO_USER')) 'case cannot inherit runner credentials'
            if ($caseKind -ceq 'query-ordinary') {
                Assert-Nb1Fake ($spec.executable -ceq $state.StagedQueryBinary -and $spec.snapshot.sha256 -ceq 'query-hash' -and $spec.sid -ceq 'fresh-sid' -and $spec.credential -ceq $state.Password -and $spec.prefix -ceq 'NB1_TASKQUERY_NATIVE_RESULT ' -and $spec.arguments -ceq '-test.run=^TestNativeQueryOptIn$ -test.v -test.count=1' -and -not $spec.environment.Contains('NB1_TASKUSER_NATIVE')) 'query protocol must use only its own binary/identity/argv/env/prefix'
            } elseif ($caseKind -ceq 'timing-ordinary') {
                Assert-Nb1Fake ($spec.executable -ceq $state.StagedTimingBinary -and $spec.snapshot.sha256 -ceq 'timing-hash' -and $spec.sid -ceq 'fresh-sid' -and $spec.credential -ceq $state.Password -and $spec.prefix -ceq 'NB1_TASKQUERY_TIMING_RESULT ' -and $spec.arguments -ceq '-test.run=^TestNativeQueryTiming$ -test.v -test.count=1' -and $spec.wait_ms -eq 90000 -and -not $spec.environment.Contains('NB1_TASKQUERY_NATIVE') -and -not $spec.environment.Contains('NB1_TASKUSER_NATIVE')) 'timing must use its own binary/fixed90s/diagnostic env without production opt-in'
            } else {
                Assert-Nb1Fake ($spec.prefix -ceq 'NB1_TASKUSER_NATIVE_RESULT ' -and $spec.arguments -ceq '-test.run=^TestNativeCurrentOptIn$ -test.v -test.count=1' -and -not $spec.environment.Contains('NB1_TASKQUERY_NATIVE')) 'token protocol must stay distinct and unchanged'
                if ($caseKind -ceq 'token-admin') { Assert-Nb1Fake ($null -eq $spec.credential -and $spec.sid -ceq 'host-sid' -and $spec.executable -ceq $state.Binary -and $spec.working_directory -ceq $state.SystemDirectory) 'admin protocol must use host without fresh-user credentials' }
            }
        }
        $results += @{ case = ('closed-case-' + $caseKind); status = 'PASS'; rejected = $rejected; scope = 'Injected case state only' }
    } catch { $results += @{ case = ('closed-case-' + $caseKind); status = 'FAIL'; error = $_.Exception.Message } }
}
$stagedFileCases = @('token-only','token-and-query','token-and-timing','mutually-exclusive')
foreach ($stagedCase in $stagedFileCases) {
    $state = @{ StagedBinary = 'token.exe'; StagedSnapshot = @{ sha256 = 'token-hash' }; QueryBinary = $null }
    if ($stagedCase -eq 'token-and-query') { $state.QueryBinary = 'source-query.exe'; $state.StagedQueryBinary = 'query.exe'; $state.StagedQuerySnapshot = @{ sha256 = 'query-hash' } }
    if ($stagedCase -in @('token-and-timing','mutually-exclusive')) { $state.TimingBinary = 'source-timing.exe'; $state.StagedTimingBinary = 'timing.exe'; $state.StagedTimingSnapshot = @{ sha256 = 'timing-hash' } }
    if ($stagedCase -eq 'mutually-exclusive') {
        $state.QueryBinary = 'source-query.exe'; $state.StagedQueryBinary = 'query.exe'; $state.StagedQuerySnapshot = @{ sha256 = 'query-hash' }
        $rejected = $false
        try { $files = @(Get-Nb1StagedFiles $state) } catch { $rejected = $true }
        $results += @{ case = 'staged-file-mutually-exclusive'; status = $(if ($rejected) { 'PASS' } else { 'FAIL' }); scope = 'Injected mode state only' }
        continue
    }
    $files = @(Get-Nb1StagedFiles $state)
    $valid = $files.Count -eq $(if ($stagedCase -eq 'token-only') { 1 } else { 2 }) -and $files[0].snapshot.sha256 -ceq 'token-hash'
    if ($stagedCase -eq 'token-and-query') { $valid = $valid -and $files[1].snapshot.sha256 -ceq 'query-hash' }
    if ($stagedCase -eq 'token-and-timing') { $valid = $valid -and $files[1].snapshot.sha256 -ceq 'timing-hash' }
    $results += @{ case = ('staged-file-' + $stagedCase); status = $(if ($valid) { 'PASS' } else { 'FAIL' }); scope = 'Injected known-file state only' }
}

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
    local_group_case_count = $groupCases.Count
    profile_owner_case_count = $profileCases.Count
    raw_ace_case_count = $aceCases.Count
    raw_dacl_case_count = $daclCases.Count
    snapshot_case_count = $snapshotCases.Count
    claim_evidence_case_count = $claimEvidenceCases.Count
    case_spec_case_count = $caseSpecCases.Count
    staged_file_case_count = $stagedFileCases.Count
    passed = $results.Count - $failed.Count
    failed = $failed.Count
    results = $results
}
Write-Output ($summary | ConvertTo-Json -Depth 12 -Compress)
if ($failed.Count -ne 0) { exit 1 }
exit 0
