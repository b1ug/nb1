param([Parameter(Mandatory = $true)][string]$OutputPath)

# Read-only CI host facts. This is not the product identity guard, an account
# fixture, a task mutation, or proof of a standard user's interactive session.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$report = [ordered]@{
    schema_version = 1
    scope = 'Selected own-token and exact Task Scheduler lookup facts on a disposable hosted VM. Not product lifecycle acceptance.'
    hardware_access = $false
    task_mutations = $false
    accounts_changed = $false
    status = 'FAIL'
}
$identity = $null
$scheduler = $null
$folder = $null
$task = $null
$collectionPhase = 'token-helper-definition'
try {
    Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
namespace Nb1HostProbe {
    public static class TokenFacts {
        [DllImport("kernel32.dll")] static extern IntPtr GetCurrentProcess();
        [DllImport("advapi32.dll", SetLastError=true)]
        static extern bool OpenProcessToken(IntPtr process, uint access, out IntPtr token);
        [DllImport("advapi32.dll", SetLastError=true)]
        static extern bool GetTokenInformation(IntPtr token, int kind, out uint value, uint length, out uint returned);
        [DllImport("kernel32.dll", SetLastError=true)] static extern bool CloseHandle(IntPtr handle);
        public static uint[] Read() {
            IntPtr token;
            if (!OpenProcessToken(GetCurrentProcess(), 0x0008, out token))
                throw new Win32Exception(Marshal.GetLastWin32Error(), "own TOKEN_QUERY open failed");
            try {
                int[] kinds = {12, 18, 20}; // SessionId, ElevationType, Elevation
                uint[] values = new uint[kinds.Length];
                for (int i = 0; i < kinds.Length; i++) {
                    uint returned;
                    if (!GetTokenInformation(token, kinds[i], out values[i], 4, out returned) || returned != 4)
                        throw new Win32Exception(Marshal.GetLastWin32Error(), "own token scalar query failed");
                }
                return values;
            } finally {
                if (!CloseHandle(token))
                    throw new Win32Exception(Marshal.GetLastWin32Error(), "own token close failed");
            }
        }
    }
}
'@
    $collectionPhase = 'token-scalars'
    $facts = [Nb1HostProbe.TokenFacts]::Read()
    $collectionPhase = 'identity-and-known-group-presence'
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent([Security.Principal.TokenAccessLevels]::Query)
    $sid = $identity.User.Value
    # Only these four known group-presence facts leave the host. Presence is
    # not enabled/deny-only membership and does not prove the product guard.
    $known = [ordered]@{}
    foreach ($item in @('S-1-5-4', 'S-1-5-6', 'S-1-5-3', 'S-1-5-32-544')) {
        $known[$item] = @($identity.Groups | Where-Object { $_.Value -eq $item }).Count -gt 0
    }
    $report['host'] = [ordered]@{
        sid = $sid
        token_session_id = $facts[0]
        token_elevation_type = $facts[1]
        token_elevated = ($facts[2] -ne 0)
        known_group_presence = $known
        powershell_version = $PSVersionTable.PSVersion.ToString()
    }
    $path = '\B1UG-nb1-' + $sid
    $phase = 'connect'
    try {
        $scheduler = New-Object -ComObject 'Schedule.Service'
        $scheduler.Connect()
        $phase = 'root-folder'
        $folder = $scheduler.GetFolder('\')
        $phase = 'exact-task'
        $task = $folder.GetTask($path)
        $report['task_lookup'] = [ordered]@{
            status = 'present'
            phase = $phase
            path_matches = ($task.Path -ceq $path)
            state = [int]$task.State
            enabled = [bool]$task.Enabled
        }
    } catch {
        $exception = $_.Exception
        while ($null -ne $exception.InnerException) { $exception = $exception.InnerException }
        $unsigned = [uint32]([int64]$exception.HResult -band 0xffffffffL)
        $report['task_lookup'] = [ordered]@{
            status = 'lookup-error-observed'
            phase = $phase
            hresult = ('0x{0:x8}' -f $unsigned)
            # No broad missing-task claim before the ordinary-user contract.
            absence_verified_for_product = $false
        }
    }
    $report['status'] = 'OBSERVED'
} catch {
    # Do not export exception strings, XML, arguments, environment or identity
    # inventories. A failed token collection keeps the diagnostic job red.
    $report['collection_failed'] = $true
    $exception = $_.Exception
    while ($null -ne $exception.InnerException) { $exception = $exception.InnerException }
    $report['collection_phase'] = $collectionPhase
    $report['collection_hresult'] = ('0x{0:x8}' -f [uint32]([int64]$exception.HResult -band 0xffffffffL))
    $report['collection_exception_type'] = $exception.GetType().FullName
} finally {
    foreach ($object in @($task, $folder, $scheduler)) {
        if ($null -ne $object -and [Runtime.InteropServices.Marshal]::IsComObject($object)) {
            try { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($object) }
            catch { $report['cleanup_failed'] = $true; $report['status'] = 'FAIL' }
        }
    }
    if ($null -ne $identity) {
        try { $identity.Dispose() }
        catch { $report['cleanup_failed'] = $true; $report['status'] = 'FAIL' }
    }
}
$encoded = $report | ConvertTo-Json -Depth 6
[IO.File]::WriteAllText($OutputPath, $encoded, [Text.UTF8Encoding]::new($false))
Write-Output $encoded
if ($report.status -ne 'OBSERVED') { exit 1 }
