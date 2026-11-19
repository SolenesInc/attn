param([string]$Distro, [int]$Memory, [int]$Processors)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$work = 'C:\attn-wsl'
New-Item -ItemType Directory -Force $work | Out-Null

cmd /c 'wsl.exe --version >nul 2>&1'
if ($LASTEXITCODE -ne 0) {
    $release = Invoke-RestMethod https://api.github.com/repos/microsoft/WSL/releases/latest
    $asset = $release.assets | Where-Object { $_.name -like '*x64.msi' } | Select-Object -First 1
    Write-Output "Installing $($asset.name)"
    Invoke-WebRequest $asset.browser_download_url -OutFile "$work\wsl.msi"
    $msi = Start-Process msiexec.exe -Wait -PassThru -ArgumentList "/i $work\wsl.msi /qn /norestart"
    if ($msi.ExitCode -notin 0, 3010) { throw "WSL MSI exited $($msi.ExitCode)" }
}
wsl.exe --version

@"
[general]
instanceIdleTimeout=-1

[wsl2]
memory=${Memory}GB
processors=$Processors
vmIdleTimeout=-1
"@ | Set-Content -Encoding ascii "$env:USERPROFILE\.wslconfig"

cmd /c "wsl.exe -d $Distro --exec true >nul 2>&1"
if ($LASTEXITCODE -ne 0) {
    $rootfs = "$work\ubuntu.rootfs.tar.gz"
    if (-not (Test-Path $rootfs)) {
        Write-Output 'Downloading the Ubuntu 24.04 WSL root filesystem'
        Invoke-WebRequest https://cloud-images.ubuntu.com/wsl/releases/24.04/current/ubuntu-noble-wsl-amd64-wsl.rootfs.tar.gz -OutFile "$rootfs.part"
        Move-Item -Force "$rootfs.part" $rootfs
    }
    wsl.exe --import $Distro "$work\$Distro" $rootfs --version 2
    if ($LASTEXITCODE -ne 0) { throw "wsl --import exited $LASTEXITCODE" }
}

wsl.exe -d $Distro -u root -- bash /mnt/c/attn-wsl/setup-distro.sh
if ($LASTEXITCODE -ne 0) { throw "setup-distro.sh exited $LASTEXITCODE" }

$action = New-ScheduledTaskAction -Execute 'wsl.exe' -Argument "-d $Distro --exec true"
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit 0 -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask -Force -TaskName attn-wsl-start -Action $action -Trigger $trigger -Principal $principal -Settings $settings | Out-Null
wsl.exe --shutdown
Start-ScheduledTask -TaskName attn-wsl-start
