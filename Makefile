# Install tools using asdf
tools:
	@tools/install-asdf-plugins.sh
	@tools/install-asdf-versions.sh

build:
	@mkdir -p build
	go mod tidy
	go build -o build/aws-cost-sankey ./cmd/aws-cost-sankey
	@chmod a+x build/aws-cost-sankey

run:
	./build/aws-cost-sankey

clean:
	@rm -rf build

.PHONY: tools build clean
