# Create only a fresh runner-owned directory; never change an existing parent ACL.
$ErrorActionPreference = 'Stop'
if ($env:GITHUB_ACTIONS -ne 'true' -or $env:OS -ne 'Windows_NT' -or
    -not [IO.Path]::IsPathRooted($env:RUNNER_TEMP) -or -not $env:GITHUB_ENV) {
    throw 'This fixture requires an isolated Windows GitHub Actions runner.'
}
$root = Join-Path $env:RUNNER_TEMP ('nb1-386-private-temp-' + [Guid]::NewGuid().ToString('N'))
if (Test-Path -LiteralPath $root) { throw 'Refusing an existing private temporary directory.' }
$user = [Security.Principal.WindowsIdentity]::GetCurrent([Security.Principal.TokenAccessLevels]::Query).User
$allowed = @($user.Value, 'S-1-5-18', 'S-1-5-32-544') | Select-Object -Unique
$security = [Security.AccessControl.DirectorySecurity]::new()
$security.SetOwner($user)
$security.SetAccessRuleProtection($true, $false)
$inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
foreach ($sid in $allowed) {
    $principal = [Security.Principal.SecurityIdentifier]::new($sid)
    $rule = [Security.AccessControl.FileSystemAccessRule]::new(
        $principal, [Security.AccessControl.FileSystemRights]::FullControl,
        $inherit, [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Allow)
    $security.AddAccessRule($rule)
}
New-Item -ItemType Directory -Path $root -ErrorAction Stop | Out-Null
# Record ownership before ACL verification so the always() cleanup can remove
# our fresh directory even when a subsequent verification fails. TEMP/TMP are
# not selected until verification succeeds.
"NB1_PATTERN_PRIVATE_TEMP=$root" | Out-File -LiteralPath $env:GITHUB_ENV -Encoding utf8 -Append
Set-Acl -LiteralPath $root -AclObject $security
$actual = Get-Acl -LiteralPath $root
if (-not $actual.AreAccessRulesProtected -or
    $actual.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $user.Value) {
    throw 'Private temporary root did not retain its owner and protected DACL.'
}
$rules = @($actual.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
if ($rules.Count -ne $allowed.Count) { throw 'Private temporary root has an unexpected rule count.' }
foreach ($rule in $rules) {
    if ($rule.IdentityReference.Value -notin $allowed -or $rule.IsInherited -or
        $rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or
        $rule.FileSystemRights -ne [Security.AccessControl.FileSystemRights]::FullControl -or
        $rule.InheritanceFlags -ne $inherit -or
        $rule.PropagationFlags -ne [Security.AccessControl.PropagationFlags]::None) {
        throw 'Private temporary root has an unexpected access rule.'
    }
}
# MSYS2 steps additionally export these values after shell startup, because its
# profile can select a different TEMP/TMP. The Go diagnostic verifies actual use.
@("TEMP=$root", "TMP=$root") |
    Out-File -LiteralPath $env:GITHUB_ENV -Encoding utf8 -Append
Write-Output '{"fixture":"windows-386-private-temp","owner":"current-user","protected":true,"allowed_principals":["current-user","system","administrators"],"existing_parent_acl_changed":false}'
