param(
  [string]$VirtioDrive = "",
  [switch]$StartService,
  [switch]$EnableStartup,
  [switch]$Force
)

$ErrorActionPreference = 'Stop'

function Find-VirtioGuestAgentInstaller {
  param([string]$DriveHint)

  $candidates = @()
  if ($DriveHint) {
    $candidates += $DriveHint
  }

  $candidates += (Get-PSDrive -PSProvider FileSystem | Select-Object -ExpandProperty Root)

  foreach ($root in $candidates | Select-Object -Unique) {
    foreach ($relative in @(
      'guest-agent\qemu-ga-x86_64.msi',
      'guest-agent\qemu-ga.msi',
      'guest-agent\qemu-ga-x86.msi'
    )) {
      $path = Join-Path $root $relative
      if (Test-Path $path) {
        return $path
      }
    }
  }

  throw 'Could not find qemu-ga MSI. Mount the virtio-win ISO (or pass -VirtioDrive D:\) and retry.'
}

$service = Get-Service -Name 'QEMU-GA' -ErrorAction SilentlyContinue
if ($service -and -not $Force) {
  Write-Host 'QEMU Guest Agent is already installed. Use -Force to reinstall.'
} else {
  $msi = Find-VirtioGuestAgentInstaller -DriveHint $VirtioDrive
  Write-Host "Installing QEMU Guest Agent from $msi"
  $proc = Start-Process msiexec.exe -ArgumentList @('/i', ('"' + $msi + '"'), '/qn', '/norestart') -Wait -PassThru
  if ($proc.ExitCode -ne 0) {
    throw "msiexec failed with exit code $($proc.ExitCode)"
  }
}

$service = Get-Service -Name 'QEMU-GA' -ErrorAction Stop
if ($EnableStartup) {
  Set-Service -Name 'QEMU-GA' -StartupType Automatic
}
if ($StartService -or $service.Status -ne 'Running') {
  Start-Service -Name 'QEMU-GA'
}

Write-Host 'QEMU Guest Agent installed.'
Write-Host 'Suggested host-side checks:'
Write-Host '  vmsmith vm list            # IP should now come from the guest agent when available'
Write-Host '  vmsmith host stats         # balloon metrics become available once the agent reports them'
Write-Host 'Inside Windows, verify: Get-Service QEMU-GA'
