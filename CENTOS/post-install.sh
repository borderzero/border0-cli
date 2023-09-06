#!/bin/bash

function create_config_file {
  echo "Creating config file..."
  echo """
token: ${BORDER0_TOKEN}
""" >/etc/border0/border0.yaml
}

case "$1" in
1)
  # New installation
  echo "Performing new installation."
  if [ -n "$BORDER0_TOKEN" ]; then
    border0 connector install --daemon-only
    create_config_file
  else
    if [ -f /etc/systemd/system/border0.service ]; then
      echo "Looks like border0.service is already installed."
      exit 0
    fi
    echo -e "BORDER0_TOKEN is not set.\nPlease run the install manually... \n'border0 connector install'"
  fi
  ;;
2)
  # Upgrade
  echo "Upgrading Border0 Connector..."
  if systemctl is-active --quiet border0.service; then
    echo "Restarting border0.service..."
    systemctl restart border0.service
  else
    echo -e "Looks like border0.service is not running.\nyou can check the status with 'systemctl status border0.service'\nIt can be started with 'systemctl start border0.service'"
  fi
  ;;
0)
  # Uninstall
  echo "Package is being purged"
  if systemctl is-enabled --quiet border0.service; then systemctl disable border0.service; fi
  if systemctl is-active --quiet border0.service; then systemctl stop border0.service; fi
  if [ -f /etc/systemd/system/border0.service ]; then rm /etc/systemd/system/border0.service; fi
  systemctl daemon-reload
  if [ -f /usr/local/bin/border0 ]; then rm /usr/local/bin/border0; fi
  if [ -f /usr/bin/border0 ]; then rm /usr/bin/border0; fi
  if [ -d /etc/border0 ]; then rm -rf /etc/border0; fi
  ;;
*)
  echo "Unknown argument: $1"
  ;;
esac
