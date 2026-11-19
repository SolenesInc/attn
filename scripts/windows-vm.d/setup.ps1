$ErrorActionPreference = 'Stop'
Start-Transcript -Path C:\attn-setup.log -Append
$media = Split-Path -Parent $MyInvocation.MyCommand.Path

powercfg /change monitor-timeout-ac 0
powercfg /change standby-timeout-ac 0
powercfg /change hibernate-timeout-ac 0
reg add 'HKLM\SOFTWARE\Policies\Microsoft\Windows\Personalization' /v NoLockScreen /t REG_DWORD /d 1 /f
reg add 'HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU' /v NoAutoUpdate /t REG_DWORD /d 1 /f

cscript //nologo C:\Windows\System32\slmgr.vbs /ato

try {
    Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0 | Out-Null
} catch {
    Write-Output "OpenSSH capability failed: $_; installing the MSI from GitHub"
    $release = Invoke-RestMethod https://api.github.com/repos/PowerShell/Win32-OpenSSH/releases/latest
    $asset = $release.assets | Where-Object { $_.name -like '*Win64*.msi' } | Select-Object -First 1
    Invoke-WebRequest $asset.browser_download_url -OutFile C:\openssh.msi
    Start-Process msiexec.exe -Wait -ArgumentList '/i C:\openssh.msi /qn ADDLOCAL=Server'
}
Copy-Item "$media\authorized_keys" C:\ProgramData\ssh\administrators_authorized_keys
icacls C:\ProgramData\ssh\administrators_authorized_keys /inheritance:r /grant 'Administrators:F' /grant 'SYSTEM:F'
Set-Service sshd -StartupType Automatic
Start-Service sshd
if (-not (Get-NetFirewallRule -Name attn-sshd -ErrorAction SilentlyContinue)) {
    New-NetFirewallRule -Name attn-sshd -DisplayName 'OpenSSH Server' -Protocol TCP -LocalPort 22 -Action Allow -Direction Inbound | Out-Null
}

dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart
dism.exe /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart

New-Item -ItemType File -Force C:\attn-setup.done | Out-Null
Stop-Transcript
Restart-Computer -Force
