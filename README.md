# aws-cost-sankey

Generate cost analysis Sankey chart from AWS Cost Explorer

## Prerequisite

- Install asdf tool
  ```bash
  brew install asdf
  ```

- Install asdf plugins and versions
  ```bash
  make tools
  ```

## How to use

- Build the code
  ```bash
  make build
  ```

- Update the config file

  TODO: add instruction to update configs/configs.yaml

- Run the code
  ```bash
  ./build/aws-cost-sankey [-c configFile] [-o outputFile]
  ```
