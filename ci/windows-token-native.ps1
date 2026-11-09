param(
    [switch]$Execute,
    [string]$Binary,
    [string]$OutputDirectory,
    [switch]$LibraryOnly
)

# Disposable CI-account fixture only. No tasks, devices, services or policy changes.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Add-Nb1Step($Report, [string]$Name, [string]$Status) {
    $Report.cleanup += @([ordered]@{ step = $Name; status = $Status })
}

function Get-Nb1Failure([string]$Phase, $Record) {
    $errorObject = $Record.Exception
    for ($i = 0; $i -lt 8 -and $null -ne $errorObject.InnerException; $i++) { $errorObject = $errorObject.InnerException }
    $nativeCode = $null
    if ($errorObject -is [ComponentModel.Win32Exception]) { $nativeCode = $errorObject.NativeErrorCode }
    return [ordered]@{ phase = $Phase; exception_type = $errorObject.GetType().FullName; hresult = $errorObject.HResult; native_error_code = $nativeCode; powershell_error_id = $Record.FullyQualifiedErrorId }
}

function Invoke-Nb1TokenLifecycle([hashtable]$State, $Report, [hashtable]$Ops) {
    $mainPassed = $false
    $phase = 'preflight'
    try {
        & $Ops.Preflight $State $Report
        $phase = 'administrator-case'; & $Ops.AdministratorCase $State $Report
        $phase = 'create-user'; $State.CreationAttempted = $true
        & $Ops.CreateUser $State $Report
        $State.CreationReturned = $true
        $phase = 'claim-user'
        $claimed = @(& $Ops.ClaimUser $State $Report)
        if ($claimed.Count -ne 1 -or $claimed[0] -isnot [bool] -or -not $claimed[0] -or -not $State.CreationVerified) { throw 'new account attribution failed' }
        $phase = 'groups'; & $Ops.EnsureGroups $State $Report
        $phase = 'stage'; & $Ops.StageFixture $State $Report
        $phase = 'ordinary-case'; & $Ops.OrdinaryCase $State $Report
        $mainPassed = $true
    } catch {
        $Report.failure = Get-Nb1Failure $phase $_
        $Report.status = 'FAIL'
        if ($_.Exception.Message.StartsWith('ENVIRONMENT_UNAVAILABLE:', [StringComparison]::Ordinal)) { $Report.status = 'ENVIRONMENT_UNAVAILABLE' }
    } finally {
        $cleanupPassed = $true
        try {
            & $Ops.ReapChildren $State $Report
            if (-not $State.ChildrenExited) { throw 'private child exit is not proven' }
            Add-Nb1Step $Report 'all-private-children-exited' 'PASS'
        } catch {
            $cleanupPassed = $false
            Add-Nb1Step $Report 'all-private-children-exited' 'FAIL'
        }
        if ($State.CreationAttempted -and $cleanupPassed) {
            try {
                $claimed = @(& $Ops.ClaimUser $State $Report)
                if ($claimed.Count -ne 1 -or $claimed[0] -isnot [bool]) { throw 'invalid attribution response' }
                if ($claimed[0]) {
                    if (-not $State.CreationVerified) { throw 'account is unverified' }
                    & $Ops.CleanupResources $State $Report
                    Add-Nb1Step $Report 'exact-profile-and-fixture-cleanup' 'PASS'
                    $again = @(& $Ops.ClaimUser $State $Report)
                    if ($again.Count -ne 1 -or $again[0] -isnot [bool] -or -not $again[0] -or -not $State.CreationVerified) { throw 'account identity changed before removal' }
                    & $Ops.RemoveUser $State $Report
                    & $Ops.FinalAbsence $State $Report
                    Add-Nb1Step $Report 'fresh-account-profile-and-fixture-absent' 'PASS'
                } elseif ($State.CreationVerified) {
                    throw 'verified account disappeared unexpectedly; preserve unknown residuals'
                } else {
                    Add-Nb1Step $Report 'no-attributed-account-created' 'PASS'
                }
            } catch {
                $cleanupPassed = $false
                Add-Nb1Step $Report 'fresh-resource-cleanup' 'FAIL'
                $Report.cleanup_error = Get-Nb1Failure 'fresh-resource-cleanup' $_
            }
            if (-not $State.CreationReturned) {
                $cleanupPassed = $false
                Add-Nb1Step $Report 'creation-completion-unconfirmed' 'FAIL'
            }
        }
        if (-not $cleanupPassed) { $Report.status = 'FAIL_CLEANUP' }
        elseif ($mainPassed) { $Report.status = 'PASS' }
        $Report.creation = [ordered]@{ attempted = [bool]$State.CreationAttempted; returned = [bool]$State.CreationReturned; verified = [bool]$State.CreationVerified }
        $Report.children_exit_proven = [bool]$State.ChildrenExited
        if ($State.ContainsKey('ProfileObservation')) { $Report.profile_observation = $State.ProfileObservation }
    }
    return $Report
}

