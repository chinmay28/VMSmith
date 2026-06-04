param(
  [string]$CloudbaseInitInstaller = "",
  [string]$VirtioDrive = "",
  [string]$UnattendTemplate = "",
  [string]$SysprepOutput = "C:\\Windows\\Panther\\Unattend\\VMSmith-Unattend.xml",
  [switch]$InstallQemuGA,
  [switch]$EnableOpenSSH,
  [switch]$SkipSysprep,
  [switch]$Force
)

$ErrorActionPreference = 'Stop'

function Resolve-ExistingPath {
  param(
    [string]$Explicit,
    [string[]]$Candidates,
    [string]$Label
  )

  if ($Explicit) {
    if (-not (Test-Path -LiteralPath $Explicit)) {
      throw "$Label not found: $Explicit"
    }
    return (Resolve-Path -LiteralPath $Explicit).Path
  }

  foreach ($candidate in $Candidates) {
    if (-not $candidate) { continue }
    if (Test-Path -LiteralPath $candidate) {
      return (Resolve-Path -LiteralPath $candidate).Path
    }
  }

  throw "Could not find $Label. Pass it explicitly and retry."
}

function Find-CloudbaseInitInstaller {
  param([string]$Explicit)

  $downloads = Join-Path $env:USERPROFILE 'Downloads'
  $candidates = @(
    (Join-Path $downloads 'CloudbaseInitSetup_Stable_x64.msi'),
    (Join-Path $downloads 'CloudbaseInitSetup_x64.msi'),
    (Join-Path $downloads 'CloudbaseInitSetup_Stable_x86.msi')
  )

  return Resolve-ExistingPath -Explicit $Explicit -Candidates $candidates -Label 'cloudbase-init installer'
}

function Find-QemuGuestAgentInstaller {
  param([string]$DriveHint)

  $candidates = @()
  if ($DriveHint) {
    $candidates += $DriveHint
  }
  $candidates += (Get-PSDrive -PSProvider FileSystem | Select-Object -ExpandProperty Root)

  foreach ($root in $candidates | Select-Object -Unique) {
    foreach ($relative in @(
      'guest-agent\\qemu-ga-x86_64.msi',
      'guest-agent\\qemu-ga.msi',
      'guest-agent\\qemu-ga-x86.msi'
    )) {
      $path = Join-Path $root $relative
      if (Test-Path -LiteralPath $path) {
        return (Resolve-Path -LiteralPath $path).Path
      }
    }
  }

  throw 'Could not find qemu-ga MSI. Mount the virtio-win ISO (or pass -VirtioDrive D:\\) and retry.'
}

function Install-MSI {
  param(
    [string]$InstallerPath,
    [string]$Label
  )

  Write-Host "Installing $Label from $InstallerPath"
  $proc = Start-Process msiexec.exe -ArgumentList @('/i', ('"' + $InstallerPath + '"'), '/qn', '/norestart') -Wait -PassThru
  if ($proc.ExitCode -ne 0) {
    throw "$Label installer failed with exit code $($proc.ExitCode)"
  }
}

function Install-CloudbaseInit {
  param([string]$InstallerPath)

  $service = Get-Service -Name 'cloudbase-init' -ErrorAction SilentlyContinue
  if ($service -and -not $Force) {
    Write-Host 'cloudbase-init is already installed. Use -Force to reinstall.'
    return
  }

  Install-MSI -InstallerPath $InstallerPath -Label 'cloudbase-init'
}

function Install-QemuGuestAgent {
  param([string]$DriveHint)

  $service = Get-Service -Name 'QEMU-GA' -ErrorAction SilentlyContinue
  if ($service -and -not $Force) {
    Write-Host 'QEMU Guest Agent is already installed. Use -Force to reinstall.'
  } else {
    $msi = Find-QemuGuestAgentInstaller -DriveHint $DriveHint
    Install-MSI -InstallerPath $msi -Label 'QEMU Guest Agent'
  }

  Set-Service -Name 'QEMU-GA' -StartupType Automatic
  if ((Get-Service -Name 'QEMU-GA').Status -ne 'Running') {
    Start-Service -Name 'QEMU-GA'
  }
}

function Ensure-OpenSSH {
  $capability = Get-WindowsCapability -Online | Where-Object Name -like 'OpenSSH.Server*' | Select-Object -First 1
  if (-not $capability) {
    throw 'OpenSSH.Server capability is unavailable on this Windows image.'
  }

  if ($capability.State -ne 'Installed') {
    Write-Host 'Installing OpenSSH Server capability'
    Add-WindowsCapability -Online -Name $capability.Name | Out-Null
  }

  Set-Service -Name sshd -StartupType Automatic
  if ((Get-Service -Name sshd).Status -ne 'Running') {
    Start-Service -Name sshd
  }
  if (-not (Get-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -ErrorAction SilentlyContinue)) {
    New-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -DisplayName 'OpenSSH Server (sshd)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22 | Out-Null
  }
}

function Copy-UnattendTemplate {
  param([string]$Destination)

  $candidateTemplates = @()
  if ($UnattendTemplate) {
    $candidateTemplates += $UnattendTemplate
  }
  $candidateTemplates += @(
    'C:\\Program Files\\Cloudbase Solutions\\Cloudbase-Init\\conf\\Unattend.xml',
    'C:\\Program Files (x86)\\Cloudbase Solutions\\Cloudbase-Init\\conf\\Unattend.xml'
  )

  $template = Resolve-ExistingPath -Explicit '' -Candidates $candidateTemplates -Label 'cloudbase-init Unattend.xml template'
  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Destination) | Out-Null
  Copy-Item -LiteralPath $template -Destination $Destination -Force
  Write-Host "Copied sysprep unattend template to $Destination"
}

Install-CloudbaseInit -InstallerPath (Find-CloudbaseInitInstaller -Explicit $CloudbaseInitInstaller)
Copy-UnattendTemplate -Destination $SysprepOutput

if ($InstallQemuGA) {
  Install-QemuGuestAgent -DriveHint $VirtioDrive
}

if ($EnableOpenSSH) {
  Ensure-OpenSSH
}

Write-Host ''
Write-Host 'Preparation steps completed.'
Write-Host 'Next recommended checks:'
Write-Host '  - Verify cloudbase-init service exists: Get-Service cloudbase-init'
if ($InstallQemuGA) {
  Write-Host '  - Verify QEMU-GA service is running: Get-Service QEMU-GA'
}
if ($EnableOpenSSH) {
  Write-Host '  - Verify sshd is running: Get-Service sshd'
}
Write-Host '  - Review the copied unattend file:' $SysprepOutput

if (-not $SkipSysprep) {
  Write-Host ''
  Write-Host 'Launching sysprep /generalize /oobe /shutdown ...'
  $sysprep = 'C:\\Windows\\System32\\Sysprep\\sysprep.exe'
  $proc = Start-Process -FilePath $sysprep -ArgumentList @('/generalize', '/oobe', '/shutdown', ('/unattend:' + $SysprepOutput)) -Wait -PassThru
  if ($proc.ExitCode -ne 0) {
    throw "sysprep failed with exit code $($proc.ExitCode)"
  }
} else {
  Write-Host ''
  Write-Host 'Skipping sysprep. Run this manually when ready:'
  Write-Host "  C:\\Windows\\System32\\Sysprep\\sysprep.exe /generalize /oobe /shutdown /unattend:$SysprepOutput"
}
