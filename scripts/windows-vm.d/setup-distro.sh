#!/usr/bin/env bash
set -euo pipefail
user=attn
id "$user" >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --groups sudo "$user"
printf '%s ALL=(ALL) NOPASSWD:ALL\n' "$user" >/etc/sudoers.d/attn
chmod 0440 /etc/sudoers.d/attn
cat >/etc/wsl.conf <<EOF
[boot]
systemd=true

[user]
default=$user
EOF
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq openssh-server
mkdir -p /etc/ssh/sshd_config.d
printf 'Port 2222\nPasswordAuthentication no\n' >/etc/ssh/sshd_config.d/attn-wsl.conf
install -d -m 0700 -o "$user" -g "$user" "/home/$user/.ssh"
install -m 0600 -o "$user" -g "$user" /mnt/c/attn-wsl/authorized_keys "/home/$user/.ssh/authorized_keys"
systemctl enable ssh.service >/dev/null 2>&1 || ln -sf /lib/systemd/system/ssh.service /etc/systemd/system/multi-user.target.wants/ssh.service
