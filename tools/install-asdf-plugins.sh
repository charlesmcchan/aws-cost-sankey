#!/bin/bash
if [ ! -f .tool-versions ]; then
  echo ".tool-versions file not found!"
  exit 1
fi

cat .tool-versions | awk '{print $1}' | while read -r plugin; do
ret=$(asdf plugin-list | grep $plugin)
if [ $? -eq 0 ]; then
  echo "$plugin is already installed. Skipping"
else
  echo "Installing $plugin"
  asdf plugin-add $plugin
  ex=$?
  if [ $ex -eq 2 ] || [ $ex -eq 0 ]; then
    echo "Successfully installed $plugin"
  else
    echo "Error installing $plugin"
    exit $ex
  fi
fi
done
