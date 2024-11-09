# Install tools using asdf
tools:
	@tools/install-asdf-plugins.sh
	@tools/install-asdf-versions.sh

build:
	@mkdir -p build
	@GOOS=linux GOARCH=arm64 go build -o build/aws-cost-sankey ./cmd/aws-cost-sankey

clean:
	@rm -rf build

.PHONY: tools build clean
