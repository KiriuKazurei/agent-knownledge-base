param([Parameter(Mandatory = $true)][string]$InstallationRoot, [switch]$CloseForInspection)
$resolvedTestRoot = (Resolve-Path -LiteralPath $InstallationRoot).Path
if ($resolvedTestRoot -notlike '*\.run-data\updater-acceptance\installed') { throw 'Only isolated updater acceptance installation allowed' }
Add-Type @'
using System;
using System.Text;
using System.Collections.Generic;
using System.Runtime.InteropServices;
public class TestWindowInfo { public int pid; public long handle; public string title; public bool visible; }
public static class TestWindowVisibility {
  private delegate bool EnumProc(IntPtr handle, IntPtr parameter);
  [DllImport("user32.dll")] private static extern bool EnumWindows(EnumProc callback, IntPtr parameter);
  [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr handle, out uint pid);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] private static extern int GetWindowText(IntPtr handle, StringBuilder text, int length);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr handle);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr handle, uint message, IntPtr wParam, IntPtr lParam);
  public static TestWindowInfo[] Find(int[] processIds) {
    var ids = new HashSet<int>(processIds); var result = new List<TestWindowInfo>();
    EnumWindows((handle, unused) => { uint pid; GetWindowThreadProcessId(handle, out pid);
      if (ids.Contains((int)pid)) { var title = new StringBuilder(512); GetWindowText(handle, title, title.Capacity);
        result.Add(new TestWindowInfo { pid=(int)pid, handle=handle.ToInt64(), title=title.ToString(), visible=IsWindowVisible(handle) }); }
      return true;
    }, IntPtr.Zero); return result.ToArray();
  }
}
'@
$windows = @(Get-Process | Where-Object { $_.Path -and $_.Path.StartsWith($resolvedTestRoot + '\', [StringComparison]::OrdinalIgnoreCase) -and $_.MainWindowHandle -ne 0 } | ForEach-Object {
  [PSCustomObject]@{ pid = $_.Id; title = $_.MainWindowTitle; visible = [TestWindowVisibility]::IsWindowVisible($_.MainWindowHandle) }
})
$testProcessIds = @(Get-Process -Name 'Knowledge Agent Hub Update Test' -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path.StartsWith($resolvedTestRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { $_.Id })
$windows = @([TestWindowVisibility]::Find([int[]]$testProcessIds))
if ($CloseForInspection) { foreach ($window in $windows) { if ($window.visible -and $window.title -eq 'Knowledge Agent Hub') { [void][TestWindowVisibility]::PostMessage([IntPtr]$window.handle, 0x10, [IntPtr]::Zero, [IntPtr]::Zero) } } }
ConvertTo-Json -InputObject $windows -Compress
