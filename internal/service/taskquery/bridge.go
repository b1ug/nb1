package taskquery

// This fixed script is passed as one argv value. JSON is data on stdin, never
// interpolated into code. It uses .NET JSON/COM APIs, not module auto-loading.
const queryScript = `
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$PSModuleAutoLoadingPreference = 'None'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false)
$service = $null; $folder = $null; $task = $null; $identity = $null
$caller = ''; $path = ''; $stage = 'identity'; $answer = $null
try {
  $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent([System.Security.Principal.TokenAccessLevels]::Query)
  $caller = $identity.User.Value
  $path = '\B1UG-nb1-' + $caller
  $stage = 'bootstrap'
  [void][System.Reflection.Assembly]::Load('System.Web.Extensions, Version=4.0.0.0, Culture=neutral, PublicKeyToken=31bf3856ad364e35')
  $json = [System.Web.Script.Serialization.JavaScriptSerializer]::new()
  $json.MaxJsonLength = 4096; $json.RecursionLimit = 8
  $stage = 'input'
  $raw = [Console]::In.ReadToEnd()
  if ($raw.Length -gt 4096) { throw [System.InvalidOperationException]::new() }
  $request = $json.DeserializeObject($raw)
  if ($request -isnot [System.Collections.Generic.Dictionary[string,object]] -or $request.Count -ne 3 -or
      $request['schema_version'] -isnot [int] -or $request['schema_version'] -ne 1 -or
      $request['sid'] -isnot [string] -or $request['sid'] -cne $caller -or
      $request['task_path'] -isnot [string] -or $request['task_path'] -cne $path) {
    throw [System.InvalidOperationException]::new()
  }
  $stage = 'activate'
  $service = [System.Activator]::CreateInstance([Type]::GetTypeFromProgID('Schedule.Service', $true))
  $stage = 'connect'; $service.Connect()
  $stage = 'folder'; $folder = $service.GetFolder('\')
  $stage = 'get_task'; $task = $folder.GetTask($path)
  $stage = 'properties'
  if (-not [String]::Equals([string]$task.Path, $path, [StringComparison]::OrdinalIgnoreCase)) {
    throw [System.InvalidOperationException]::new()
  }
  $answer = @{schema_version=1; outcome='present'; caller_sid=$caller; task_path=$path; stage='complete';
              enabled=[bool]$task.Enabled; scheduler_state=[int]$task.State; last_task_result=[int]$task.LastTaskResult}
} catch {
  $exception = $_.Exception; $code = [int]$exception.HResult
  for ($depth = 0; $depth -lt 8 -and $null -ne $exception; $depth++) {
    if ($exception -is [System.Runtime.InteropServices.COMException]) { $code = [int]$exception.HResult; break }
    $exception = $exception.InnerException
  }
  $hex = ([long]$code -band 4294967295).ToString('X8')
  $answer = @{schema_version=1; outcome='error'; caller_sid=$caller; task_path=$path; stage=$stage; hresult=('0x' + $hex)}
} finally {
  $releaseFailed = $false
  foreach ($item in @($task, $folder, $service)) {
    if ($null -ne $item) {
      try { [void][System.Runtime.InteropServices.Marshal]::FinalReleaseComObject($item) }
      catch { $releaseFailed = $true }
    }
  }
  if ($null -ne $identity) { $identity.Dispose() }
  if ($releaseFailed) {
    $answer = @{schema_version=1; outcome='error'; caller_sid=$caller; task_path=$path; stage='release'; hresult='0x80004005'}
  }
}
# Avoid raw exception formatting. Even bootstrap failures emit only fixed data.
if ($null -ne $json) { [Console]::Out.Write($json.Serialize($answer)) }
else { [Console]::Out.Write('{"schema_version":1,"outcome":"error","caller_sid":"","task_path":"","stage":"bootstrap","hresult":"0x80004005"}') }
`