function Initialize-Nb1NativeTypes {
    if ($null -ne ('Nb1TokenFixture.Native' -as [type])) { return }
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;
using System.Security;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using Microsoft.Win32.SafeHandles;
namespace Nb1TokenFixture {
 public static class Native {
  [StructLayout(LayoutKind.Sequential)] struct Info {
   public uint Attributes; public System.Runtime.InteropServices.ComTypes.FILETIME Creation, Access, Write;
   public uint Volume, SizeHigh, SizeLow, Links, IndexHigh, IndexLow;
  }
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
  static extern SafeFileHandle CreateFileW(string path,uint access,uint share,IntPtr security,uint creation,uint flags,IntPtr template);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool CreateDirectoryW(string path,IntPtr security);
  [DllImport("kernel32.dll", SetLastError=true)] static extern bool GetFileInformationByHandle(SafeFileHandle handle,out Info info);
  [DllImport("advapi32.dll")] static extern uint GetSecurityInfo(SafeFileHandle handle,int type,uint information,out IntPtr owner,out IntPtr group,out IntPtr dacl,out IntPtr sacl,out IntPtr descriptor);
  [DllImport("advapi32.dll")] static extern uint GetSecurityDescriptorLength(IntPtr descriptor);
  [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr memory);
  [DllImport("userenv.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool GetProfilesDirectoryW(StringBuilder path,ref uint size);
  [DllImport("userenv.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool DeleteProfileW(string sid,string path,string computer);
  [DllImport("netapi32.dll", CharSet=CharSet.Unicode)] static extern int NetUserGetLocalGroups(string server,string user,uint level,uint flags,out IntPtr buffer,uint preferred,out uint read,out uint total);
  [DllImport("netapi32.dll")] static extern int NetApiBufferFree(IntPtr buffer);
  public static void MakeDirectoryExclusive(string path) {
   if(!CreateDirectoryW(path,IntPtr.Zero)) throw new Win32Exception(Marshal.GetLastWin32Error(),"fresh fixture directory publication failed");
  }
  public static bool ExistsNoFollow(string path) {
   using (SafeFileHandle handle=CreateFileW(path,0x80,7,IntPtr.Zero,3,0x02200000,IntPtr.Zero)) {
    if(!handle.IsInvalid) return true;
    int error=Marshal.GetLastWin32Error(); if(error==2 || error==3) return false;
    throw new Win32Exception(error,"fixture existence is unknown");
   }
  }
  public static string[] FileIdentity(string path) {
   using (SafeFileHandle handle=CreateFileW(path,0x80,7,IntPtr.Zero,3,0x02200000,IntPtr.Zero)) {
    if(handle.IsInvalid) throw new Win32Exception(Marshal.GetLastWin32Error(),"fixture metadata open failed");
    Info value; if(!GetFileInformationByHandle(handle,out value)) throw new Win32Exception(Marshal.GetLastWin32Error(),"fixture metadata query failed");
    return new string[]{value.Volume.ToString("x8")+":"+value.IndexHigh.ToString("x8")+value.IndexLow.ToString("x8"),value.Attributes.ToString(),(((ulong)value.SizeHigh<<32)|value.SizeLow).ToString(),value.Write.dwHighDateTime.ToString()+":"+value.Write.dwLowDateTime.ToString()};
   }
  }
  public static byte[] SecurityDescriptor(string path) {
   using(SafeFileHandle handle=CreateFileW(path,0x20080,7,IntPtr.Zero,3,0x02200000,IntPtr.Zero)) {
    if(handle.IsInvalid) throw new Win32Exception(Marshal.GetLastWin32Error(),"profile raw security open failed");
    IntPtr owner,group,dacl,sacl,descriptor=IntPtr.Zero;
    try {
     uint result=GetSecurityInfo(handle,1,7,out owner,out group,out dacl,out sacl,out descriptor);
     if(result!=0) throw new Win32Exception((int)result,"profile raw security query failed");
     uint length=GetSecurityDescriptorLength(descriptor);
     if(length<20 || length>65536) throw new InvalidOperationException("profile raw security size is unknown");
     byte[] bytes=new byte[length]; Marshal.Copy(descriptor,bytes,0,(int)length); return bytes;
    } finally { if(descriptor!=IntPtr.Zero && LocalFree(descriptor)!=IntPtr.Zero) throw new InvalidOperationException("profile raw security release failed"); }
   }
  }
  public static string ProfilesRoot() {
   uint size=256;
   for(int i=0;i<3;i++) {
    StringBuilder path=new StringBuilder((int)size);
    if(GetProfilesDirectoryW(path,ref size)) return path.ToString();
    int error=Marshal.GetLastWin32Error(); if(error!=122 || size<1 || size>32768) throw new Win32Exception(error,"profiles root unavailable");
   }
   throw new InvalidOperationException("profiles root size did not stabilize");
  }
  public static void DeleteProfile(string sid) {
   if(!DeleteProfileW(sid,null,null)) throw new Win32Exception(Marshal.GetLastWin32Error(),"exact fixture profile removal failed");
  }
  public static string[] LocalGroupNames(string user) {
   IntPtr buffer=IntPtr.Zero; uint read,total;
   int result=NetUserGetLocalGroups(null,user,0,1,out buffer,65536,out read,out total);
   try {
    if(result!=0 || read!=total || read>64) throw new Win32Exception(result,"fresh user group query incomplete");
    List<string> values=new List<string>();
    for(int i=0;i<(int)read;i++) {
     IntPtr name=Marshal.ReadIntPtr(buffer,i*IntPtr.Size);
     string group=Marshal.PtrToStringUni(name);
     values.Add(group);
    }
    return values.ToArray();
   } finally { if(buffer!=IntPtr.Zero && NetApiBufferFree(buffer)!=0) throw new InvalidOperationException("group buffer release failed"); }
  }
  public static SecureString Password() {
   const string alphabet="abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#%_-";
   byte[] bytes=new byte[32]; SecureString result=new SecureString();
   try {
    using(RandomNumberGenerator random=RandomNumberGenerator.Create()) random.GetBytes(bytes);
    foreach(char value in "Nb1!") result.AppendChar(value);
    foreach(byte value in bytes) result.AppendChar(alphabet[value%alphabet.Length]);
    result.MakeReadOnly(); return result;
   } catch { result.Dispose(); throw; } finally { Array.Clear(bytes,0,bytes.Length); }
  }
 }
}
'@
}

function Get-Nb1LocalUser([string]$Name) {
    try { return Get-LocalUser -Name $Name -ErrorAction Stop }
    catch {
        if ($_.FullyQualifiedErrorId.StartsWith('UserNotFound,', [StringComparison]::Ordinal)) { return $null }
        throw
    }
}

function Resolve-Nb1LocalGroupSIDs([string[]]$Names, [scriptblock]$Lookup) {
    # NetUserGetLocalGroups returns local alias names, including BUILTIN aliases.
    # Resolve on the local SAM through Get-LocalGroup; never invent a machine domain.
    if ($null -eq $Names) { return }
    if ($Names.Count -gt 64) { throw 'fresh user local group count is unknown' }
    foreach ($name in $Names) {
        if ([string]::IsNullOrEmpty($name)) { throw 'fresh user local group name is unknown' }
        $groups = @(& $Lookup $name)
        if ($groups.Count -ne 1 -or $groups[0].Name -ine $name -or $groups[0].SID.Value -cnotmatch '^S-1-5-(32-\d+|21-\d+-\d+-\d+-\d+)$') { throw 'fresh user local group lookup is not exact' }
        Write-Output $groups[0].SID.Value
    }
}

function Assert-Nb1UsersOnly([string[]]$SIDs) {
    if ($null -eq $SIDs -or $SIDs.Count -ne 1 -or $SIDs[0] -cne 'S-1-5-32-545') { throw 'fresh user belongs to unexpected groups; no policy/group fallback' }
}

function Get-Nb1Snapshot([string]$Path, [switch]$Directory) {
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or [bool]$item.PSIsContainer -ne [bool]$Directory) { throw 'reparse or unexpected fixture file type' }
    $native = [Nb1TokenFixture.Native]::FileIdentity($Path)
    if (([uint32]$native[1] -band 0x400) -ne 0 -or (([uint32]$native[1] -band 0x10) -ne 0) -ne [bool]$Directory) { throw 'native fixture metadata is reparse or wrong type' }
    $acl = Get-Acl -LiteralPath $Path
    $owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
    $hash = $null
    if (-not $Directory) { $hash = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() }
    return [ordered]@{ path = [IO.Path]::GetFullPath($Path); id = $native[0]; attributes = $native[1]; length = $native[2]; write = $native[3]; owner = $owner; sddl = $acl.Sddl; sha256 = $hash }
}

function Assert-Nb1Same($Expected, $Actual, [switch]$IdentityOnly) {
    $fields = @('path','id','attributes','owner','sddl')
    if ($Expected.Contains('raw_security_sha256')) { $fields += 'raw_security_sha256' }
    if (-not $IdentityOnly) { $fields += @('length','write','sha256') }
    foreach ($key in $fields) { if ($Expected[$key] -cne $Actual[$key]) { throw 'fixture identity/content/ACL changed; preserve it' } }
}

function Assert-Nb1LocalAbsolutePath([string]$Path) {
    if ($Path -notmatch '^[A-Za-z]:\\' -or $Path.Substring(2).Contains(':') -or $Path -match '[\x00-\x1f]') { throw 'explicit local drive absolute path required; no UNC or drive-relative path' }
}

function Assert-Nb1FixtureIdentity([string]$Name, [string]$Marker) {
    # New-LocalUser Name allows 20 characters; Description allows only 48.
    if ($Name.Length -gt 20 -or $Name -cnotmatch '^nb1tok-[0-9a-f]{12}$' -or $Marker.Length -gt 48 -or $Marker -cnotmatch '^nb1-token-[0-9a-f]{32}$') { throw 'fixture account name or description marker is invalid' }
}

function New-Nb1FixtureIdentity {
    $value = [ordered]@{ Name = ('nb1tok-' + [Guid]::NewGuid().ToString('N').Substring(0, 12)); Marker = ('nb1-token-' + [Guid]::NewGuid().ToString('N')) }
    Assert-Nb1FixtureIdentity $value.Name $value.Marker
    return $value
}

function Get-Nb1ProfileAccessFacts($Rules, [string]$SID, [string]$HostSID) {
    $facts = [ordered]@{ fresh_sid_full_control = $false; unexpected_root_write = $false; root_deny = $false }
    foreach ($rule in $Rules) {
        if (([long]$rule.rights -band 0x500d0156) -ne 0 -and $rule.allow -and $rule.sid -cnotin @($SID, $HostSID, 'S-1-5-18', 'S-1-5-32-544') -and -not ($rule.inherit_only -and $rule.sid -ceq 'S-1-3-0')) { $facts.unexpected_root_write = $true }
        if ($rule.inherit_only) { continue }
        if (-not $rule.allow) { $facts.root_deny = $true; continue }
        if ($rule.sid -ceq $SID -and ([long]$rule.rights -band 0x1f01ff) -eq 0x1f01ff) { $facts.fresh_sid_full_control = $true }
    }
    return $facts
}

function Convert-Nb1ProfileAce($Ace) {
    if ($Ace.kind -cne 'CommonAce' -or $Ace.callback -or $Ace.opaque -ne 0 -or $Ace.type -notin @(0,1) -or ([long]$Ace.flags -band (-bnot 0x1f)) -ne 0 -or $Ace.rights -lt 0 -or $Ace.rights -gt 0xffffffffL -or ([long]$Ace.rights -band (-bnot 0xf01f01ffL)) -ne 0) { throw 'unknown profile DACL ACE semantics' }
    return [ordered]@{ sid = $Ace.sid; allow = ($Ace.type -eq 0); rights = [long]$Ace.rights; inherit_only = (($Ace.flags -band 8) -ne 0) }
}

function Assert-Nb1RawDacl([bool]$Present, $Dacl) {
    if (-not $Present -or $null -eq $Dacl -or $Dacl.Count -eq 0 -or $Dacl.Count -gt 64) { throw 'profile raw DACL is missing or unknown' }
}

function Assert-Nb1ProfileOwner($State, $Snapshot, $Access) {
    if (-not $State.CreationReturned -or -not $State.CreationVerified -or -not $State.ProfileAbsentBeforeLogon -or -not $State.OrdinaryNativePassed -or -not $State.ProfileObservation.mapping_matches -or -not $Access.fresh_sid_full_control -or $Access.unexpected_root_write -or $Access.root_deny) { throw 'fresh profile provenance/access is not established' }
    if ($Snapshot.owner -ceq $State.SID) { return 'fresh-sid' }
    if ($Snapshot.owner -cne 'S-1-5-32-544') { throw 'fresh profile owner is not established' }
    return 'builtin-administrators-with-provenance-and-dacl'
}

function Set-Nb1PrivateDirectory([string]$Path, [string]$HostSID, [string]$UserSID, [Security.AccessControl.FileSystemRights]$UserRights) {
    $security = New-Object Security.AccessControl.DirectorySecurity
    $security.SetAccessRuleProtection($true, $false)
    $security.SetOwner((New-Object Security.Principal.SecurityIdentifier($HostSID)))
    foreach ($sid in @($HostSID, 'S-1-5-18')) {
        $principal = New-Object Security.Principal.SecurityIdentifier($sid)
        $rule = New-Object Security.AccessControl.FileSystemAccessRule($principal, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        [void]$security.AddAccessRule($rule)
    }
    $principal = New-Object Security.Principal.SecurityIdentifier($UserSID)
    $rule = New-Object Security.AccessControl.FileSystemAccessRule($principal, $UserRights, 'ContainerInherit,ObjectInherit', 'None', 'Allow')
    [void]$security.AddAccessRule($rule)
    Set-Acl -LiteralPath $Path -AclObject $security
}

function Get-Nb1Profile($State) {
    $key = [Microsoft.Win32.Registry]::LocalMachine.OpenSubKey('SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\' + $State.SID)
    $profile = @()
    try {
        $profile = @(Get-CimInstance -ClassName Win32_UserProfile -Filter ("SID='" + $State.SID + "'") -ErrorAction Stop)
        if ($null -eq $key) {
            if ($profile.Count -ne 0) { throw 'profile registry/provider disagree' }
            return $null
        }
        $raw = [string]$key.GetValue('ProfileImagePath', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        if ($profile.Count -ne 1 -or $profile[0].SID -cne $State.SID -or $profile[0].Special) { throw 'exact fresh SID profile is not established' }
        $path = [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($raw))
        $cimPath = [IO.Path]::GetFullPath([string]$profile[0].LocalPath)
        $State.ProfileObservation = [ordered]@{ sid = $State.SID; path = $path; cim_path = $cimPath; expected_path = $State.ExpectedProfile; mapping_matches = ($path -ieq $State.ExpectedProfile -and $cimPath -ieq $path); loaded = [bool]$profile[0].Loaded; root_owner = $null; root_id = $null }
        if (-not $State.ProfileObservation.mapping_matches) { throw 'profile path is not the exact fresh SID mapping' }
        $snapshot = Get-Nb1Snapshot $path -Directory
        $State.ProfileObservation.root_owner = $snapshot.owner
        $State.ProfileObservation.root_id = $snapshot.id
        [byte[]]$raw = [Nb1TokenFixture.Native]::SecurityDescriptor($path)
        Assert-Nb1Same $snapshot (Get-Nb1Snapshot $path -Directory) -IdentityOnly
        $hash = [Security.Cryptography.SHA256]::Create()
        try { $snapshot['raw_security_sha256'] = ([BitConverter]::ToString($hash.ComputeHash($raw))).Replace('-', '').ToLowerInvariant() } finally { $hash.Dispose() }
        $State.ProfileObservation['raw_security_sha256'] = $snapshot.raw_security_sha256
        if ($null -eq $State.ProfileSnapshot) { $State.ProfileSnapshot = $snapshot }
        Assert-Nb1Same $State.ProfileSnapshot $snapshot -IdentityOnly
        $security = [Security.AccessControl.RawSecurityDescriptor]::new($raw, 0)
        if ($security.Owner.Value -cne $snapshot.owner) { throw 'profile raw owner is unknown' }
        Assert-Nb1RawDacl (($security.ControlFlags -band [Security.AccessControl.ControlFlags]::DiscretionaryAclPresent) -ne 0) $security.DiscretionaryAcl
        $rules = @()
        foreach ($ace in $security.DiscretionaryAcl) {
            if ($ace -isnot [Security.AccessControl.CommonAce]) { throw 'unknown profile DACL ACE type' }
            $rules += Convert-Nb1ProfileAce @{ kind = 'CommonAce'; callback = $ace.IsCallback; opaque = $ace.OpaqueLength; type = [int]$ace.AceType; flags = [int]$ace.AceFlags; rights = ([long]$ace.AccessMask -band 0xffffffffL); sid = $ace.SecurityIdentifier.Value }
        }
        $access = Get-Nb1ProfileAccessFacts $rules $State.SID $State.HostSID
        $State.ProfileObservation['access'] = $access
        $State.ProfileObservation['absent_before_logon'] = [bool]$State.ProfileAbsentBeforeLogon
        $State.ProfileObservation['ordinary_native_passed'] = [bool]$State.OrdinaryNativePassed
        $State.ProfileObservation['owner_basis'] = Assert-Nb1ProfileOwner $State $snapshot $access
        return [ordered]@{ path = $path; loaded = [bool]$profile[0].Loaded; snapshot = $snapshot }
    } finally {
        if ($null -ne $key) { $key.Dispose() }
        foreach ($instance in $profile) { if ($instance -is [IDisposable]) { $instance.Dispose() } }
    }
}

function Assert-Nb1NativeReport($Value, [string]$Expected, [string]$SID) {
    if ($Expected -cne 'ordinary' -and $Expected -cne 'administrator-rejected') { throw 'unsupported native expectation' }
    $integers = @($Value.schema_version, $Value.collection_fields, $Value.facts.session_id, $Value.facts.token_is_elevated, $Value.facts.elevation_type, $Value.current.session_id, $Value.current.integrity_rid, $Value.current.elevation_type, $Value.handle_observation.completed, $Value.handle_observation.iterations, $Value.handle_observation.warmup_calls, $Value.handle_observation.baseline, $Value.handle_observation.final, $Value.handle_observation.peak, $Value.handle_observation.allowed_growth, $Value.handle_observation.budget_ms, $Value.handle_observation.settle_budget_ms, $Value.handle_observation.elapsed_ms)
    foreach ($number in $integers) { if (($number -isnot [int] -and $number -isnot [long]) -or $number -lt 0) { throw 'native report integer types are invalid' } }
    if ($Value.collection_complete -isnot [bool] -or $Value.collector_closed -isnot [bool] -or $Value.current.valid -isnot [bool] -or $Value.facts.interactive_enabled -isnot [bool] -or $Value.facts.service_or_batch -isnot [bool] -or $Value.facts.domain_rid_500 -isnot [bool] -or $Value.handle_observation.persistent_growth -isnot [bool]) { throw 'native report boolean types are invalid' }
    if ($Value.schema_version -ne 1 -or $Value.status -cne 'PASS' -or $Value.expected -cne $Expected -or $Value.expected_sid -cne $SID -or $Value.collection_fields -ne 6 -or -not $Value.collection_complete -or -not $Value.collector_closed) { throw 'native token collection/report did not PASS' }
    if ($Value.facts.sid -cne $SID -or $Value.facts.token_is_elevated -gt 1 -or $Value.handle_observation.completed -ne 32 -or $Value.handle_observation.iterations -ne 32 -or $Value.handle_observation.warmup_calls -ne 8 -or $Value.handle_observation.allowed_growth -ne 2 -or $Value.handle_observation.budget_ms -ne 5000 -or $Value.handle_observation.settle_budget_ms -ne 250 -or $Value.handle_observation.persistent_growth -or $Value.handle_observation.peak -lt $Value.handle_observation.baseline -or $Value.handle_observation.peak -lt $Value.handle_observation.final -or $Value.handle_observation.final -gt ($Value.handle_observation.baseline + 2)) { throw 'native identity or own-handle observation did not PASS' }
    if ($Expected -ceq 'ordinary') {
        if (-not $Value.current.valid -or $Value.current.sid -cne $SID -or $Value.current.task_path -cne ('\B1UG-nb1-' + $SID) -or $Value.facts.token_is_elevated -ne 0 -or $Value.facts.integrity_sid -cne 'S-1-16-8192' -or -not $Value.facts.interactive_enabled -or $Value.facts.service_or_batch -or $Value.facts.session_id -eq 0 -or $Value.facts.admin_group -cne 'none' -or $Value.facts.elevation_type -ne 1 -or $Value.current.session_id -ne $Value.facts.session_id -or $Value.current.integrity_rid -ne 8192 -or $Value.current.elevation_type -ne $Value.facts.elevation_type -or $Value.current.admin_membership -cne $Value.facts.admin_group) { throw 'new ordinary user native facts are not the required minimal token' }
    } elseif ($Value.current.valid -or $Value.current.sid -cne '' -or $Value.current.task_path -cne '' -or $Value.current.session_id -ne 0 -or $Value.current.integrity_rid -ne 0 -or $Value.current.elevation_type -ne 0 -or $Value.current.admin_membership -cne '' -or (-not $Value.facts.domain_rid_500 -and $Value.facts.token_is_elevated -eq 0)) { throw 'administrator rejection lacks actual elevated/RID500 evidence and zero Context' }
}

function Invoke-Nb1NativeCase($State, $Report, [string]$Expected, [string]$SID, [string]$Executable, [string]$WorkingDirectory, $Credential) {
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $Executable
    $start.Arguments = '-test.run=^TestNativeCurrentOptIn$ -test.v -test.count=1'
    $start.WorkingDirectory = $WorkingDirectory
    $start.UseShellExecute = $false
    $start.RedirectStandardOutput = $true; $start.RedirectStandardError = $true
    $start.CreateNoWindow = $true
    $start.EnvironmentVariables.Clear()
    $environment = [ordered]@{
        SystemRoot = $State.WindowsDirectory; WINDIR = $State.WindowsDirectory
        PATH = $State.SystemDirectory
        TEMP = $WorkingDirectory; TMP = $WorkingDirectory
        NB1_TASKUSER_NATIVE = '1'; NB1_TASKUSER_EXPECT = $Expected; NB1_TASKUSER_EXPECT_SID = $SID
    }
    foreach ($key in $environment.Keys) { $start.EnvironmentVariables[$key] = $environment[$key] }
    if ($null -ne $Credential) {
        $start.UserName = $State.Name; $start.Domain = [Environment]::MachineName
        $start.Password = $Credential; $start.LoadUserProfile = $true
    }
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    $child = @{ Process = $process; Started = $false; Unknown = $true; Exited = $false; Expected = $Expected }
    [void]$State.Children.Add($child)
    if (-not $process.Start()) { throw 'private test process did not start' }
    $child.Started = $true; $child.Unknown = $false
    [void]$process.Handle # retain the exact private process handle; never find/kill by text
    $stdout = $process.StandardOutput.ReadToEndAsync(); $stderr = $process.StandardError.ReadToEndAsync()
    if (-not $process.WaitForExit(40000)) {
        $process.Kill()
        if (-not $process.WaitForExit(10000)) { throw 'private timed-out child exit remains unknown' }
        $child.Exited = $true
        throw 'private child exceeded observation budget; not token acceptance'
    }
    $process.WaitForExit(); $child.Exited = $true
    $text = $stdout.GetAwaiter().GetResult(); $errorText = $stderr.GetAwaiter().GetResult()
    if ($text.Length -gt 131072 -or $errorText.Length -gt 131072) { throw 'private output exceeded evidence budget' }
    $lines = @($text -split '\r?\n' | Where-Object { $_.StartsWith('NB1_TASKUSER_NATIVE_RESULT ', [StringComparison]::Ordinal) })
    $diagnostics = @($text -split '\r?\n' | Where-Object { $_ -match '^\s*context_native_windows_test\.go:\d+: ' -or $_ -match '^(--- (PASS|FAIL|SKIP): TestNativeCurrentOptIn |(PASS|FAIL)$)' })
    $value = $null
    if ($lines.Count -eq 1) { $value = $lines[0].Substring('NB1_TASKUSER_NATIVE_RESULT '.Length) | ConvertFrom-Json }
    $hash = [Security.Cryptography.SHA256]::Create()
    try { $stderrHash = ([BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes($errorText)))).Replace('-', '').ToLowerInvariant() } finally { $hash.Dispose() }
    $Report.cases += @([ordered]@{ expected = $Expected; exit_code = $process.ExitCode; environment_keys = @($environment.Keys); report_lines = $lines.Count; stderr_chars = $errorText.Length; stderr_sha256 = $stderrHash; native_diagnostics = $diagnostics; native = $value })
    if ($process.ExitCode -ne 0 -or $lines.Count -ne 1) { throw 'native child failed or did not emit exactly one result' }
    Assert-Nb1NativeReport $value $Expected $SID
}

function New-Nb1NativeOps {
    return @{
        Preflight = {
            param($s,$r)
            if ($env:GITHUB_ACTIONS -cne 'true' -or [Environment]::OSVersion.Platform -ne 'Win32NT' -or -not [Environment]::Is64BitProcess -or $PSVersionTable.PSVersion.Major -lt 5) { throw 'ENVIRONMENT_UNAVAILABLE: dedicated 64-bit hosted Windows PowerShell required' }
            Assert-Nb1FixtureIdentity $s.Name $s.Marker
            $r.fixture_identity_lengths = [ordered]@{ name = $s.Name.Length; description = $s.Marker.Length }
            Initialize-Nb1NativeTypes
            Import-Module Microsoft.PowerShell.LocalAccounts -ErrorAction Stop
            $s.SystemDirectory = [Environment]::GetFolderPath('System')
            $s.WindowsDirectory = [IO.Path]::GetDirectoryName($s.SystemDirectory)
            Assert-Nb1LocalAbsolutePath $s.SystemDirectory
            $identity = [Security.Principal.WindowsIdentity]::GetCurrent([Security.Principal.TokenAccessLevels]::Query)
            try { $s.HostSID = $identity.User.Value } finally { $identity.Dispose() }
            if ($s.HostSID -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$') { throw 'ENVIRONMENT_UNAVAILABLE: hosted local administrator identity not established' }
            $s.SourceSnapshot = Get-Nb1Snapshot $s.Binary
            $s.BinaryHash = $s.SourceSnapshot.sha256
            $s.ProfilesRoot = [IO.Path]::GetFullPath([Nb1TokenFixture.Native]::ProfilesRoot())
            $s.ProfilesRootSnapshot = Get-Nb1Snapshot $s.ProfilesRoot -Directory
            $s.ExpectedProfile = Join-Path $s.ProfilesRoot $s.Name
            $s.StageParentSnapshot = Get-Nb1Snapshot ([IO.Path]::GetDirectoryName($s.StageRoot)) -Directory
            $r.fixture_paths = [ordered]@{ stage_root = $s.StageRoot; expected_profile = $s.ExpectedProfile }
            if ($null -ne (Get-Nb1LocalUser $s.Name) -or [Nb1TokenFixture.Native]::ExistsNoFollow($s.ExpectedProfile) -or [Nb1TokenFixture.Native]::ExistsNoFollow($s.StageRoot)) { throw 'fixture name/profile/directory already exists' }
            $s.Password = [Nb1TokenFixture.Native]::Password()
            $r.binary_sha256 = $s.BinaryHash
        }
        AdministratorCase = {
            param($s,$r)
            Assert-Nb1Same $s.SourceSnapshot (Get-Nb1Snapshot $s.Binary)
            Invoke-Nb1NativeCase $s $r 'administrator-rejected' $s.HostSID $s.Binary $s.SystemDirectory $null
        }
        CreateUser = { param($s,$r) [void](New-LocalUser -Name $s.Name -Password $s.Password -Description $s.Marker -ErrorAction Stop) }
        ClaimUser = {
            param($s,$r)
            $user = Get-Nb1LocalUser $s.Name
            if ($null -eq $user) { return $false }
            $sid = $user.SID.Value
            $r.account_observation = [ordered]@{ exists = $true; sid = $sid; marker_matches = ($user.Description -ceq $s.Marker) }
            if ($user.Description -cne $s.Marker -or $sid -notmatch '^S-1-5-21-\d+-\d+-\d+-(\d+)$' -or [uint32]$Matches[1] -lt 1000 -or $sid -ceq $s.HostSID) { throw 'fresh account marker/SID attribution failed' }
            $reverse = Get-LocalUser -SID $user.SID -ErrorAction Stop
            if ($reverse.Name -cne $s.Name -or $reverse.Description -cne $s.Marker -or ($s.CreationVerified -and $s.SID -cne $sid)) { throw 'fresh account reverse SID identity changed' }
            $s.SID = $sid; $s.CreationVerified = $true
            $r.fixture_user = [ordered]@{ name = $s.Name; sid = $sid; marker_sha256 = $s.MarkerHash }
            return $true
        }
        EnsureGroups = {
            param($s,$r)
            $lookup = { param($name) Get-LocalGroup -Name $name -ErrorAction Stop }
            $groups = @(Resolve-Nb1LocalGroupSIDs -Names @([Nb1TokenFixture.Native]::LocalGroupNames($s.Name)) -Lookup $lookup)
            if ($groups.Count -eq 0) {
                $users = Get-LocalGroup -SID (New-Object Security.Principal.SecurityIdentifier('S-1-5-32-545')) -ErrorAction Stop
                Add-LocalGroupMember -Group $users -Member (Get-LocalUser -SID (New-Object Security.Principal.SecurityIdentifier($s.SID))) -ErrorAction Stop
                $groups = @(Resolve-Nb1LocalGroupSIDs -Names @([Nb1TokenFixture.Native]::LocalGroupNames($s.Name)) -Lookup $lookup)
            }
            Assert-Nb1UsersOnly $groups
            $r.fixture_user['local_group_sids'] = $groups
        }
        StageFixture = {
            param($s,$r)
            Assert-Nb1Same $s.SourceSnapshot (Get-Nb1Snapshot $s.Binary)
            Assert-Nb1Same $s.ProfilesRootSnapshot (Get-Nb1Snapshot $s.ProfilesRoot -Directory) -IdentityOnly
            Assert-Nb1Same $s.StageParentSnapshot (Get-Nb1Snapshot ([IO.Path]::GetDirectoryName($s.StageRoot)) -Directory) -IdentityOnly
            if ([Nb1TokenFixture.Native]::ExistsNoFollow($s.StageRoot)) { throw 'fixture path appeared before exclusive staging' }
            foreach ($path in @($s.StageRoot, $s.BinDir, $s.TempDir)) { [Nb1TokenFixture.Native]::MakeDirectoryExclusive($path) }
            Set-Nb1PrivateDirectory $s.StageRoot $s.HostSID $s.SID 'ReadAndExecute'
            Set-Nb1PrivateDirectory $s.BinDir $s.HostSID $s.SID 'ReadAndExecute'
            Set-Nb1PrivateDirectory $s.TempDir $s.HostSID $s.SID 'Modify'
            [IO.File]::Copy($s.Binary, $s.StagedBinary, $false)
            $s.StagedSnapshot = Get-Nb1Snapshot $s.StagedBinary
            if ($s.StagedSnapshot.sha256 -cne $s.BinaryHash) { throw 'staged test binary hash mismatch' }
            $s.StageSnapshots = @{}
            foreach ($path in @($s.StageRoot, $s.BinDir, $s.TempDir)) { $s.StageSnapshots[$path] = Get-Nb1Snapshot $path -Directory }
            $s.StageVerified = $true
        }
        OrdinaryCase = {
            param($s,$r)
            Assert-Nb1Same $s.StagedSnapshot (Get-Nb1Snapshot $s.StagedBinary)
            if ($null -ne (Get-Nb1Profile $s)) { throw 'new user already has a profile before fixture logon' }
            if ([Nb1TokenFixture.Native]::ExistsNoFollow($s.ExpectedProfile)) { throw 'unmapped profile path exists before fixture logon' }
            $s.ProfileAbsentBeforeLogon = $true
            Invoke-Nb1NativeCase $s $r 'ordinary' $s.SID $s.StagedBinary $s.TempDir $s.Password
            $s.OrdinaryNativePassed = $true
            $profile = Get-Nb1Profile $s
            if ($null -eq $profile) { throw 'new user profile mapping was not established' }
            $s.ProfileSnapshot = $profile.snapshot
            $r.profile_observation = [ordered]@{ sid = $s.SID; path = $profile.path; root_id = $profile.snapshot.id; loaded = $profile.loaded }
        }
        ReapChildren = {
            param($s,$r)
            foreach ($child in $s.Children) {
                if ($child.Unknown -or -not $child.Started) { throw 'process start result is unknown; no account/profile cleanup' }
                if (-not $child.Process.HasExited) {
                    $child.Process.Kill()
                    if (-not $child.Process.WaitForExit(10000)) { throw 'private process remains alive' }
                }
                $child.Exited = $true
                $child.Process.Dispose()
            }
            $s.ChildrenExited = $true
        }
        CleanupResources = {
            param($s,$r)
            if (-not $s.ChildrenExited) { throw 'cannot clean resources before private exit' }
            if ([Nb1TokenFixture.Native]::ExistsNoFollow($s.StageRoot)) {
                if (-not $s.StageVerified) { throw 'partially created fixture directory is unverified; preserve it' }
                foreach ($path in @($s.StageRoot, $s.BinDir, $s.TempDir)) { Assert-Nb1Same $s.StageSnapshots[$path] (Get-Nb1Snapshot $path -Directory) -IdentityOnly }
                $rootNames = @((Get-ChildItem -LiteralPath $s.StageRoot -Force).Name | Sort-Object)
                if (($rootNames -join ',') -cne 'bin,tmp' -or @(Get-ChildItem -LiteralPath $s.BinDir -Force).Count -ne 1 -or @(Get-ChildItem -LiteralPath $s.TempDir -Force).Count -ne 0) { throw 'unknown fixture files; preserve directory and account' }
                Assert-Nb1Same $s.StagedSnapshot (Get-Nb1Snapshot $s.StagedBinary)
            }
            Assert-Nb1Same $s.ProfilesRootSnapshot (Get-Nb1Snapshot $s.ProfilesRoot -Directory) -IdentityOnly
            $profile = Get-Nb1Profile $s
            if ($null -ne $profile) {
                if ($null -eq $s.ProfileSnapshot) { $s.ProfileSnapshot = $profile.snapshot }
                Assert-Nb1Same $s.ProfileSnapshot $profile.snapshot -IdentityOnly
                $deadline = [DateTime]::UtcNow.AddSeconds(15)
                while ($profile.loaded -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 200; $profile = Get-Nb1Profile $s; if ($null -eq $profile) { throw 'profile disappeared while waiting for unload' }; Assert-Nb1Same $s.ProfileSnapshot $profile.snapshot -IdentityOnly }
                if ($profile.loaded) { throw 'profile remains loaded; no forced hive unload' }
                [Nb1TokenFixture.Native]::DeleteProfile($s.SID)
                if ($null -ne (Get-Nb1Profile $s) -or ([Nb1TokenFixture.Native]::ExistsNoFollow($s.ExpectedProfile))) { throw 'exact fixture profile remains after OS deletion' }
            } elseif ([Nb1TokenFixture.Native]::ExistsNoFollow($s.ExpectedProfile)) { throw 'unmapped or replaced profile path; preserve it' }
            if ($s.StageVerified) {
                Assert-Nb1Same $s.StagedSnapshot (Get-Nb1Snapshot $s.StagedBinary)
                [IO.File]::Delete($s.StagedBinary)
                foreach ($path in @($s.BinDir, $s.TempDir, $s.StageRoot)) {
                    Assert-Nb1Same $s.StageSnapshots[$path] (Get-Nb1Snapshot $path -Directory) -IdentityOnly
                    if (@(Get-ChildItem -LiteralPath $path -Force).Count -ne 0) { throw 'fixture directory is not empty; no recursive delete' }
                    [IO.Directory]::Delete($path, $false)
                }
            }
        }
        RemoveUser = {
            param($s,$r)
            $user = Get-Nb1LocalUser $s.Name
            if ($null -eq $user -or $user.SID.Value -cne $s.SID -or $user.Description -cne $s.Marker) { throw 'fresh account changed before exact removal' }
            Remove-LocalUser -SID $user.SID -Confirm:$false -ErrorAction Stop
        }
        FinalAbsence = {
            param($s,$r)
            if ($null -ne (Get-Nb1LocalUser $s.Name) -or ([Nb1TokenFixture.Native]::ExistsNoFollow($s.StageRoot)) -or ([Nb1TokenFixture.Native]::ExistsNoFollow($s.ExpectedProfile)) -or $null -ne (Get-Nb1Profile $s)) { throw 'fresh account/profile/fixture remnants remain' }
        }
    }
}

if ($LibraryOnly) { return }
$report = [ordered]@{ schema_version = 1; status = 'PREPARED_NOT_RUN'; hardware_access = $false; task_mutations = $false; policy_changes = $false; cases = @(); checks = @(); cleanup = @(); scope = 'Native own-process token classification and bounded handle observation only; no Task Scheduler or USB acceptance.' }
if (-not $Execute) { $report | ConvertTo-Json -Depth 12; return }
Assert-Nb1LocalAbsolutePath $Binary
Assert-Nb1LocalAbsolutePath $OutputDirectory
if ($env:GITHUB_ACTIONS -cne 'true' -or [Environment]::OSVersion.Platform -ne 'Win32NT') { throw 'ENVIRONMENT_UNAVAILABLE: execute is restricted to a disposable hosted Windows VM' }
if (Test-Path -LiteralPath $OutputDirectory) { throw 'evidence directory already exists' }
[void][IO.Directory]::CreateDirectory($OutputDirectory)
$fixtureIdentity = New-Nb1FixtureIdentity
$name = $fixtureIdentity.Name
$marker = $fixtureIdentity.Marker
$markerBytes = [Text.Encoding]::UTF8.GetBytes($marker)
$sha = [Security.Cryptography.SHA256]::Create()
try { $markerHash = ([BitConverter]::ToString($sha.ComputeHash($markerBytes))).Replace('-', '').ToLowerInvariant() } finally { $sha.Dispose() }
$stageRoot = Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) ('nb1-token-ci-' + $name)
$state = @{ Name = $name; Marker = $marker; MarkerHash = $markerHash; SID = $null; HostSID = $null; Binary = [IO.Path]::GetFullPath($Binary); StageRoot = $stageRoot; BinDir = (Join-Path $stageRoot 'bin'); TempDir = (Join-Path $stageRoot 'tmp'); StagedBinary = (Join-Path $stageRoot 'bin\taskuser-native.test.exe'); Password = $null; Children = (New-Object Collections.ArrayList); CreationAttempted = $false; CreationVerified = $false; CreationReturned = $false; ChildrenExited = $false; StageVerified = $false; ProfileSnapshot = $null; ProfileAbsentBeforeLogon = $false; OrdinaryNativePassed = $false }
try { $report = Invoke-Nb1TokenLifecycle $state $report (New-Nb1NativeOps) }
finally { if ($null -ne $state.Password) { $state.Password.Dispose() } }
$encoded = $report | ConvertTo-Json -Depth 16
[IO.File]::WriteAllText((Join-Path $OutputDirectory 'results.json'), $encoded, (New-Object Text.UTF8Encoding($false)))
Write-Output $encoded
if ($report.status -cne 'PASS') { exit 1 }
